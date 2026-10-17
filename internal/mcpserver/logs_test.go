package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}

// logsServer responde GET …/logs com as entradas dadas e guarda a consulta.
func logsServer(m *mcpTest, entries []map[string]any) *url.Values {
	var mu sync.Mutex
	got := url.Values{}
	m.handle("GET /api/v1/apps/loja/logs", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.URL.Query()
		mu.Unlock()
		writeJSON(w, entries)
	})
	return &got
}

func logLine(ts, level, msg string) map[string]any {
	return map[string]any{"timestamp": ts, "source": "app", "level": level, "message": msg}
}

// clock é a hora que o jatai_logs mostra para o timestamp: a local.
func clock(t *testing.T, ts string) string {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Local().Format("15:04:05")
}

func TestLogsTailDefaultsToAHundredLines(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	const ts = "2026-10-16T14:03:07-03:00"
	query := logsServer(m, []map[string]any{logLine(ts, "info", "GET / 200")})

	text, isErr := m.call("jatai_logs", map[string]any{"pasta": m.dir})

	if isErr || text != clock(t, ts)+" [info] GET / 200" {
		t.Fatalf("%v %q", isErr, text)
	}
	if query.Get("tail") != "100" {
		t.Fatalf("query = %v", *query)
	}
}

func TestLogsFilterIsAppliedOverTheTail(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	const ts = "2026-10-16T14:03:07-03:00"
	query := logsServer(m, []map[string]any{
		logLine(ts, "info", "GET / 200"),
		logLine(ts, "error", "ERRO ao ler o banco"),
		logLine(ts, "info", "GET /sobre 200"),
		logLine(ts, "warn", "erro de timeout"),
	})

	text, isErr := m.call("jatai_logs", map[string]any{"pasta": m.dir, "filtro": "erro"})

	want := clock(t, ts) + " [error] ERRO ao ler o banco\n" + clock(t, ts) + " [warn] erro de timeout"
	if isErr || text != want {
		t.Fatalf("%v %q", isErr, text)
	}
	if query.Get("tail") != "500" {
		t.Fatalf("query = %v", *query)
	}
	if m.calls("GET /api/v1/apps/loja/logs/search") != 0 {
		t.Fatal("called logs/search, which answers 402 on the basic Logs Extension")
	}
}

func TestLogsFilterReturnsTheMostRecentMatchesUpToLinhas(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	var entries []map[string]any
	for i := 1; i <= 8; i++ {
		entries = append(entries, logLine("2026-10-16T14:03:07Z", "error", fmt.Sprintf("falha %d", i)),
			logLine("2026-10-16T14:03:07Z", "info", fmt.Sprintf("ok %d", i)))
	}
	logsServer(m, entries)

	text, _ := m.call("jatai_logs", map[string]any{"pasta": m.dir, "filtro": "falha", "linhas": 3})

	lines := strings.Split(text, "\n")
	if len(lines) != 3 || !strings.HasSuffix(lines[0], "falha 6") || !strings.HasSuffix(lines[2], "falha 8") {
		t.Fatalf("%q", text)
	}
}

func TestLogsFilterWithoutMatchesSaysSo(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	logsServer(m, []map[string]any{logLine("2026-10-16T14:03:07Z", "info", "GET / 200")})

	text, isErr := m.call("jatai_logs", map[string]any{"pasta": m.dir, "filtro": "timeout"})

	if isErr || text != `Nenhuma linha com "timeout" nas últimas 500 linhas de loja.` {
		t.Fatalf("%v %q", isErr, text)
	}
}

func TestLogsWithoutEntriesSaysThereAreNoneYet(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	logsServer(m, nil)

	text, isErr := m.call("jatai_logs", map[string]any{"pasta": m.dir})
	filtered, _ := m.call("jatai_logs", map[string]any{"pasta": m.dir, "filtro": "erro"})

	if isErr || text != "Sem linhas de log para loja ainda." || filtered != text {
		t.Fatalf("%v %q / %q", isErr, text, filtered)
	}
}

func TestLogsLinhasIsClampedBetweenOneAndFiveHundred(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	query := logsServer(m, nil)
	for linhas, want := range map[int]string{250: "250", 9000: "500", -4: "100", 0: "100"} {
		m.call("jatai_logs", map[string]any{"pasta": m.dir, "linhas": linhas})
		if query.Get("tail") != want {
			t.Fatalf("linhas %d: tail = %q, want %s", linhas, query.Get("tail"), want)
		}
	}
}

func TestLogsCapsTheLinesToLinhasWithoutFilter(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	var entries []map[string]any
	for i := 1; i <= 4; i++ {
		entries = append(entries, logLine("2026-10-16T14:03:07Z", "info", fmt.Sprintf("linha %d", i)))
	}
	logsServer(m, entries)

	text, _ := m.call("jatai_logs", map[string]any{"pasta": m.dir, "linhas": 2})

	lines := strings.Split(text, "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], "linha 3") || !strings.HasSuffix(lines[1], "linha 4") {
		t.Fatalf("%q", text)
	}
}

func TestLogsShowsAnUnparseableTimestampRawAndMissingFieldsGracefully(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	logsServer(m, []map[string]any{
		logLine("10:30:00", "info", "ja vinha como hora"),
		logLine("ontem", "", "sem nivel\n"),
	})

	text, _ := m.call("jatai_logs", map[string]any{"pasta": m.dir})

	if text != "10:30:00 [info] ja vinha como hora\nontem [-] sem nivel" {
		t.Fatalf("%q", text)
	}
}

func TestLogsTruncatesAHugeMessage(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	logsServer(m, []map[string]any{logLine("x", "error", strings.Repeat("a", 5000))})

	text, _ := m.call("jatai_logs", map[string]any{"pasta": m.dir})

	if n := len([]rune(text)); n > 1100 || !strings.HasSuffix(text, "…") {
		t.Fatalf("%d runes", n)
	}
}

func TestLogsUsesTheExplicitAppAndExplainsErrors(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	m.json("GET /api/v1/apps/loja/logs", 402, map[string]any{"error": "Extensão de Logs não contratada"})

	text, isErr := m.call("jatai_logs", map[string]any{"pasta": m.dir, "app": "loja"})

	assertContains(t, text, "Extensão de Logs não contratada.", "https://jataicloud.com.br/billing")
	if !isErr {
		t.Fatal("not an error")
	}
	missing, isErr := m.call("jatai_logs", map[string]any{"pasta": m.dir})
	if !isErr || missing != errNoApp.Error() {
		t.Fatalf("%v %q", isErr, missing)
	}
}
