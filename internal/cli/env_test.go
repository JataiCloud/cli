package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jataicloud/cli/internal/config"
	"github.com/jataicloud/cli/internal/ui"
	"github.com/jataicloud/cli/internal/ui/uitest"
)

type testEnv struct {
	t        *testing.T
	app      *App
	out, err *bytes.Buffer
	prompter *uitest.ScriptedPrompter
	mux      *http.ServeMux
	dir      string
	vars     map[string]string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	e := &testEnv{
		t: t, out: &bytes.Buffer{}, err: &bytes.Buffer{}, prompter: &uitest.ScriptedPrompter{},
		mux: mux, dir: t.TempDir(),
		vars: map[string]string{"JATAI_API_URL": srv.URL, "JATAI_NO_UPDATE_CHECK": "1"},
	}
	e.app = &App{
		UI:              &ui.UI{Out: e.out, Err: e.err, Interactive: true, Prompter: e.prompter},
		Paths:           config.Paths{Home: t.TempDir()},
		Version:         "2.0.0",
		Getenv:          func(k string) string { return e.vars[k] },
		Getwd:           func() (string, error) { return e.dir, nil },
		Now:             time.Now,
		Sleep:           func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
		HTTP:            srv.Client(),
		ReconnectDelays: []time.Duration{time.Millisecond},
		WelcomeTimeout:  2 * time.Second,
		Git:             func(string, ...string) (string, error) { return "", errors.New("sem git") },
		Hostname:        func() (string, error) { return "maquina-teste", nil },
		OpenBrowser:     func(string) error { return nil },
		CanOpenBrowser:  func() bool { return false },
	}
	return e
}

func (e *testEnv) run(args ...string) int { return Run(context.Background(), e.app, args) }

func (e *testEnv) login() {
	if err := config.SaveToken(e.app.Paths, "jat_teste"); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) writeProject(yaml string) {
	if err := os.WriteFile(filepath.Join(e.dir, config.ProjectFile), []byte(yaml), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) answer(answers ...string) {
	e.prompter.Answers = append(e.prompter.Answers, answers...)
}

func (e *testEnv) handle(pattern string, h http.HandlerFunc) { e.mux.HandleFunc(pattern, h) }

func (e *testEnv) json(pattern string, status int, body any) {
	e.handle(pattern, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(body)
	})
}
