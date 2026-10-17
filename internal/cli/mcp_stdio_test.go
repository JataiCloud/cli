package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jataicloud/cli/internal/config"
	"github.com/jataicloud/cli/internal/ui"
)

// TestMain deixa o binário de teste fazer o papel do `jatai` de verdade:
// com JATAI_TEST_MAIN=1 ele roda a CLI com os argumentos recebidos.
func TestMain(m *testing.M) {
	if os.Getenv("JATAI_TEST_MAIN") == "1" {
		// Os mesmos sinais do cmd/jatai/main.go.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := Main(ctx, "2.0.0", os.Args[1:])
		stop()
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func fakeAccountAPI(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/account", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"user": map[string]any{"name": "Ana", "email": "ana@x.com"}, "team": map[string]any{"name": "Time", "slug": "time"},
			"apps": map[string]any{"count": 0, "limit": 50}, "courtesy": map[string]any{"available": true, "app": nil},
			"payment_method": false,
		})
	})
	mux.HandleFunc("GET /api/v1/apps", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("[]")) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// runJSONRPC manda as mensagens, uma por linha, mantém o stdin aberto até
// chegar a resposta de id 2, fecha o stdin e devolve todas as linhas do stdout.
func runJSONRPC(t *testing.T, cmd *exec.Cmd, messages ...string) []string {
	t.Helper()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for _, msg := range messages {
		io.WriteString(stdin, msg+"\n")
	}
	lines := make(chan string)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	var out []string
	timeout := time.After(15 * time.Second)
	for open := true; open; {
		select {
		case line, ok := <-lines:
			if !ok {
				open = false
				break
			}
			out = append(out, line)
			if strings.Contains(line, `"id":2`) {
				stdin.Close()
			}
		case <-timeout:
			cmd.Process.Kill()
			t.Fatalf("no reply in time; stdout = %q, stderr = %q", out, stderr.String())
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("jatai mcp: %v; stderr = %q", err, stderr.String())
	}
	return out
}

func TestMCPStdoutCarriesOnlyProtocol(t *testing.T) {
	api := fakeAccountAPI(t)
	cmd := exec.Command(os.Args[0], "mcp")
	cmd.Env = append(os.Environ(), "JATAI_TEST_MAIN=1", "JATAI_API_URL="+api.URL, "JATAI_TOKEN=jat_x",
		"HOME="+t.TempDir(), "JATAI_DEBUG=1")

	out := runJSONRPC(t, cmd,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"jatai_conta","arguments":{}}}`)

	for _, line := range out {
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil || msg["jsonrpc"] != "2.0" {
			t.Fatalf("stdout: %q", line)
		}
	}
	joined := strings.Join(out, "\n")
	assertNoSecret := !strings.Contains(joined, "jat_x")
	if !strings.Contains(joined, "Logado como") || !assertNoSecret {
		t.Fatalf("stdout = %q", joined)
	}
}

func TestMCPExitsCleanlyOnSIGTERM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sem SIGTERM no Windows")
	}
	cmd := exec.Command(os.Args[0], "mcp")
	cmd.Env = append(os.Environ(), "JATAI_TEST_MAIN=1", "HOME="+t.TempDir())
	stdin, _ := cmd.StdinPipe() // fica aberto: só o sinal encerra
	defer stdin.Close()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	io.WriteString(stdin, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",`+
		`"capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`+"\n")
	if !bufio.NewScanner(stdout).Scan() { // servidor de pé: respondeu ao initialize
		t.Fatal("no initialize reply")
	}

	cmd.Process.Signal(syscall.SIGTERM)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("exit: %v", err)
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("jatai mcp did not exit after SIGTERM")
	}
}

func TestMCPCommandSkipsTheUpdateNotice(t *testing.T) {
	e := newTestEnv(t)
	delete(e.vars, "JATAI_NO_UPDATE_CHECK")
	config.SaveState(e.app.Paths, config.State{LastUpdateCheck: time.Now().Add(-48 * time.Hour)})
	calls := 0
	e.app.LatestVersion = func(context.Context) (string, error) { calls++; return "9.9.9", nil }
	var stdout bytes.Buffer
	e.app.Stdin, e.app.Stdout = strings.NewReader(""), &stdout

	// O gancho do root, chamado direto com o terminal interativo: é ele que
	// pula o aviso (o `jatai mcp` também desliga o modo interativo).
	root := newRootCmd(e.app)
	mcpCmd, _, err := root.Find([]string{"mcp"})
	if err != nil || mcpCmd.Name() != "mcp" {
		t.Fatalf("mcp command not registered: %v", err)
	}
	mcpCmd.SetContext(context.Background())
	root.PersistentPostRun(mcpCmd, nil)

	code := e.run("mcp")

	if code != 0 || calls != 0 {
		t.Fatalf("code = %d, LatestVersion calls = %d, err = %q", code, calls, e.err)
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if line != "" && !json.Valid([]byte(line)) {
			t.Fatalf("non-JSON on stdout: %q", line)
		}
	}
	if e.out.Len() != 0 {
		t.Fatalf("ui stdout = %q", e.out)
	}
}

func TestMCPCommandSendsTheUIToStderr(t *testing.T) {
	e := newTestEnv(t)
	e.app.UI.Color = true
	var stdout bytes.Buffer
	e.app.Stdin, e.app.Stdout = strings.NewReader(""), &stdout

	if code := e.run("mcp"); code != 0 {
		t.Fatalf("code = %d, err = %q", code, e.err)
	}
	if _, ok := e.app.UI.Prompter.(ui.NonInteractivePrompter); !ok {
		t.Fatalf("prompter = %T", e.app.UI.Prompter)
	}
	if e.app.UI.Out != e.app.UI.Err || e.app.UI.Interactive || e.app.UI.Color {
		t.Fatalf("ui = %+v", e.app.UI)
	}
}
