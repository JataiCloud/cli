package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jataicloud/cli/internal/api"
	"github.com/jataicloud/cli/internal/config"
	"github.com/jataicloud/cli/internal/deployflow"
)

func accountBody(courtesy map[string]any, payment bool) map[string]any {
	return map[string]any{
		"user": map[string]any{"name": "Ana", "email": "ana@x.com"}, "team": map[string]any{"name": "Time da Ana", "slug": "time"},
		"apps": map[string]any{"count": 1, "limit": 50}, "courtesy": courtesy, "payment_method": payment,
	}
}

func TestToolsWithoutLoginAskForJataiLogin(t *testing.T) {
	text, isErr := newMCPTest(t).call("jatai_conta", nil)
	if !isErr || !strings.Contains(text, "jatai_login") {
		t.Fatalf("%v %q", isErr, text)
	}
}

func TestLoginReturnsTheLinkAndCodeAtOnce(t *testing.T) {
	m := newMCPTest(t)
	m.json("POST /api/v1/cli/device_authorizations", 201, deviceStart())
	m.json("POST /api/v1/cli/device_authorizations/token", 400, map[string]any{"error": "authorization_pending"})

	text, isErr := m.call("jatai_login", nil)

	assertContains(t, text, "https://jataicloud.com.br/cli/autorizar?codigo=WDJB-MJHT", "WDJB-MJHT",
		"chame jatai_login de novo", signupHint)
	assertMissing(t, text, "dev_secret_123")
	if isErr || m.calls("POST /api/v1/cli/device_authorizations/token") != 0 {
		t.Fatalf("isErr = %v, token polls = %d", isErr, m.calls("POST /api/v1/cli/device_authorizations/token"))
	}
	if m.savedToken() != "" {
		t.Fatal("token saved before approval")
	}
}

func TestLoginSendsTheMachineInfoAndOpensTheBrowserWhenItCan(t *testing.T) {
	m := newMCPTest(t)
	m.deps.CanOpenBrowser = func() bool { return true }
	var client map[string]string
	m.handle("POST /api/v1/cli/device_authorizations", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Client map[string]string }
		json.NewDecoder(r.Body).Decode(&body)
		client = body.Client
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(deviceStart())
	})

	text, _ := m.call("jatai_login", nil)

	assertContains(t, text, "navegador")
	if len(m.opened) != 1 || m.opened[0] != "https://jataicloud.com.br/cli/autorizar?codigo=WDJB-MJHT" {
		t.Fatalf("opened = %v", m.opened)
	}
	if client["hostname"] != "maquina-teste" || client["version"] != "2.0.0" {
		t.Fatalf("client = %v", client)
	}
}

func TestLoginSecondCallSavesTheTokenWithoutPrintingIt(t *testing.T) {
	m := newMCPTest(t)
	m.json("POST /api/v1/cli/device_authorizations", 201, deviceStart())
	m.call("jatai_login", nil)
	m.json("POST /api/v1/cli/device_authorizations/token", 200,
		map[string]any{"token": "jat_secret", "user": map[string]any{"name": "Ana", "email": "ana@x.com"}})

	text, isErr := m.call("jatai_login", nil)

	assertContains(t, text, "Logado como Ana (ana@x.com)", "jatai_conta")
	assertMissing(t, text, "jat_secret", "dev_secret_123", signupHint)
	if isErr || m.savedToken() != "jat_secret" {
		t.Fatalf("isErr = %v, token not saved", isErr)
	}
	if m.calls("POST /api/v1/cli/device_authorizations") != 1 {
		t.Fatal("second call started a new device authorization")
	}
}

func TestLoginSecondCallWhileStillPendingSaysToCallAgain(t *testing.T) {
	m := newMCPTest(t)
	m.json("POST /api/v1/cli/device_authorizations", 201, deviceStart())
	m.json("POST /api/v1/cli/device_authorizations/token", 400, map[string]any{"error": "authorization_pending"})
	m.call("jatai_login", nil)

	text, isErr := m.call("jatai_login", nil)

	assertContains(t, text, "Ainda aguardando", "WDJB-MJHT", "jatai_login de novo", signupHint)
	if isErr || m.calls("POST /api/v1/cli/device_authorizations/token") == 0 {
		t.Fatalf("isErr = %v, polls = %d", isErr, m.calls("POST /api/v1/cli/device_authorizations/token"))
	}
	if m.calls("POST /api/v1/cli/device_authorizations") != 1 {
		t.Fatal("pending login was discarded")
	}
}

func TestLoginExpiredCodeStartsOverOnTheNextCall(t *testing.T) {
	m := newMCPTest(t)
	m.json("POST /api/v1/cli/device_authorizations", 201, deviceStart())
	m.json("POST /api/v1/cli/device_authorizations/token", 400, map[string]any{"error": "expired_token"})
	m.call("jatai_login", nil)

	text, isErr := m.call("jatai_login", nil)
	assertContains(t, text, "expirou", "jatai_login de novo")
	assertMissing(t, text, "jatai login`", signupHint)
	if !isErr {
		t.Fatal("expired login should be an error")
	}

	again, _ := m.call("jatai_login", nil)
	assertContains(t, again, "https://jataicloud.com.br/cli/autorizar?codigo=WDJB-MJHT")
	if m.calls("POST /api/v1/cli/device_authorizations") != 2 {
		t.Fatalf("device authorizations = %d", m.calls("POST /api/v1/cli/device_authorizations"))
	}
}

func TestLoginDeniedInTheBrowserDiscardsThePendingCode(t *testing.T) {
	m := newMCPTest(t)
	m.json("POST /api/v1/cli/device_authorizations", 201, deviceStart())
	m.json("POST /api/v1/cli/device_authorizations/token", 400, map[string]any{"error": "access_denied"})
	m.call("jatai_login", nil)

	text, isErr := m.call("jatai_login", nil)

	assertContains(t, text, "recusado", "jatai_login de novo")
	assertMissing(t, text, signupHint)
	if !isErr {
		t.Fatal("denied login should be an error")
	}
}

func TestLoginWhenAlreadyLoggedInOnlySaysWho(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	m.json("GET /api/v1/account", 200, accountBody(map[string]any{"available": true, "app": nil}, false))
	m.json("POST /api/v1/cli/device_authorizations", 201, deviceStart())

	text, isErr := m.call("jatai_login", nil)

	assertContains(t, text, "Já está logado como Ana (ana@x.com)")
	assertMissing(t, text, signupHint)
	if isErr || m.calls("POST /api/v1/cli/device_authorizations") != 0 {
		t.Fatalf("isErr = %v, device authorizations = %d", isErr, m.calls("POST /api/v1/cli/device_authorizations"))
	}
}

func TestLoginWithAnExpiredSavedTokenStartsANewLogin(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	m.json("GET /api/v1/account", 401, map[string]any{"error": "invalid_token"})
	m.json("POST /api/v1/cli/device_authorizations", 201, deviceStart())

	text, _ := m.call("jatai_login", nil)

	assertContains(t, text, "WDJB-MJHT")
}

func TestLoginWithAnInvalidJataiTokenVariableExplainsIt(t *testing.T) {
	m := newMCPTest(t)
	m.vars["JATAI_TOKEN"] = "jat_env_ruim"
	m.json("GET /api/v1/account", 401, map[string]any{"error": "invalid_token"})
	m.json("POST /api/v1/cli/device_authorizations", 201, deviceStart())

	text, isErr := m.call("jatai_login", nil)

	assertContains(t, text, "JATAI_TOKEN")
	assertMissing(t, text, "jat_env_ruim")
	if !isErr || m.calls("POST /api/v1/cli/device_authorizations") != 0 {
		t.Fatalf("isErr = %v", isErr)
	}
}

func TestAccountShowsTeamAppsAndCourtesy(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	m.json("GET /api/v1/account", 200, accountBody(map[string]any{"available": false, "app": "loja"}, false))
	m.json("GET /api/v1/apps", 200, []map[string]any{{"slug": "loja", "status": "active"}})

	text, isErr := m.call("jatai_conta", nil)

	assertContains(t, text,
		"Logado como Ana (ana@x.com) · time Time da Ana · apps: 1 de 50 (loja: active).",
		"Cortesia: em uso pelo app loja.",
		"Meio de pagamento: não cadastrado — necessário para apps além da cortesia: https://jataicloud.com.br/billing")
	if isErr {
		t.Fatal("unexpected error")
	}
}

func TestAccountWithFreeCourtesyAndPaymentMethod(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	body := accountBody(map[string]any{"available": true, "app": nil}, true)
	body["apps"] = map[string]any{"count": 0, "limit": 50}
	m.json("GET /api/v1/account", 200, body)
	m.json("GET /api/v1/apps", 200, []map[string]any{})

	text, _ := m.call("jatai_conta", nil)

	assertContains(t, text, "apps: 0 de 50.", "Cortesia: livre — 1 Hex Base + 1 PG Base sem cartão.",
		"Meio de pagamento: cadastrado.", "jatai_criar_app")
}

func TestAccountCourtesyTakenByADeletedApp(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	m.json("GET /api/v1/account", 200, accountBody(map[string]any{"available": false, "app": nil}, false))
	m.json("GET /api/v1/apps", 200, []map[string]any{})

	text, _ := m.call("jatai_conta", nil)

	assertContains(t, text, "Cortesia: em uso neste período (um app de cortesia foi apagado recentemente).")
	assertMissing(t, text, "pelo app")
}

func TestAccountTellsWhetherTheFolderAlreadyHasAnApp(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	m.json("GET /api/v1/account", 200, accountBody(map[string]any{"available": true, "app": nil}, false))
	m.json("GET /api/v1/apps", 200, []map[string]any{})

	text, _ := m.call("jatai_conta", map[string]any{"pasta": m.dir})

	assertContains(t, text, m.dir, "ainda não tem app", "jatai_criar_app")
}

func TestAccountWithAnExpiredTokenAsksForLogin(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	m.json("GET /api/v1/account", 401, map[string]any{"error": "invalid_token"})

	text, isErr := m.call("jatai_conta", nil)

	if !isErr || text != "Você não está logado no Jataí. Chame a ferramenta jatai_login." {
		t.Fatalf("%v %q", isErr, text)
	}
}

func TestExplainPaymentRequiredPointsToBilling(t *testing.T) {
	msg := explain(&api.Error{Status: 402, Message: "Cadastre um meio de pagamento."})
	assertContains(t, msg, "Cadastre um meio de pagamento.", "1 Hex Base + 1 PG Base")
	if !strings.HasSuffix(msg, "https://jataicloud.com.br/billing") {
		t.Fatalf("402 text must end with the billing link: %q", msg)
	}
}

func TestExplainMapsEachFailureToTheNextStep(t *testing.T) {
	st := newState(Deps{UpdateHint: "brew upgrade jatai", Getenv: func(k string) string {
		if k == "JATAI_API_URL" {
			return "https://api.exemplo.com.br"
		}
		return ""
	}})
	netErr := fmt.Errorf("x: %w", &url.Error{Op: "Get", URL: "https://api.exemplo.com.br/api/v1/apps", Err: errors.New("dial")})
	cases := []struct {
		name string
		err  error
		want []string
	}{
		{"no token", errNotLoggedIn, []string{"Você não está logado no Jataí. Chame a ferramenta jatai_login."}},
		{"401", &api.Error{Status: 401, Message: "Sua sessão expirou. Rode `jatai login`."}, []string{"jatai_login"}},
		{"session expired", fmt.Errorf("x: %w", deployflow.ErrSessionExpired), []string{"jatai_login"}},
		{"403", &api.Error{Status: 403, Message: "Limite de apps atingido"}, []string{"Limite de apps atingido", "https://jataicloud.com.br/billing"}},
		{"404", &api.Error{Status: 404, Message: "Recurso não encontrado."}, []string{"App não encontrado neste time. Use jatai_conta para ver os apps."}},
		{"409", &api.Error{Status: 409, Message: "Deploy em andamento"}, []string{"Deploy em andamento", "Use jatai_status para acompanhar."}},
		{"422", &api.Error{Status: 422, Message: "Nome já está em uso"}, []string{"Nome já está em uso"}},
		{"426", &api.Error{Status: 426, Message: "Sua CLI está desatualizada."}, []string{"Esta versão da CLI ficou para trás. Atualize com: brew upgrade jatai"}},
		{"network", netErr, []string{"Sem conexão com a API do Jataí (https://api.exemplo.com.br). Tente de novo em instantes."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := st.explain(tc.err)
			assertContains(t, got, tc.want...)
			assertMissing(t, got, "jatai login`")
		})
	}
	if got := st.explain(&api.Error{Status: 403, Message: "Sem permissão"}); !strings.HasSuffix(got, "https://jataicloud.com.br/billing") {
		t.Fatalf("403 text must end with the billing link: %q", got)
	}
}

func TestLoginKeepsTheCodeThroughATemporaryServerError(t *testing.T) {
	for _, status := range []int{502, 503, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			m := newMCPTest(t)
			m.json("POST /api/v1/cli/device_authorizations", 201, deviceStart())
			m.call("jatai_login", nil)
			m.json("POST /api/v1/cli/device_authorizations/token", status, map[string]any{"error": "indisponível"})

			text, isErr := m.call("jatai_login", nil)
			assertContains(t, text, "Ainda aguardando a aprovação de WDJB-MJHT", "jatai_login de novo", signupHint)
			if isErr {
				t.Fatalf("temporary error reported as failure: %q", text)
			}

			m.handle("POST /api/v1/cli/device_authorizations/token", func(w http.ResponseWriter, r *http.Request) {
				var body map[string]string
				json.NewDecoder(r.Body).Decode(&body)
				if body["device_code"] != "dev_secret_123" {
					w.WriteHeader(400)
					json.NewEncoder(w).Encode(map[string]any{"error": "expired_token"})
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"token": "jat_secret", "user": map[string]any{"name": "Ana", "email": "ana@x.com"}})
			})
			text, _ = m.call("jatai_login", nil)
			assertContains(t, text, "Logado como Ana")
			if m.savedToken() != "jat_secret" || m.calls("POST /api/v1/cli/device_authorizations") != 1 {
				t.Fatalf("token = %q, device authorizations = %d", m.savedToken(), m.calls("POST /api/v1/cli/device_authorizations"))
			}
		})
	}
}

func TestLoginDiscardsTheCodeOnOtherClientErrors(t *testing.T) {
	m := newMCPTest(t)
	m.json("POST /api/v1/cli/device_authorizations", 201, deviceStart())
	m.call("jatai_login", nil)
	m.json("POST /api/v1/cli/device_authorizations/token", 404, map[string]any{"error": "not_found"})

	text, isErr := m.call("jatai_login", nil)

	assertContains(t, text, "jatai_login de novo para gerar outro código")
	assertMissing(t, text, "App não encontrado", "Use jatai_conta")
	if !isErr {
		t.Fatal("4xx should discard the code and fail")
	}
	m.call("jatai_login", nil)
	if m.calls("POST /api/v1/cli/device_authorizations") != 2 {
		t.Fatal("next call did not start a new code")
	}
}

func TestConcurrentLoginAnswersAtOnceWithTheWaitingText(t *testing.T) {
	m := newMCPTest(t)
	m.deps.Budget = 10 * time.Second
	m.json("POST /api/v1/cli/device_authorizations", 201, deviceStart())
	m.call("jatai_login", nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	m.handle("POST /api/v1/cli/device_authorizations/token", func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(entered) })
		<-release
		json.NewEncoder(w).Encode(map[string]any{"token": "jat_secret", "user": map[string]any{"name": "Ana", "email": "ana@x.com"}})
	})
	first := make(chan string, 1)
	go func() {
		text, _ := m.call("jatai_login", nil)
		first <- text
	}()
	<-entered

	text, isErr := m.call("jatai_login", nil)

	close(release)
	assertContains(t, text, "Ainda aguardando a aprovação de WDJB-MJHT", signupHint)
	if isErr {
		t.Fatalf("%q", text)
	}
	assertContains(t, <-first, "Logado como Ana")
}

func TestLoginAfterTheCodeExpiredStartsANewOneAtOnce(t *testing.T) {
	m := newMCPTest(t)
	var mu sync.Mutex
	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	m.deps.Now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	m.json("POST /api/v1/cli/device_authorizations", 201, deviceStart()) // expires_in 600
	m.json("POST /api/v1/cli/device_authorizations/token", 400, map[string]any{"error": "authorization_pending"})
	m.call("jatai_login", nil)
	mu.Lock()
	now = now.Add(601 * time.Second)
	mu.Unlock()

	text, isErr := m.call("jatai_login", nil)

	assertContains(t, text, "Peça ao usuário para abrir")
	if isErr || m.calls("POST /api/v1/cli/device_authorizations") != 2 || m.calls("POST /api/v1/cli/device_authorizations/token") != 0 {
		t.Fatalf("isErr = %v, starts = %d, polls = %d", isErr, m.calls("POST /api/v1/cli/device_authorizations"),
			m.calls("POST /api/v1/cli/device_authorizations/token"))
	}
}

func TestAccountFolderLineCoversAppMalformedFileAndHome(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	m.json("GET /api/v1/account", 200, accountBody(map[string]any{"available": true, "app": nil}, false))
	m.json("GET /api/v1/apps", 200, []map[string]any{})
	withApp, broken := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(withApp, ".jatai.yml"), []byte("app: loja\nstack: node\n"), 0o644)
	os.WriteFile(filepath.Join(broken, ".jatai.yml"), []byte("app: [loja\n"), 0o644)

	text, _ := m.call("jatai_conta", map[string]any{"pasta": withApp})
	assertContains(t, text, "app loja (.jatai.yml)", "jatai_publicar")
	text, _ = m.call("jatai_conta", map[string]any{"pasta": broken})
	assertContains(t, text, "não consegui ler o .jatai.yml")
	assertMissing(t, text, "ainda não tem app")
	text, _ = m.call("jatai_conta", map[string]any{"pasta": m.deps.Paths.Home})
	assertContains(t, text, "Informe `pasta`")
}

func TestAccountNotFoundDoesNotSendTheAgentInCircles(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	m.json("GET /api/v1/account", 404, map[string]any{"error": "not_found"})

	text, isErr := m.call("jatai_conta", nil)

	assertContains(t, text, "O Jataí não reconheceu esta operação", "npm update -g jatai-cli")
	assertMissing(t, text, "App não encontrado", "Use jatai_conta")
	if !isErr {
		t.Fatal("expected an error")
	}
}

func TestPackageExplainNamesTheFailingHost(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	c, _ := api.New(base, "", "teste")
	c.RetryDelay = 0
	err := c.Get(context.Background(), "/api/v1/apps", nil, nil)

	got := explain(err)

	if got != "Sem conexão com a API do Jataí ("+base+"). Tente de novo em instantes." {
		t.Fatalf("%q (err %v)", got, err)
	}
}

func TestLoginWithoutAHomeDirectoryDoesNotWriteTheTokenIntoTheProject(t *testing.T) {
	m := newMCPTest(t)
	m.deps.Paths = config.Paths{}
	t.Chdir(m.dir)
	m.json("POST /api/v1/cli/device_authorizations", 201, deviceStart())
	m.call("jatai_login", nil)
	m.json("POST /api/v1/cli/device_authorizations/token", 200,
		map[string]any{"token": "jat_secret", "user": map[string]any{"name": "Ana", "email": "ana@x.com"}})

	text, isErr := m.call("jatai_login", nil)

	assertContains(t, text, "Login aprovado, mas a credencial não foi gravada.", "HOME")
	assertMissing(t, text, "jat_secret")
	if _, err := os.Stat(filepath.Join(m.dir, ".jatai")); !isErr || err == nil {
		t.Fatalf("isErr = %v, .jatai created in the working directory: %v", isErr, err)
	}
}
