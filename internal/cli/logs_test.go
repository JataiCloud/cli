package cli

import (
	"net/http"
	"strings"
	"testing"
)

func TestLogsPassesTailAndPrintsEntries(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.writeProject("app: loja\n")
	var tail string
	e.handle("GET /api/v1/apps/loja/logs", func(w http.ResponseWriter, r *http.Request) {
		tail = r.URL.Query().Get("tail")
		w.Write([]byte(`[{"timestamp":"10:30:00","source":"web.1","level":"info","message":"GET / 200"}]`))
	})

	e.run("logs", "--tail", "5")

	if tail != "5" || e.out.String() != "10:30:00 web.1 [info] GET / 200\n" {
		t.Fatalf("tail = %q, out = %q", tail, e.out)
	}
}

func TestLogsSearchRequiresAQuery(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.writeProject("app: loja\n")
	if code := e.run("logs:search"); code != 2 {
		t.Fatalf("code = %d", code)
	}
}

func TestLogsSearchSendsFilters(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.writeProject("app: loja\n")
	var query string
	e.handle("GET /api/v1/apps/loja/logs/search", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Write([]byte(`[]`))
	})

	e.run("logs:search", "--query", "timeout", "--level", "error", "--since", "1h")

	for _, want := range []string{"q=timeout", "level=error", "since=1h", "limit=100"} {
		if !strings.Contains(query, want) {
			t.Errorf("missing %q in %q", want, query)
		}
	}
	if !strings.Contains(e.out.String(), "Nenhum resultado encontrado.") {
		t.Fatalf("out = %q", e.out)
	}
}

func TestLogsExportPrintsCSVAsIsAndIndentsJSON(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.writeProject("app: loja\n")
	e.handle("GET /api/v1/apps/loja/logs/export", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("export_format") == "csv" {
			w.Header().Set("Content-Type", "text/csv")
			w.Write([]byte("timestamp,message\n1,oi\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"message":"oi"}]`))
	})

	e.run("logs:export", "--format", "csv")
	if e.out.String() != "timestamp,message\n1,oi\n" {
		t.Fatalf("csv out = %q", e.out)
	}
	e.out.Reset()
	e.run("logs:export")
	if !strings.Contains(e.out.String(), "\n  {\n    \"message\": \"oi\"\n  }\n") {
		t.Fatalf("json out = %q", e.out)
	}
}
