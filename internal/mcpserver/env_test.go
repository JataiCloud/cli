package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// envPut guarda o corpo do PUT …/env e responde 200.
func envPut(m *mcpTest) func() map[string]any {
	var mu sync.Mutex
	var body map[string]any
	m.handle("PUT /api/v1/apps/loja/env", func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		_ = json.Unmarshal(data, &body)
		mu.Unlock()
		writeJSON(w, map[string]any{"message": "Variáveis atualizadas"})
	})
	return func() map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return body
	}
}

func TestEnvListShowsOnlyKeysEvenIfTheAPISendsValues(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	var masked string
	m.handle("GET /api/v1/apps/loja/env", func(w http.ResponseWriter, r *http.Request) {
		masked = r.URL.Query().Get("masked")
		writeJSON(w, map[string]any{"API_KEY": "s3cr3t", "DATABASE_URL": "postgres://u:p4ss@h/db", "PORT": 3000})
	})

	text, isErr := m.call("jatai_variaveis_listar", map[string]any{"pasta": m.dir})

	want := "Variáveis de loja (3):\nAPI_KEY\nDATABASE_URL\nPORT\nValores não são exibidos."
	if isErr || text != want {
		t.Fatalf("%v %q", isErr, text)
	}
	if masked != "true" {
		t.Fatalf("masked = %q", masked)
	}
	assertMissing(t, text, "s3cr3t", "p4ss", "3000")
}

func TestEnvListWithoutVariables(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	m.json("GET /api/v1/apps/loja/env", 200, map[string]any{})

	text, isErr := m.call("jatai_variaveis_listar", map[string]any{"pasta": m.dir})

	if isErr || text != "O app loja não tem variáveis de ambiente." {
		t.Fatalf("%v %q", isErr, text)
	}
}

func TestEnvSetNeverEchoesValues(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	sent := envPut(m)

	text, isErr := m.call("jatai_variaveis_definir",
		map[string]any{"pasta": m.dir, "variaveis": map[string]any{"API_KEY": "s3cr3t", "A_FLAG": "on"}})

	if isErr || text != "Definidas: API_KEY, A_FLAG. Valem no próximo deploy." {
		t.Fatalf("%v %q", isErr, text)
	}
	env, _ := sent()["env"].(map[string]any)
	if env["API_KEY"] != "s3cr3t" || env["A_FLAG"] != "on" || len(env) != 2 {
		t.Fatalf("PUT body = %v", sent())
	}
	assertMissing(t, text, "s3cr3t")
	if m.calls("POST /api/v1/apps/loja/deploys") != 0 {
		t.Fatal("published without being asked to")
	}
}

func TestEnvSetWithPublishStartsADeploy(t *testing.T) {
	m := newMCPTest(t)
	m.deps.Budget = 5 * time.Second
	lojaProject(t, m)
	envPut(m)
	deployStatuses(m, "building", "live")

	text, isErr := m.call("jatai_variaveis_definir",
		map[string]any{"pasta": m.dir, "variaveis": map[string]any{"API_KEY": "s3cr3t"}, "publicar": true})

	if isErr || !strings.HasPrefix(text, "Definidas: API_KEY. Valem no próximo deploy.\nPublicado: "+lojaURL+"\n") {
		t.Fatalf("%v %q", isErr, text)
	}
	assertMissing(t, text, "s3cr3t")
	if m.calls("POST /api/v1/apps/loja/deploys") != 1 {
		t.Fatalf("deploys = %d", m.calls("POST /api/v1/apps/loja/deploys"))
	}
}

func TestEnvSetWithPublishReportsAFailedDeployAfterTheConfirmation(t *testing.T) {
	m := newMCPTest(t)
	m.deps.Budget = 5 * time.Second
	lojaProject(t, m)
	envPut(m)
	deployStatuses(m, "failed")
	m.json("GET /api/v1/apps/loja/deploys/7/logs", 200, []map[string]any{{"message": "npm ERR! build quebrou"}})

	text, isErr := m.call("jatai_variaveis_definir",
		map[string]any{"pasta": m.dir, "variaveis": map[string]any{"A": "b"}, "publicar": true})

	if !isErr {
		t.Fatal("a failed deploy should be an error result")
	}
	assertContains(t, text, "Definidas: A. Valem no próximo deploy.\nO deploy #7 de loja falhou.")
}

func TestEnvSetWithPublishJoinsARunningDeployInsteadOfStartingAnother(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	envPut(m)
	gatedDeploy(m)
	m.call("jatai_publicar", map[string]any{"pasta": m.dir})

	text, _ := m.call("jatai_variaveis_definir",
		map[string]any{"pasta": m.dir, "variaveis": map[string]any{"A": "b"}, "publicar": true})

	assertContains(t, text, "Definidas: A.", "Deploy #7 de loja em andamento")
	if m.calls("POST /api/v1/apps/loja/deploys") != 1 {
		t.Fatalf("deploys = %d", m.calls("POST /api/v1/apps/loja/deploys"))
	}
}

func TestEnvSetWithoutVariablesAsksForAtLeastOne(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	envPut(m)

	text, isErr := m.call("jatai_variaveis_definir", map[string]any{"pasta": m.dir, "variaveis": map[string]any{}})
	if !isErr || text != errNoVariables.Error() {
		t.Fatalf("%v %q", isErr, text)
	}
	// O esquema do SDK já recusa `variaveis: null` antes do handler.
	text, isErr = m.call("jatai_variaveis_definir", map[string]any{"pasta": m.dir, "variaveis": nil})
	if !isErr || !strings.Contains(text, "variaveis") {
		t.Fatalf("%v %q", isErr, text)
	}
	text, isErr = m.call("jatai_variaveis_definir",
		map[string]any{"pasta": m.dir, "variaveis": map[string]any{"  ": "x"}})
	if !isErr || !strings.Contains(text, "sem nome") {
		t.Fatalf("%v %q", isErr, text)
	}
	if m.calls("PUT /api/v1/apps/loja/env") != 0 {
		t.Fatal("called the API with nothing to set")
	}
}

func TestEnvSetNeverEchoesAValueFromAValidationError(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	m.json("PUT /api/v1/apps/loja/env", 422,
		map[string]any{"errors": []string{"Valor 's3cr3t-longo' inválido para API_KEY"}})

	text, isErr := m.call("jatai_variaveis_definir",
		map[string]any{"pasta": m.dir, "variaveis": map[string]any{"API_KEY": "s3cr3t-longo"}})

	if !isErr || text != "A API recusou as variáveis: verifique os nomes." {
		t.Fatalf("%v %q", isErr, text)
	}
	assertMissing(t, text, "s3cr3t")
}

func TestEnvSetShowsTheAPIMessageWhenItEchoesNoValue(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	m.json("PUT /api/v1/apps/loja/env", 422, map[string]any{"errors": []string{"Chave não pode começar com número"}})

	text, isErr := m.call("jatai_variaveis_definir",
		map[string]any{"pasta": m.dir, "variaveis": map[string]any{"1A": "s3cr3t"}})

	if !isErr || text != "Chave não pode começar com número." {
		t.Fatalf("%v %q", isErr, text)
	}
}

func TestEnvSetKeepsTheConstantMessagesOfNon422StatusesEvenWithAShortValue(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	m.json("PUT /api/v1/apps/loja/env", 401, map[string]any{"error": "Sessão expirada: 1 vez só"})

	text, isErr := m.call("jatai_variaveis_definir",
		map[string]any{"pasta": m.dir, "variaveis": map[string]any{"DEBUG": "1"}})

	if !isErr || text != errNotLoggedIn.Error() {
		t.Fatalf("%v %q", isErr, text)
	}
}

func TestEnvSetIgnoresShortValuesInTheEchoGuard(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	m.json("PUT /api/v1/apps/loja/env", 422, map[string]any{"errors": []string{"Limite de 100 variáveis por app"}})

	// "10" aparece em "100", mas valores com menos de 4 caracteres não contam como eco.
	text, isErr := m.call("jatai_variaveis_definir",
		map[string]any{"pasta": m.dir, "variaveis": map[string]any{"WORKERS": "10"}})

	if !isErr || text != "Limite de 100 variáveis por app." {
		t.Fatalf("%v %q", isErr, text)
	}
}

func TestEnvSetDoesNotApplyTheEchoGuardTo409WithALongValue(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	m.json("PUT /api/v1/apps/loja/env", 409, map[string]any{"error": "Deploy em andamento"})

	text, isErr := m.call("jatai_variaveis_definir",
		map[string]any{"pasta": m.dir, "variaveis": map[string]any{"MODE": "andamento"}})

	if !isErr || text != "Deploy em andamento. Use jatai_status para acompanhar." {
		t.Fatalf("%v %q", isErr, text)
	}
}

func TestEnvUnsetReservedKeyExplains(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	m.json("DELETE /api/v1/apps/loja/env/DATABASE_URL", 422, map[string]any{"error": "DATABASE_URL é reservada"})

	text, isErr := m.call("jatai_variaveis_remover", map[string]any{"pasta": m.dir, "chaves": []string{"DATABASE_URL"}})

	if !isErr || text != "DATABASE_URL é reservada." {
		t.Fatalf("%v %q", isErr, text)
	}
}

func TestEnvUnsetEscapesAndJoinsTheKeys(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	var path string
	m.handle("DELETE /api/v1/apps/loja/env/{keys}", func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		writeJSON(w, map[string]any{"message": "removidas"})
	})

	text, isErr := m.call("jatai_variaveis_remover",
		map[string]any{"pasta": m.dir, "chaves": []string{"A_KEY", " B KEY ", "A_KEY"}})

	if isErr || text != "Removidas: A_KEY, B KEY. Valem no próximo deploy." {
		t.Fatalf("%v %q", isErr, text)
	}
	if path != "/api/v1/apps/loja/env/A_KEY,B%20KEY" {
		t.Fatalf("path = %q", path)
	}
}

func TestEnvUnsetValidatesTheKeysBeforeCallingTheAPI(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	m.handle("DELETE /api/v1/apps/loja/env/{keys}", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{})
	})
	for chaves, want := range map[string]string{
		"null":      errNoKeys.Error(),
		"empty":     errNoKeys.Error(),
		"blank key": "chave vazia",
		"comma":     "tem vírgula",
	} {
		args := map[string]any{"pasta": m.dir}
		switch chaves {
		case "null":
			args["chaves"] = nil
		case "empty":
			args["chaves"] = []string{}
		case "blank key":
			args["chaves"] = []string{"A", " "}
		case "comma":
			args["chaves"] = []string{"A,B"}
		}
		text, isErr := m.call("jatai_variaveis_remover", args)
		if !isErr || !strings.Contains(text, want) {
			t.Fatalf("%s: %v %q", chaves, isErr, text)
		}
	}
	if m.calls("DELETE /api/v1/apps/loja/env/{keys}") != 0 {
		t.Fatal("called the API with invalid keys")
	}
}

func TestEnvUnsetWithPublishAppendsTheDeployResult(t *testing.T) {
	m := newMCPTest(t)
	m.deps.Budget = 5 * time.Second
	lojaProject(t, m)
	m.json("DELETE /api/v1/apps/loja/env/OLD", 200, map[string]any{"message": "ok"})
	deployStatuses(m, "live")

	text, isErr := m.call("jatai_variaveis_remover",
		map[string]any{"pasta": m.dir, "chaves": []string{"OLD"}, "publicar": true})

	if isErr || !strings.HasPrefix(text, "Removidas: OLD. Valem no próximo deploy.\nPublicado: "+lojaURL) {
		t.Fatalf("%v %q", isErr, text)
	}
}

func TestEnvToolsWithoutLoginOrAppExplainTheNextStep(t *testing.T) {
	m := newMCPTest(t)
	for name, args := range map[string]map[string]any{
		"jatai_variaveis_listar":  {},
		"jatai_variaveis_definir": {"variaveis": map[string]any{"A": "b"}},
		"jatai_variaveis_remover": {"chaves": []string{"A"}},
		"jatai_logs":              {},
	} {
		if text, isErr := m.call(name, args); !isErr || !strings.Contains(text, "jatai_login") {
			t.Fatalf("%s: %v %q", name, isErr, text)
		}
	}
	m.login()
	for name, args := range map[string]map[string]any{
		"jatai_variaveis_listar":  {"pasta": m.dir},
		"jatai_variaveis_definir": {"pasta": m.dir, "variaveis": map[string]any{"A": "b"}},
		"jatai_variaveis_remover": {"pasta": m.dir, "chaves": []string{"A"}},
	} {
		if text, isErr := m.call(name, args); !isErr || text != errNoApp.Error() {
			t.Fatalf("%s: %v %q", name, isErr, text)
		}
	}
}
