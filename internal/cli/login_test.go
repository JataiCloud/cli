package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jataicloud/cli/internal/config"
)

func deviceStart() map[string]any {
	return map[string]any{
		"device_code": "dev-123", "user_code": "WDJB-MJHT",
		"verification_uri":          "https://jataicloud.com.br/cli/autorizar",
		"verification_uri_complete": "https://jataicloud.com.br/cli/autorizar?codigo=WDJB-MJHT",
		"expires_in":                600, "interval": 5,
	}
}

func TestLoginSavesTheTokenAfterApproval(t *testing.T) {
	e := newTestEnv(t)
	e.json("POST /api/v1/cli/device_authorizations", http.StatusCreated, deviceStart())
	polls := 0
	e.handle("POST /api/v1/cli/device_authorizations/token", func(w http.ResponseWriter, _ *http.Request) {
		polls++
		if polls == 1 {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"authorization_pending"}`))
			return
		}
		w.Write([]byte(`{"token":"jat_novo","user":{"name":"Bruno","email":"bruno@exemplo.com"}}`))
	})

	code := e.run("login")

	if code != 0 || config.Token(e.app.Paths, func(string) string { return "" }) != "jat_novo" {
		t.Fatalf("code = %d, err = %q", code, e.err)
	}
	for _, want := range []string{"WDJB-MJHT", "jataicloud.com.br/cli/autorizar", "bruno@exemplo.com"} {
		if !strings.Contains(e.out.String(), want) {
			t.Errorf("missing %q in %q", want, e.out)
		}
	}
}

func TestLoginOpensTheBrowserUnlessAskedNotTo(t *testing.T) {
	e := newTestEnv(t)
	e.app.CanOpenBrowser = func() bool { return true }
	var opened []string
	e.app.OpenBrowser = func(url string) error { opened = append(opened, url); return nil }
	e.json("POST /api/v1/cli/device_authorizations", http.StatusCreated, deviceStart())
	e.json("POST /api/v1/cli/device_authorizations/token", http.StatusOK, map[string]any{"token": "jat_x"})

	e.run("login")
	e.run("login", "--no-browser")

	if len(opened) != 1 || !strings.HasSuffix(opened[0], "?codigo=WDJB-MJHT") {
		t.Fatalf("opened = %v", opened)
	}
}

func TestLoginReportsADenial(t *testing.T) {
	e := newTestEnv(t)
	e.json("POST /api/v1/cli/device_authorizations", http.StatusCreated, deviceStart())
	e.json("POST /api/v1/cli/device_authorizations/token", http.StatusBadRequest, map[string]any{"error": "access_denied"})

	if code := e.run("login"); code != 1 || !strings.Contains(e.err.String(), "cancelado") {
		t.Fatalf("code = %d, err = %q", code, e.err)
	}
}

func TestLogoutRevokesAndForgetsTheToken(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	var auth string
	e.handle("DELETE /api/v1/cli/token", func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	})

	code := e.run("logout")

	if code != 0 || auth != "Bearer jat_teste" || config.Token(e.app.Paths, func(string) string { return "" }) != "" {
		t.Fatalf("code = %d, auth = %q, err = %q", code, auth, e.err)
	}
}

func TestLogoutWithoutLogin(t *testing.T) {
	e := newTestEnv(t)
	if code := e.run("logout"); code != 0 || !strings.Contains(e.out.String(), "não está logado") {
		t.Fatalf("code = %d, out = %q", code, e.out)
	}
}
