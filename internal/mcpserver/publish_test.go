package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jataicloud/cli/internal/cable/cabletest"
	"github.com/jataicloud/cli/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const lojaURL = "https://loja.jataiapp.com.br"

// lojaProject é uma pasta com .jatai.yml do app loja, que já existe e está
// ativo; o envio do código cria o deploy 7.
func lojaProject(t *testing.T, m *mcpTest) {
	t.Helper()
	m.login()
	writeFile(t, m.dir, "package.json", `{"name":"loja"}`)
	writeFile(t, m.dir, "server.js", "console.log('oi')")
	writeFile(t, m.dir, config.ProjectFile, "app: loja\nstack: node\n")
	m.json("GET /api/v1/apps/loja", 200, map[string]any{"id": 1, "slug": "loja", "status": "active", "url": lojaURL})
	m.json("POST /api/v1/apps/loja/deploys", 201, map[string]any{"id": 7, "status": "queued"})
}

// deployStatuses responde o GET do deploy 7 com os status dados, repetindo o
// último.
func deployStatuses(m *mcpTest, statuses ...string) {
	var mu sync.Mutex
	m.handle("GET /api/v1/apps/loja/deploys/7", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		status := statuses[0]
		if len(statuses) > 1 {
			statuses = statuses[1:]
		}
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"id": 7, "status": status})
	})
}

// gatedDeploy fica em building até live virar true.
func gatedDeploy(m *mcpTest) *atomic.Bool {
	var live atomic.Bool
	m.handle("GET /api/v1/apps/loja/deploys/7", func(w http.ResponseWriter, _ *http.Request) {
		status := "building"
		if live.Load() {
			status = "live"
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 7, "status": status})
	})
	return &live
}

func TestPublishReturnsTheURLWhenTheDeployFinishesInTime(t *testing.T) {
	m := newMCPTest(t)
	m.deps.Budget = 5 * time.Second
	lojaProject(t, m)
	deployStatuses(m, "building", "live")

	text, isErr := m.call("jatai_publicar", map[string]any{"pasta": m.dir})

	if isErr || !strings.HasPrefix(text, "Publicado: "+lojaURL+"\nApp loja · deploy #7 · ") ||
		!strings.HasSuffix(text, "s") {
		t.Fatalf("%v %q", isErr, text)
	}
}

func TestPublishAnswersInProgressAndKeepsDeploying(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	live := gatedDeploy(m)

	text, isErr := m.call("jatai_publicar", map[string]any{"pasta": m.dir})

	if isErr || text != "Deploy #7 de loja em andamento (fase: construindo). Chame jatai_status com aguardar=true "+
		"para acompanhar." {
		t.Fatalf("%v %q", isErr, text)
	}
	live.Store(true)
	status := waitFor(t, func() (string, bool) {
		text, _ := m.call("jatai_status", map[string]any{"pasta": m.dir, "aguardar": true})
		return text, strings.HasPrefix(text, "Publicado: ")
	})
	assertContains(t, status, "Publicado: "+lojaURL, "deploy #7")
	if m.calls("POST /api/v1/apps/loja/deploys") != 1 {
		t.Fatalf("deploys = %d", m.calls("POST /api/v1/apps/loja/deploys"))
	}
}

// waitFor repete fn (cada chamada espera até o Budget) até ela dar certo.
func waitFor(t *testing.T, fn func() (string, bool)) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		text, ok := fn()
		if ok {
			return text
		}
		if time.Now().After(deadline) {
			t.Fatalf("gave up; last answer: %q", text)
		}
	}
}

func TestPublishFailureIncludesTheBuildLogTail(t *testing.T) {
	m := newMCPTest(t)
	m.deps.Budget = 5 * time.Second
	lojaProject(t, m)
	deployStatuses(m, "failed")
	var tail string
	m.handle("GET /api/v1/apps/loja/deploys/7/log", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		tail = r.URL.Query().Get("tail")
		m.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"log": "...\nType error: x\n"})
	})

	text, isErr := m.call("jatai_publicar", map[string]any{"pasta": m.dir})

	want := "O deploy #7 de loja falhou.\nÚltimas linhas do build:\n...\nType error: x\n" +
		"Corrija o projeto e chame jatai_publicar de novo."
	if !isErr || text != want {
		t.Fatalf("%v %q", isErr, text)
	}
	if tail != "60" {
		t.Fatalf("tail = %q", tail)
	}
}

func TestPublishFailureSaysWhenTheBuildLogIsUnavailable(t *testing.T) {
	m := newMCPTest(t)
	m.deps.Budget = 5 * time.Second
	lojaProject(t, m)
	deployStatuses(m, "failed")
	m.json("GET /api/v1/apps/loja/deploys/7/log", 500, map[string]any{"error": "boom"})

	text, _ := m.call("jatai_publicar", map[string]any{"pasta": m.dir})

	assertContains(t, text, "O deploy #7 de loja falhou.", logUnavailable, "jatai_publicar de novo")
}

func TestPublishOverWebSocketFallsBackToTheStreamedLinesWhenTheLogFails(t *testing.T) {
	m := newMCPTest(t)
	m.deps.Budget = 5 * time.Second
	lojaProject(t, m)
	cableSrv := &cabletest.Handler{Messages: map[string][]map[string]any{
		"DeployLogChannel":    {{"line": "> next build"}, {"line": "  Type error: y"}},
		"DeployStatusChannel": {{"status": "failed"}},
	}}
	m.mux.Handle("/cable", cableSrv)
	m.json("GET /api/v1/apps/loja/deploys/7/log", 503, map[string]any{"error": "indisponível"})

	text, isErr := m.call("jatai_publicar", map[string]any{"pasta": m.dir})

	if !isErr || !strings.Contains(text, "Últimas linhas do build:\n> next build\n  Type error: y\nCorrija") {
		t.Fatalf("%v %q", isErr, text)
	}
	if got := cableSrv.Headers().Get("Authorization"); got != "Bearer jat_teste" {
		t.Fatalf("cable authorization = %q", got)
	}
	if m.calls("GET /api/v1/apps/loja/deploys/7") != 0 {
		t.Fatal("fell back to polling with the WebSocket up")
	}
}

func TestPublishPartiallyLiveListsTheProcessFailures(t *testing.T) {
	m := newMCPTest(t)
	m.deps.Budget = 5 * time.Second
	lojaProject(t, m)
	var polls atomic.Int32
	m.handle("GET /api/v1/apps/loja/deploys/7", func(w http.ResponseWriter, _ *http.Request) {
		body := map[string]any{"id": 7, "status": "building"}
		if polls.Add(1) > 1 {
			body = map[string]any{"id": 7, "status": "partially_live",
				"process_failures": []map[string]any{{"name": "worker", "reason": "sem memória"}}}
		}
		json.NewEncoder(w).Encode(body)
	})

	text, isErr := m.call("jatai_publicar", map[string]any{"pasta": m.dir})

	if isErr || text != "Publicado com ressalvas: "+lojaURL+"\nworker: sem memória" {
		t.Fatalf("%v %q", isErr, text)
	}
}

func TestPublishTwiceWhileRunningDoesNotStartASecondDeploy(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	gatedDeploy(m)

	first, _ := m.call("jatai_publicar", map[string]any{"pasta": m.dir})
	second, _ := m.call("jatai_publicar", map[string]any{"pasta": m.dir})

	assertContains(t, first, "em andamento", "#7")
	assertContains(t, second, "em andamento", "#7")
	if n := m.calls("POST /api/v1/apps/loja/deploys"); n != 1 {
		t.Fatalf("deploys = %d", n)
	}
}

func TestPublishAfterAFinishedJobStartsANewDeploy(t *testing.T) {
	m := newMCPTest(t)
	m.deps.Budget = 5 * time.Second
	lojaProject(t, m)
	deployStatuses(m, "live")

	m.call("jatai_publicar", map[string]any{"pasta": m.dir})
	text, _ := m.call("jatai_publicar", map[string]any{"pasta": m.dir})

	assertContains(t, text, "Publicado: ")
	if n := m.calls("POST /api/v1/apps/loja/deploys"); n != 2 {
		t.Fatalf("deploys = %d", n)
	}
}

func TestPublishSendsProgressNotifications(t *testing.T) {
	m := newMCPTest(t)
	m.deps.Budget = 5 * time.Second
	var mu sync.Mutex
	var messages []string
	var progress []float64
	m.clientOpts = &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			mu.Lock()
			defer mu.Unlock()
			if req.Params.ProgressToken == "pub-1" {
				messages = append(messages, req.Params.Message)
				progress = append(progress, req.Params.Progress)
			}
		},
	}
	lojaProject(t, m)
	deployStatuses(m, "building", "live")

	params := &mcp.CallToolParams{Name: "jatai_publicar", Arguments: map[string]any{"pasta": m.dir}}
	params.SetProgressToken("pub-1")
	if _, err := m.connect().CallTool(context.Background(), params); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := slices.Clone(messages)
		increasing := slices.IsSorted(progress)
		mu.Unlock()
		if slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "enviando ") }) {
			if !slices.Contains(got, "empacotando") || !increasing {
				t.Fatalf("messages = %q, progress = %v", got, progress)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no \"enviando\" notification; got %q", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPublishCreatesTheProjectAndProvisionsANewApp(t *testing.T) {
	m := newMCPTest(t)
	m.deps.Budget = 5 * time.Second
	m.login()
	dir := filepath.Join(m.dir, "loja")
	os.Mkdir(dir, 0o755)
	writeFile(t, dir, "package.json", "{}")
	var gets atomic.Int32
	m.handle("GET /api/v1/apps/loja", func(w http.ResponseWriter, _ *http.Request) {
		if gets.Add(1) == 1 {
			w.WriteHeader(404)
			json.NewEncoder(w).Encode(map[string]any{"error": "App não encontrado"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 1, "slug": "loja", "status": "active"})
	})
	m.json("POST /api/v1/apps", 201, map[string]any{"id": 1, "slug": "loja", "status": "pending"})
	m.json("POST /api/v1/apps/loja/provision", 202, map[string]any{})
	m.json("POST /api/v1/apps/loja/deploys", 201, map[string]any{"id": 7, "status": "queued"})
	deployStatuses(m, "live")

	text, isErr := m.call("jatai_publicar", map[string]any{"pasta": dir})

	if isErr || !strings.HasPrefix(text, "Publicado: https://loja.jataiapp.com.br\n") {
		t.Fatalf("%v %q", isErr, text)
	}
	if p, _, err := config.LoadProject(dir); err != nil || p.App != "loja" || p.Stack != "node" {
		t.Fatalf("project = %+v, err = %v", p, err)
	}
	if m.calls("POST /api/v1/apps/loja/provision") != 1 {
		t.Fatal("app was not provisioned")
	}
}

func TestPublishErrorBeforeTheDeployIsExplained(t *testing.T) {
	m := newMCPTest(t)
	m.deps.Budget = 5 * time.Second
	lojaProject(t, m)
	m.json("POST /api/v1/apps/loja/deploys", 402, map[string]any{"error": "Cadastre um meio de pagamento"})

	text, isErr := m.call("jatai_publicar", map[string]any{"pasta": m.dir})

	assertContains(t, text, "Cadastre um meio de pagamento.", "https://jataicloud.com.br/billing")
	if !isErr {
		t.Fatal("expected an error")
	}
}

func TestPublishWithoutProjectOrStackAsksForTheStack(t *testing.T) {
	m := newMCPTest(t)
	m.login()

	text, isErr := m.call("jatai_publicar", map[string]any{"pasta": m.dir})

	assertContains(t, text, "rails, node, static, docker")
	if !isErr {
		t.Fatal("expected an error")
	}
}

func TestShutdownStopsTheRunningJobs(t *testing.T) {
	m := newMCPTest(t)
	var st *state
	m.setup = func(s *state, _ *mcp.Server) { st = s }
	lojaProject(t, m)
	gatedDeploy(m)
	m.call("jatai_publicar", map[string]any{"pasta": m.dir})
	j := st.jobFor("loja")
	if j == nil {
		t.Fatal("no running job")
	}

	start := time.Now()
	st.shutdown()

	select {
	case <-j.done:
	default:
		t.Fatal("job still running after shutdown")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("shutdown took %v", time.Since(start))
	}
	text, _ := m.call("jatai_publicar", map[string]any{"pasta": m.dir})
	assertContains(t, text, "encerrando")
}

func TestStatusReadsTheAppWhenThereIsNoJob(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	m.json("GET /api/v1/apps/loja", 200, map[string]any{"slug": "loja", "status": "active", "url": lojaURL,
		"last_deploy": map[string]any{"id": 9, "status": "live", "created_at": "2026-10-16T14:03:00-03:00"}})

	text, isErr := m.call("jatai_status", map[string]any{"app": "loja"})

	if isErr || text != "App loja: active · "+lojaURL+" · último deploy #9: live (16/10/2026 14:03)" {
		t.Fatalf("%v %q", isErr, text)
	}
}

func TestStatusWithoutDeploysSuggestsPublishing(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	m.json("GET /api/v1/apps/loja", 200, map[string]any{"slug": "loja", "status": "pending", "url": lojaURL})

	text, _ := m.call("jatai_status", map[string]any{"app": "loja"})

	if text != "App loja: pending · "+lojaURL+" · ainda sem deploy. Próximo passo: jatai_publicar." {
		t.Fatalf("%q", text)
	}
}

func TestStatusShowsTheBuildLogTailWhenTheLastDeployFailed(t *testing.T) {
	m := newMCPTest(t)
	m.login()
	m.json("GET /api/v1/apps/loja", 200, map[string]any{"slug": "loja", "status": "active", "url": lojaURL,
		"last_deploy": map[string]any{"id": 9, "status": "failed", "created_at": "2026-10-16T14:03:00-03:00"}})
	m.json("GET /api/v1/apps/loja/deploys/9/log", 200, map[string]any{"log": "npm ERR! missing script: build\n"})

	text, _ := m.call("jatai_status", map[string]any{"app": "loja"})

	assertContains(t, text, "último deploy #9: failed", "\nÚltimas linhas do build:\nnpm ERR! missing script: build\n",
		"Corrija o projeto e chame jatai_publicar de novo.")
}

func TestStatusWaitsForADeployStartedElsewhere(t *testing.T) {
	m := newMCPTest(t)
	m.deps.Budget = 5 * time.Second
	m.login()
	var finished atomic.Bool
	m.handle("GET /api/v1/apps/loja", func(w http.ResponseWriter, _ *http.Request) {
		status := "building"
		if finished.Load() {
			status = "live"
		}
		json.NewEncoder(w).Encode(map[string]any{"slug": "loja", "status": "active", "url": lojaURL,
			"last_deploy": map[string]any{"id": 9, "status": status, "created_at": "2026-10-16T14:03:00-03:00"}})
	})
	m.handle("GET /api/v1/apps/loja/deploys/9", func(w http.ResponseWriter, _ *http.Request) {
		finished.Store(true)
		json.NewEncoder(w).Encode(map[string]any{"id": 9, "status": "live"})
	})

	text, _ := m.call("jatai_status", map[string]any{"app": "loja"})

	assertContains(t, text, "último deploy #9: live")
}

func TestStatusWithAguardarFalseAnswersAtOnce(t *testing.T) {
	m := newMCPTest(t)
	lojaProject(t, m)
	gatedDeploy(m)
	m.call("jatai_publicar", map[string]any{"pasta": m.dir})

	text, _ := m.call("jatai_status", map[string]any{"pasta": m.dir, "aguardar": false})

	assertContains(t, text, "Deploy #7 de loja em andamento")
}

func TestStatusWithoutAppAsksForCriarApp(t *testing.T) {
	m := newMCPTest(t)
	m.login()

	text, isErr := m.call("jatai_status", map[string]any{"pasta": m.dir})

	if !isErr || text != "Esta pasta ainda não tem app no Jataí. Chame jatai_criar_app." {
		t.Fatalf("%v %q", isErr, text)
	}
}

func TestBuildLogTailKeepsTheLast4000Characters(t *testing.T) {
	long := strings.Repeat("á", 3000) + strings.Repeat("b", 2000)

	got := lastChars(long, 4000)

	if got != strings.Repeat("á", 2000)+strings.Repeat("b", 2000) {
		t.Fatalf("len = %d runes", len([]rune(got)))
	}
	if lastChars("curto", 4000) != "curto" {
		t.Fatal("short text changed")
	}
}

func TestProgressReporterTrimsTheGemLayout(t *testing.T) {
	j := newJob("loja", time.Now())
	l := j.listen()
	rep := &progressReporter{s: newState(Deps{}), job: j}

	rep.Step("       Enviando 12KB...")("enviado!")
	rep.Line("  at build (index.ts:3)\n")
	rep.Polled("deploying", false, time.Second)

	if j.phase != "implantando" || j.lines[0] != "  at build (index.ts:3)" {
		t.Fatalf("phase = %q, lines = %q", j.phase, j.lines)
	}
	if want := []string{"enviando 12 KB", "construindo", "implantando"}; !slices.Equal(l.queue, want) {
		t.Fatalf("notifications = %q, want %q", l.queue, want)
	}
}

func TestPublishWithAnExplicitAppUsesItWithoutWritingAProjectFile(t *testing.T) {
	m := newMCPTest(t)
	m.deps.Budget = 5 * time.Second
	lojaProject(t, m)
	os.Remove(filepath.Join(m.dir, config.ProjectFile))
	deployStatuses(m, "live")

	text, isErr := m.call("jatai_publicar", map[string]any{"pasta": m.dir, "app": "loja"})

	assertContains(t, text, "Publicado: "+lojaURL)
	if _, err := os.Stat(filepath.Join(m.dir, config.ProjectFile)); isErr || err == nil {
		t.Fatalf("isErr = %v, .jatai.yml written: %v", isErr, err)
	}

	missing, isErr := m.call("jatai_publicar", map[string]any{"pasta": m.dir, "app": "outra"})
	if !isErr || missing != "App não encontrado neste time. Use jatai_conta para ver os apps." {
		t.Fatalf("%v %q", isErr, missing)
	}
	if m.calls("POST /api/v1/apps") != 0 {
		t.Fatal("created an app for an explicit slug")
	}
}
