package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jataicloud/cli/internal/cable/cabletest"
	"github.com/jataicloud/cli/internal/config"
	"github.com/jataicloud/cli/internal/pack"
)

func withSource(e *testEnv) {
	os.WriteFile(filepath.Join(e.dir, "index.html"), []byte("<h1>oi</h1>"), 0o644)
}

func acceptUpload(e *testEnv, slug string, id int, branches *[]string) {
	e.handle("POST /api/v1/apps/"+slug+"/deploys", func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(1 << 20)
		if _, _, err := r.FormFile("source"); err != nil {
			http.Error(w, "sem source", http.StatusUnprocessableEntity)
			return
		}
		*branches = append(*branches, r.FormValue("branch"))
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": id, "status": "queued"})
	})
}

func TestDeployStreamsTheBuildOverTheWebSocket(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.writeProject("app: loja\nbranch: main\n")
	withSource(e)
	var branches []string
	acceptUpload(e, "loja", 42, &branches)
	e.mux.Handle("/cable", &cabletest.Handler{Messages: map[string][]map[string]any{
		"DeployLogChannel":    {{"line": "#1 Construindo imagem"}},
		"DeployStatusChannel": {{"status": "live"}},
	}})

	code := e.run("deploy", "--branch", "hotfix")

	if code != 0 || branches[0] != "hotfix" {
		t.Fatalf("code = %d, branches = %v, err = %q", code, branches, e.err)
	}
	for _, s := range []string{"Deploy #42 criado.", "#1 Construindo imagem", "Deploy concluído com sucesso!"} {
		if !strings.Contains(e.out.String(), s) {
			t.Errorf("missing %q in %q", s, e.out)
		}
	}
}

func TestDeployFallsBackToPollingWithoutWebSocket(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.writeProject("app: loja\n")
	withSource(e)
	var branches []string
	acceptUpload(e, "loja", 42, &branches)
	statuses := []string{"building", "live"}
	e.handle("GET /api/v1/apps/loja/deploys/42", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id": 42, "status": statuses[0]})
		statuses = statuses[1:]
	})

	code := e.run("deploy")

	if code != 0 || !strings.Contains(e.err.String(), "Usando polling") || !strings.Contains(e.out.String(), "Deploy concluído") {
		t.Fatalf("code = %d, out = %q, err = %q", code, e.out, e.err)
	}
}

func TestDeployFallsBackToPollingWhenTheSocketDropsForGood(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.writeProject("app: loja\n")
	withSource(e)
	var branches []string
	acceptUpload(e, "loja", 42, &branches)
	cable := &cabletest.Handler{DropAfterFirstSubscribe: true}
	var connections atomic.Int32
	e.handle("/cable", func(w http.ResponseWriter, r *http.Request) {
		if connections.Add(1) > 1 {
			http.NotFound(w, r)
			return
		}
		cable.ServeHTTP(w, r)
	})
	e.json("GET /api/v1/apps/loja/deploys/42", http.StatusOK, map[string]any{"id": 42, "status": "live"})

	code := e.run("deploy")

	if code != 0 || !strings.Contains(e.err.String(), "Usando polling") || !strings.Contains(e.out.String(), "Deploy concluído") {
		t.Fatalf("code = %d, out = %q, err = %q", code, e.out, e.err)
	}
}

func TestDeployFailureExitsWithOne(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.writeProject("app: loja\n")
	withSource(e)
	var branches []string
	acceptUpload(e, "loja", 42, &branches)
	e.mux.Handle("/cable", &cabletest.Handler{Messages: map[string][]map[string]any{
		"DeployStatusChannel": {{"status": "failed"}},
	}})

	if code := e.run("deploy"); code != 1 || !strings.Contains(e.err.String(), "Deploy falhou") {
		t.Fatalf("code = %d, err = %q", code, e.err)
	}
}

func TestDeployPartiallyLiveListsTheProcessesLeftBehind(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.writeProject("app: loja\n")
	withSource(e)
	var branches []string
	acceptUpload(e, "loja", 42, &branches)
	e.mux.Handle("/cable", &cabletest.Handler{Messages: map[string][]map[string]any{
		"DeployStatusChannel": {{"status": "partially_live"}},
	}})
	e.json("GET /api/v1/apps/loja/deploys/42", http.StatusOK, map[string]any{
		"id": 42, "status": "partially_live",
		"process_failures": []map[string]string{{"name": "worker", "reason": "OOMKilled"}},
	})

	code := e.run("deploy")

	if code != 0 || !strings.Contains(e.out.String(), "worker: OOMKilled") {
		t.Fatalf("code = %d, out = %q", code, e.out)
	}
}

func TestDeployPollingReportsAnExpiredSession(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.writeProject("app: loja\n")
	withSource(e)
	var branches []string
	acceptUpload(e, "loja", 42, &branches)
	e.json("GET /api/v1/apps/loja/deploys/42", http.StatusUnauthorized, map[string]any{"error": "invalid_token"})

	code := e.run("deploy")

	if code != 1 || !strings.Contains(e.err.String(), "O deploy continua no servidor") {
		t.Fatalf("code = %d, err = %q", code, e.err)
	}
}

func TestDeployRefusesAPackageOverTheLimit(t *testing.T) {
	previous := pack.MaxArchiveSize
	pack.MaxArchiveSize = 1
	defer func() { pack.MaxArchiveSize = previous }()
	e := newTestEnv(t)
	e.login()
	e.writeProject("app: loja\n")
	withSource(e)

	if code := e.run("deploy"); code != 1 || !strings.Contains(e.err.String(), "limite é 0 MB") {
		t.Fatalf("code = %d, err = %q", code, e.err)
	}
}

func TestUpCreatesProvisionsDeploysAndPersistsTheServerSlug(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.writeProject("app: Loja\nstack: rails\npreset: base\nregion: br-se1\nbranch: main\nstatic:\n  root: public\n")
	withSource(e)
	e.json("GET /api/v1/apps/loja", http.StatusNotFound, map[string]any{"error": "not found"})
	var created map[string]map[string]any
	e.handle("POST /api/v1/apps", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&created)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": 7, "slug": "loja-1", "status": "pending", "url": "https://loja-1.jataiapp.com.br"})
	})
	e.json("POST /api/v1/apps/loja-1/provision", http.StatusOK, map[string]any{})
	var branches []string
	acceptUpload(e, "loja-1", 42, &branches)
	e.mux.Handle("/cable", &cabletest.Handler{Messages: map[string][]map[string]any{
		"ProvisionStatusChannel": {{"status": "provisioning"}, {"status": "active"}},
		"DeployStatusChannel":    {{"status": "live"}},
	}})

	code := e.run("up")

	if code != 0 || created["app"]["name"] != "Loja" || created["app"]["stack"] != "rails" {
		t.Fatalf("code = %d, created = %v, err = %q", code, created, e.err)
	}
	p, _, _ := config.LoadProject(e.dir)
	data, _ := os.ReadFile(filepath.Join(e.dir, config.ProjectFile))
	if p.App != "loja-1" || p.Name != "Loja" || !strings.Contains(string(data), "root: public") {
		t.Fatalf("project file = %s", data)
	}
	if !strings.Contains(e.out.String(), "App disponível em https://loja-1.jataiapp.com.br") {
		t.Fatalf("out = %q", e.out)
	}
}

func TestUpReportsAValidationErrorOnCreate(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.writeProject("app: loja\n")
	e.json("GET /api/v1/apps/loja", http.StatusNotFound, map[string]any{})
	e.json("POST /api/v1/apps", http.StatusUnprocessableEntity, map[string]any{"errors": []string{"Nome já está em uso"}})

	if code := e.run("up"); code != 1 || !strings.Contains(e.err.String(), "Erro ao criar app: Nome já está em uso") {
		t.Fatalf("code = %d, err = %q", code, e.err)
	}
}
