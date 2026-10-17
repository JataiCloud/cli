package mcpserver

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jataicloud/cli/internal/config"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// createdApp registra a criação do app e devolve o corpo que o POST recebeu.
func createdApp(m *mcpTest, slug, status string) *map[string]any {
	var body map[string]any
	m.json("GET /api/v1/apps/"+slug, 404, map[string]any{"error": "App não encontrado"})
	m.handle("POST /api/v1/apps", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			App map[string]any `json:"app"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		m.mu.Lock()
		body = in.App
		m.mu.Unlock()
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"id": 1, "slug": slug, "status": status, "stack": in.App["stack"]})
	})
	return &body
}

func TestCreateAppWritesTheProjectAndCreatesTheApp(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	writeFile(t, m.dir, "package.json", "{}")
	body := createdApp(m, "minha-loja", "pending")

	text, isErr := m.call("jatai_criar_app", map[string]any{"pasta": m.dir, "nome": "Minha Loja"})

	if isErr {
		t.Fatalf("error: %s", text)
	}
	if text != "App minha-loja criado no Jataí (stack Node.js, Hex Base). PG Base incluído e DATABASE_URL nas "+
		"variáveis. Próximo passo: jatai_publicar." {
		t.Fatalf("text = %q", text)
	}
	p, _, err := config.LoadProject(m.dir)
	want := config.Project{App: "minha-loja", Name: "Minha Loja", Stack: "node", Preset: "base", Region: "br-se1",
		Branch: "main"}
	if err != nil || p != want {
		t.Fatalf("project = %+v, err = %v", p, err)
	}
	if (*body)["name"] != "Minha Loja" || (*body)["preset"] != "base" || (*body)["stack"] != "node" {
		t.Fatalf("POST body = %v", *body)
	}
}

func TestCreateAppUsesTheFolderNameTheGitBranchAndTheExplicitStack(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	dir := filepath.Join(m.dir, "Portfolio Ana")
	os.Mkdir(dir, 0o755)
	writeFile(t, dir, "package.json", "{}")
	m.deps.Git = func(_ string, args ...string) (string, error) {
		if strings.Join(args, " ") == "rev-parse --abbrev-ref HEAD" {
			return "develop", nil
		}
		return "", os.ErrNotExist
	}
	body := createdApp(m, "portfolio-ana", "pending")

	text, isErr := m.call("jatai_criar_app", map[string]any{"pasta": dir, "stack": "static"})

	assertContains(t, text, "App portfolio-ana criado", "stack Estático")
	assertMissing(t, text, "PG Base")
	p, _, _ := config.LoadProject(dir)
	if isErr || p.Stack != "static" || p.Branch != "develop" || p.Name != "Portfolio Ana" {
		t.Fatalf("isErr = %v, project = %+v", isErr, p)
	}
	if (*body)["repo_branch"] != "develop" {
		t.Fatalf("POST body = %v", *body)
	}
}

func TestCreateAppRefusesTheHomeDirectory(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	m.handle("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) })
	writeFile(t, m.deps.Paths.Home, "package.json", "{}")

	text, isErr := m.call("jatai_criar_app", map[string]any{"pasta": m.deps.Paths.Home})

	assertContains(t, text, "Informe `pasta`")
	if !isErr || m.calls("/") != 0 {
		t.Fatalf("isErr = %v, API calls = %d", isErr, m.calls("/"))
	}
	if _, err := os.Stat(filepath.Join(m.deps.Paths.Home, config.ProjectFile)); err == nil {
		t.Fatal(".jatai.yml written in the home directory")
	}
}

func TestCreateAppAsksForTheStackWhenItCannotDetect(t *testing.T) {
	m := newMCPTest(t)
	m.login()

	text, isErr := m.call("jatai_criar_app", map[string]any{"pasta": m.dir})

	assertContains(t, text, "rails, node, static, docker", "`stack`")
	if !isErr {
		t.Fatal("expected an error")
	}
	if _, err := os.Stat(filepath.Join(m.dir, config.ProjectFile)); err == nil {
		t.Fatal(".jatai.yml written without a stack")
	}
}

func TestCreateAppRejectsAnUnknownStack(t *testing.T) {
	m := newMCPTest(t)
	m.login()

	text, isErr := m.call("jatai_criar_app", map[string]any{"pasta": m.dir, "stack": "python"})

	assertContains(t, text, "python", "rails, node, static, docker")
	if !isErr {
		t.Fatal("expected an error")
	}
}

func TestCreateAppAsksForAUsableName(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	writeFile(t, m.dir, "index.html", "<html>")

	text, isErr := m.call("jatai_criar_app", map[string]any{"pasta": m.dir, "nome": "!!!"})

	assertContains(t, text, "`nome`")
	if !isErr {
		t.Fatal("expected an error")
	}
}

func TestCreateAppExplainsPaymentRequired(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	writeFile(t, m.dir, "package.json", "{}")
	m.json("GET /api/v1/apps/minha-loja", 404, map[string]any{"error": "App não encontrado"})
	m.json("POST /api/v1/apps", 402, map[string]any{"error": "A cortesia já está em uso"})

	text, isErr := m.call("jatai_criar_app", map[string]any{"pasta": m.dir, "nome": "Minha Loja"})

	assertContains(t, text, "A cortesia já está em uso.", "https://jataicloud.com.br/billing")
	if !isErr {
		t.Fatal("expected an error")
	}
}

func TestCreateAppReusesTheProjectFileAndNeverAsksAboveHexBase(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	original := "app: loja-antiga\nname: Loja Antiga\nstack: rails\npreset: escala_1\nregion: br-se1\nstatic:\n  root: public\n"
	writeFile(t, m.dir, config.ProjectFile, original)
	body := createdApp(m, "loja-antiga", "pending")

	text, isErr := m.call("jatai_criar_app", map[string]any{"pasta": m.dir, "nome": "Outro Nome", "stack": "node"})

	assertContains(t, text, "App loja-antiga criado", "stack Rails", "Hex Base")
	data, _ := os.ReadFile(filepath.Join(m.dir, config.ProjectFile))
	if isErr || string(data) != original {
		t.Fatalf("isErr = %v, .jatai.yml changed:\n%s", isErr, data)
	}
	if (*body)["name"] != "Loja Antiga" || (*body)["preset"] != "base" {
		t.Fatalf("POST body = %v", *body)
	}
}

func TestCreateAppWhenTheAppAlreadyExists(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	writeFile(t, m.dir, config.ProjectFile, "app: loja\nstack: node\n")
	m.json("GET /api/v1/apps/loja", 200, map[string]any{"id": 1, "slug": "loja", "status": "active", "stack": "node"})

	text, isErr := m.call("jatai_criar_app", map[string]any{"pasta": m.dir})

	if isErr || text != "O app loja já existe no Jataí (stack Node.js, status active). Próximo passo: jatai_publicar." {
		t.Fatalf("%v %q", isErr, text)
	}
	if m.calls("POST /api/v1/apps") != 0 {
		t.Fatal("created a second app")
	}
}

func TestCreateAppWritesTheSlugTheServerChose(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	writeFile(t, m.dir, "package.json", "{}")
	createdApp(m, "minha-loja", "pending")
	m.handle("POST /api/v1/apps", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"id": 1, "slug": "minha-loja-1", "status": "pending"})
	})

	text, _ := m.call("jatai_criar_app", map[string]any{"pasta": m.dir, "nome": "Minha Loja"})

	assertContains(t, text, "App minha-loja-1 criado")
	if p, _, _ := config.LoadProject(m.dir); p.App != "minha-loja-1" {
		t.Fatalf("app = %q", p.App)
	}
}

func TestCreateAppWithoutLoginAsksForJataiLogin(t *testing.T) {
	m := newMCPTest(t)
	writeFile(t, m.dir, "package.json", "{}")

	text, isErr := m.call("jatai_criar_app", map[string]any{"pasta": m.dir})

	assertContains(t, text, "jatai_login")
	if !isErr {
		t.Fatal("expected an error")
	}
}

func TestAppSlugPrefersTheExplicitAppThenTheProjectFile(t *testing.T) {
	st := newState(Deps{})
	dir := t.TempDir()

	if _, err := st.appSlug(dir, ""); err == nil || err.Error() != "Esta pasta ainda não tem app no Jataí. "+
		"Chame jatai_criar_app." {
		t.Fatalf("err = %v", err)
	}
	writeFile(t, dir, config.ProjectFile, "app: loja\n")
	if got, err := st.appSlug(dir, ""); err != nil || got != "loja" {
		t.Fatalf("slug = %q, err = %v", got, err)
	}
	if got, err := st.appSlug(dir, " Outra Loja "); err != nil || got != "outra-loja" {
		t.Fatalf("slug = %q, err = %v", got, err)
	}
	if _, err := st.appSlug(dir, "!!!"); err == nil {
		t.Fatal("accepted an app without letters or digits")
	}
}
