package cli

import (
	"net/http"
	"strings"
	"testing"
)

func TestCommandsNeedALogin(t *testing.T) {
	e := newTestEnv(t)
	e.writeProject("app: loja\n")
	if code := e.run("status"); code != 1 || !strings.Contains(e.err.String(), "jatai login") {
		t.Fatalf("code = %d, err = %q", code, e.err)
	}
}

func TestStatusShowsTheAppAndTheLastDeploy(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.writeProject("app: loja\n")
	e.json("GET /api/v1/apps/loja", http.StatusOK, map[string]any{
		"name": "Loja", "slug": "loja", "stack": "rails", "status": "active", "region": "br-se1",
		"preset": "escala_1", "database_plan": "base", "url": "https://loja.jataiapp.com.br",
		"last_deploy": map[string]any{"id": 42, "status": "live", "commit_sha": "abc123", "created_at": "2026-09-30T12:00:00Z"},
	})

	code := e.run("status")

	for _, want := range []string{"App: Loja", "Hex:     Hex Escala 1", "Banco:   PG Base", "URL:     https://loja.jataiapp.com.br", "Commit:  abc123"} {
		if !strings.Contains(e.out.String(), want) {
			t.Errorf("missing %q in %q", want, e.out)
		}
	}
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
}
