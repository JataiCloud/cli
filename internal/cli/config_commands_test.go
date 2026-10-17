package cli

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestEnvListPrintsSortedPairs(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.writeProject("app: loja\n")
	e.json("GET /api/v1/apps/loja/env", http.StatusOK, map[string]string{"ZETA": "1", "ALFA": "2"})

	e.run("env", "list")

	if e.out.String() != "ALFA=2\nZETA=1\n" {
		t.Fatalf("out = %q", e.out)
	}
}

func TestEnvSetSendsPairsAndRejectsBadFormat(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.writeProject("app: loja\n")
	var sent map[string]map[string]string
	e.handle("PUT /api/v1/apps/loja/env", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&sent)
		w.Write([]byte(`{}`))
	})

	if code := e.run("env", "set", "A=1", "URL=postgres://x?a=b"); code != 0 || sent["env"]["URL"] != "postgres://x?a=b" {
		t.Fatalf("code = %d, sent = %v", code, sent)
	}
	if code := e.run("env", "set", "SEMIGUAL"); code != 2 {
		t.Fatalf("bad format code = %d", code)
	}
}

func TestEnvUnsetJoinsTheKeys(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.writeProject("app: loja\n")
	var path string
	e.handle("DELETE /api/v1/apps/loja/env/{keys}", func(w http.ResponseWriter, r *http.Request) {
		path = r.PathValue("keys")
		w.Write([]byte(`{}`))
	})

	e.run("env", "unset", "A", "B")

	if path != "A,B" {
		t.Fatalf("keys = %q", path)
	}
}
