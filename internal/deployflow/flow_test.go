package deployflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jataicloud/cli/internal/api"
	"github.com/jataicloud/cli/internal/cable"
	"github.com/jataicloud/cli/internal/cable/cabletest"
)

type polled struct {
	status     string
	networkErr bool
}

// recorder guarda as chamadas do Reporter, na ordem em que chegaram.
type recorder struct {
	mu     sync.Mutex
	infos  []string
	warns  []string
	lines  []string
	steps  []string
	polled []polled
}

func (r *recorder) Info(msg string) { r.mu.Lock(); r.infos = append(r.infos, msg); r.mu.Unlock() }
func (r *recorder) Warn(msg string) { r.mu.Lock(); r.warns = append(r.warns, msg); r.mu.Unlock() }
func (r *recorder) Line(l string)   { r.mu.Lock(); r.lines = append(r.lines, l); r.mu.Unlock() }
func (r *recorder) Step(msg string) func(string) {
	r.mu.Lock()
	r.steps = append(r.steps, msg)
	r.mu.Unlock()
	return func(result string) {
		r.mu.Lock()
		r.steps = append(r.steps, result)
		r.mu.Unlock()
	}
}

func (r *recorder) Polled(status string, networkErr bool, _ time.Duration) {
	r.mu.Lock()
	r.polled = append(r.polled, polled{status, networkErr})
	r.mu.Unlock()
}

func newRunner(t *testing.T, mux *http.ServeMux) (*Runner, *recorder) {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := api.New(srv.URL, "jat_teste", "jatai-test")
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	return &Runner{
		Client: c,
		Cable: cable.Options{
			APIURL: srv.URL, Token: "jat_teste", UserAgent: "jatai-test",
			WelcomeTimeout: 2 * time.Second, ReconnectDelays: []time.Duration{time.Millisecond},
		},
		Reporter: rec,
		Now:      time.Now,
		Sleep:    func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
	}, rec
}

func TestWaitDeploySendsBuildLinesToTheReporter(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/cable", &cabletest.Handler{Messages: map[string][]map[string]any{
		"DeployLogChannel":    {{"line": "Step 1/9"}},
		"DeployStatusChannel": {{"status": "live"}},
	}})
	r, rec := newRunner(t, mux)

	status, err := r.WaitDeploy(context.Background(), "loja", 7, time.Minute, 5)

	if err != nil || status != "live" {
		t.Fatalf("status = %q, err = %v", status, err)
	}
	if !reflect.DeepEqual(rec.lines, []string{"Step 1/9"}) {
		t.Errorf("lines = %q", rec.lines)
	}
	if len(rec.warns) != 0 || len(rec.polled) != 0 {
		t.Errorf("warns = %q, polled = %v", rec.warns, rec.polled)
	}
}

func TestWaitDeployPollingReportsEachPoll(t *testing.T) {
	mux := http.NewServeMux()
	statuses := []string{"building", "building", "live"}
	mux.HandleFunc("GET /api/v1/apps/loja/deploys/7", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id": 7, "status": statuses[0]})
		statuses = statuses[1:]
	})
	r, rec := newRunner(t, mux)

	status, err := r.WaitDeploy(context.Background(), "loja", 7, time.Minute, 5)

	if err != nil || status != "live" {
		t.Fatalf("status = %q, err = %v", status, err)
	}
	want := []polled{{"building", false}, {"building", false}}
	if !reflect.DeepEqual(rec.polled, want) {
		t.Errorf("polled = %v, want %v", rec.polled, want)
	}
	if len(rec.warns) != 1 {
		t.Errorf("warns = %q, want the polling fallback notice", rec.warns)
	}
}

func TestBuildLogTailAsksForTheLastLines(t *testing.T) {
	mux := http.NewServeMux()
	var tail string
	mux.HandleFunc("GET /api/v1/apps/loja/deploys/7/log", func(w http.ResponseWriter, r *http.Request) {
		tail = r.URL.Query().Get("tail")
		json.NewEncoder(w).Encode(map[string]any{"log": "a\nb\n"})
	})
	r, _ := newRunner(t, mux)

	lines, err := r.BuildLogTail(context.Background(), "loja", 7, 60)

	if err != nil || !reflect.DeepEqual(lines, []string{"a", "b"}) {
		t.Fatalf("lines = %q, err = %v", lines, err)
	}
	if tail != "60" {
		t.Errorf("tail = %q, want 60", tail)
	}
}

func TestProvisionReturnsABillingErrorAtOnce(t *testing.T) {
	for _, status := range []int{http.StatusPaymentRequired, http.StatusForbidden, http.StatusNotFound} {
		mux := http.NewServeMux()
		var polls int
		mux.HandleFunc("POST /api/v1/apps/loja/provision", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]any{"error": "Cadastre um cartão em https://jataicloud.com.br/billing"})
		})
		mux.HandleFunc("GET /api/v1/apps/loja", func(http.ResponseWriter, *http.Request) { polls++ })
		r, rec := newRunner(t, mux)

		err := r.Provision(context.Background(), App{ID: 1, Slug: "loja", Status: "pending"})

		var apiErr *api.Error
		if !errors.As(err, &apiErr) || apiErr.Status != status {
			t.Fatalf("status %d: err = %v, want the api error", status, err)
		}
		if polls != 0 || len(rec.warns) != 0 || len(rec.polled) != 0 {
			t.Errorf("status %d: it must not wait: polls = %d, warns = %q", status, polls, rec.warns)
		}
	}
}

func TestProvisionKeepsWaitingWhenTheServerSaysItIsAlreadyProvisioning(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/apps/loja/provision", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{"error": "já provisionando"})
	})
	mux.Handle("/cable", &cabletest.Handler{Messages: map[string][]map[string]any{
		"ProvisionStatusChannel": {{"status": "provisioning"}, {"status": "active"}},
	}})
	r, _ := newRunner(t, mux)

	if err := r.Provision(context.Background(), App{ID: 1, Slug: "loja", Status: "pending"}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestWaitDeployReturnsTheContextErrorWithoutFallingBackToPolling(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/cable", &cabletest.Handler{})
	r, rec := newRunner(t, mux)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.WaitDeploy(ctx, "loja", 7, time.Minute, 5)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(rec.warns) != 0 || len(rec.polled) != 0 {
		t.Errorf("warns = %q, polled = %v", rec.warns, rec.polled)
	}
}
