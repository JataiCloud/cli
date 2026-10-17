package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jataicloud/cli/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpTest sobe o servidor MCP contra uma API falsa e conversa com ele por um
// cliente MCP de verdade, em memória.
type mcpTest struct {
	t          *testing.T
	deps       Deps
	dir        string
	clientOpts *mcp.ClientOptions
	protocol   string   // vazio = a versão mais nova do protocolo
	roots      []string // URIs das raízes que o cliente expõe
	setup      func(st *state, srv *mcp.Server)

	mu       sync.Mutex
	handlers map[string]http.HandlerFunc
	counts   map[string]int
	vars     map[string]string
	opened   []string
	mux      *http.ServeMux

	session *mcp.ClientSession
	st      *state
}

func newMCPTest(t *testing.T) *mcpTest {
	t.Helper()
	m := &mcpTest{
		t: t, dir: t.TempDir(), mux: http.NewServeMux(),
		handlers: map[string]http.HandlerFunc{}, counts: map[string]int{},
	}
	srv := httptest.NewServer(m.mux)
	t.Cleanup(srv.Close)
	m.vars = map[string]string{"JATAI_API_URL": srv.URL}
	m.deps = Deps{
		Version:   "2.0.0",
		UserAgent: "jatai-cli/2.0.0 (test)",
		Paths:     config.Paths{Home: t.TempDir()},
		Getenv: func(k string) string {
			m.mu.Lock()
			defer m.mu.Unlock()
			return m.vars[k]
		},
		Getwd:    func() (string, error) { return m.dir, nil },
		HTTP:     srv.Client(),
		Hostname: func() (string, error) { return "maquina-teste", nil },
		OpenBrowser: func(u string) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.opened = append(m.opened, u)
			return nil
		},
		CanOpenBrowser:  func() bool { return false },
		Now:             time.Now,
		Sleep:           shortSleep,
		PollInterval:    5 * time.Millisecond,
		ReconnectDelays: []time.Duration{time.Millisecond},
		WelcomeTimeout:  time.Second,
		Budget:          200 * time.Millisecond,
		UpdateHint:      "npm update -g jatai",
		Log:             io.Discard,
		Git:             func(string, ...string) (string, error) { return "", errors.New("sem git") },
	}
	return m
}

// shortSleep espera de verdade, mas no máximo 5 ms: sem espera nenhuma, o
// login ficaria consultando o /token sem parar.
func shortSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(min(d, 5*time.Millisecond))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (m *mcpTest) connect() *mcp.ClientSession {
	m.t.Helper()
	if m.session != nil {
		return m.session
	}
	st := newState(m.deps)
	m.mu.Lock()
	m.st = st
	m.mu.Unlock()
	srv := st.server()
	if m.setup != nil {
		m.setup(st, srv)
	}
	clientT, serverT := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	ss, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		m.t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "teste", Version: "1"}, m.clientOpts)
	for _, uri := range m.roots {
		// Raízes estão em desuso no protocolo novo, mas os agentes ainda as mandam.
		client.AddRoots(&mcp.Root{URI: uri}) //nolint:staticcheck // ver acima
	}
	cs, err := client.Connect(ctx, clientT, &mcp.ClientSessionOptions{ProtocolVersion: m.protocol})
	if err != nil {
		m.t.Fatal(err)
	}
	m.t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Wait()
		st.shutdown()
		cancel()
	})
	m.session = cs
	return cs
}

func (m *mcpTest) handle(pattern string, h http.HandlerFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.handlers[pattern]; !ok {
		m.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			m.mu.Lock()
			m.counts[pattern]++
			current := m.handlers[pattern]
			m.mu.Unlock()
			current(w, r)
		})
	}
	m.handlers[pattern] = h
}

func (m *mcpTest) json(pattern string, status int, body any) {
	m.handle(pattern, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(body)
	})
}

func (m *mcpTest) calls(pattern string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[pattern]
}

func (m *mcpTest) login() {
	if err := config.SaveToken(m.deps.Paths, "jat_teste"); err != nil {
		m.t.Fatal(err)
	}
}

func (m *mcpTest) savedToken() string {
	return config.Token(m.deps.Paths, func(string) string { return "" })
}

func (m *mcpTest) call(name string, args map[string]any) (text string, isErr bool) {
	m.t.Helper()
	params := &mcp.CallToolParams{Name: name}
	if args != nil {
		params.Arguments = args
	}
	res, err := m.connect().CallTool(context.Background(), params)
	if err != nil {
		m.t.Fatalf("%s: %v", name, err)
	}
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n"), res.IsError
}

func (m *mcpTest) tools() []*mcp.Tool {
	m.t.Helper()
	res, err := m.connect().ListTools(context.Background(), nil)
	if err != nil {
		m.t.Fatal(err)
	}
	return res.Tools
}

func (m *mcpTest) toolNames() []string {
	var names []string
	for _, tool := range m.tools() {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

func deviceStart() map[string]any {
	return map[string]any{
		"device_code": "dev_secret_123", "user_code": "WDJB-MJHT",
		"verification_uri":          "https://jataicloud.com.br/cli/autorizar",
		"verification_uri_complete": "https://jataicloud.com.br/cli/autorizar?codigo=WDJB-MJHT",
		"expires_in":                600, "interval": 5,
	}
}

func assertContains(t *testing.T, text string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Fatalf("missing %q in:\n%s", w, text)
		}
	}
}

func assertMissing(t *testing.T, text string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(text, u) {
			t.Fatalf("unexpected %q in:\n%s", u, text)
		}
	}
}

func TestListsExactlyTheCourseTools(t *testing.T) {
	m := newMCPTest(t)
	got := m.toolNames()
	want := []string{"jatai_conta", "jatai_criar_app", "jatai_login", "jatai_logs", "jatai_publicar",
		"jatai_status", "jatai_variaveis_definir", "jatai_variaveis_listar", "jatai_variaveis_remover"}
	if !slices.Equal(got, want) {
		t.Fatalf("tools = %v", got)
	}
}

func TestEveryToolDescribesItselfAndAcceptsPastaAndApp(t *testing.T) {
	for _, tool := range newMCPTest(t).tools() {
		if len(tool.Description) < 40 {
			t.Errorf("%s: description too short: %q", tool.Name, tool.Description)
		}
		schema, _ := tool.InputSchema.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		for _, field := range []string{"pasta", "app"} {
			prop, ok := props[field].(map[string]any)
			if !ok || prop["description"] == "" || prop["description"] == nil {
				t.Errorf("%s: field %q missing or undescribed: %v", tool.Name, field, props[field])
			}
		}
	}
}

func TestEnvToolSchemasSayValuesAreStrings(t *testing.T) {
	want := map[string][]string{
		"jatai_variaveis_definir": {"variaveis", "string", `"3000"`, "^[A-Z][A-Z0-9_]*$", "100"},
		"jatai_variaveis_remover": {"chaves", "string", "^[A-Z][A-Z0-9_]*$"},
	}
	for _, tool := range newMCPTest(t).tools() {
		if _, ok := want[tool.Name]; !ok {
			continue
		}
		schema, _ := tool.InputSchema.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		field := want[tool.Name][0]
		prop, _ := props[field].(map[string]any)
		desc, _ := prop["description"].(string)
		assertContains(t, desc, want[tool.Name][1:]...)
	}
}

func TestLogsDescriptionMentionsTheLineCut(t *testing.T) {
	for _, tool := range newMCPTest(t).tools() {
		if tool.Name == "jatai_logs" {
			assertContains(t, tool.Description, "1000 caracteres")
		}
	}
}

func TestOnlyVariablesAndKeysAreRequired(t *testing.T) {
	want := map[string][]string{"jatai_variaveis_definir": {"variaveis"}, "jatai_variaveis_remover": {"chaves"}}
	for _, tool := range newMCPTest(t).tools() {
		schema, _ := tool.InputSchema.(map[string]any)
		var got []string
		if list, ok := schema["required"].([]any); ok {
			for _, item := range list {
				got = append(got, item.(string))
			}
		}
		if !slices.Equal(got, want[tool.Name]) {
			t.Errorf("%s: required = %v, want %v", tool.Name, got, want[tool.Name])
		}
	}
}

func TestServerInstructionsGuideTheFlow(t *testing.T) {
	m := newMCPTest(t)
	got := m.connect().InitializeResult().Instructions
	assertContains(t, got, "jatai_conta → (se preciso) jatai_login → jatai_criar_app → jatai_publicar",
		"`cacheComponents`", "await connection()", "https://jataicloud.com.br/apps")
	if got != Instructions {
		t.Fatalf("instructions = %q", got)
	}
}

func TestAToolThatPanicsAnswersWithAnErrorInsteadOfCrashing(t *testing.T) {
	m := newMCPTest(t)
	m.setup = func(st *state, srv *mcp.Server) {
		addTool(srv, st, "jatai_boom", "Ferramenta de teste que entra em pânico.",
			func(context.Context, *mcp.CallToolRequest, struct{}) *mcp.CallToolResult { panic("kaboom") })
	}

	text, isErr := m.call("jatai_boom", nil)

	if !isErr || !strings.Contains(text, "Erro interno") {
		t.Fatalf("%v %q", isErr, text)
	}
	if again, _ := m.call("jatai_conta", nil); !strings.Contains(again, "jatai_login") {
		t.Fatalf("server stopped answering: %q", again)
	}
}

func TestServeEndsWhenTheContextIsCancelled(t *testing.T) {
	m := newMCPTest(t)
	clientT, serverT := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, m.deps, serverT) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).Connect(context.Background(), clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}
