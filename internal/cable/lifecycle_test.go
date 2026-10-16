package cable

import (
	"context"
	"errors"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jataicloud/cli/internal/cable/cabletest"
)

// cableGoroutines devolve as pilhas das goroutines que pertencem ao cliente
// ActionCable ou à biblioteca de WebSocket (cliente e servidor falso).
func cableGoroutines() []string {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var found []string
	for _, stack := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(stack, "cable.(*Conn)") || strings.Contains(stack, "coder/websocket") {
			found = append(found, stack)
		}
	}
	return found
}

func expectNoGoroutines(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		left := cableGoroutines()
		if len(left) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines vazaram:\n%s", len(left), strings.Join(left, "\n\n"))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newServer(t *testing.T, h *cabletest.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func options(url string) Options {
	return Options{
		APIURL: url, Token: "jat_x", WelcomeTimeout: 2 * time.Second,
		ReconnectDelays: []time.Duration{time.Millisecond},
	}
}

func waitSubscribed(t *testing.T, h *cabletest.Handler, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(h.Subscribed()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("subscribed = %v, want %d", h.Subscribed(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCloseEndsEveryGoroutine(t *testing.T) {
	h := &cabletest.Handler{Messages: map[string][]map[string]any{"AppRunChannel": {{"status": "succeeded"}}}}
	conn, err := Connect(context.Background(), options(newServer(t, h).URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.WaitFor(context.Background(), "AppRunChannel", map[string]any{"run_id": 1}, []string{"succeeded"}, nil); err != nil {
		t.Fatal(err)
	}

	conn.Close()
	conn.Close() // idempotente

	expectNoGoroutines(t)
}

func TestCancellingTheConnectContextEndsEveryGoroutine(t *testing.T) {
	h := &cabletest.Handler{}
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := Connect(ctx, options(newServer(t, h).URL))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	cancel()

	expectNoGoroutines(t)
}

func TestCloseAfterReconnectEndsEveryGoroutine(t *testing.T) {
	h := &cabletest.Handler{
		DropAfterFirstSubscribe: true,
		Messages:                map[string][]map[string]any{"DeployStatusChannel": {{"status": "live"}}},
	}
	conn, err := Connect(context.Background(), options(newServer(t, h).URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.WaitFor(context.Background(), "DeployStatusChannel", map[string]any{"deploy_id": 7}, []string{"live"}, nil); err != nil {
		t.Fatal(err)
	}

	conn.Close()

	expectNoGoroutines(t)
}

func TestCloseUnblocksAPendingWaitFor(t *testing.T) {
	h := &cabletest.Handler{}
	conn, err := Connect(context.Background(), options(newServer(t, h).URL))
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := conn.WaitFor(context.Background(), "AppRunChannel", map[string]any{"run_id": 1}, []string{"succeeded"}, nil)
		result <- err
	}()
	waitSubscribed(t, h, 1)

	conn.Close()

	select {
	case err := <-result:
		if !errors.Is(err, ErrConnection) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitFor continua bloqueado depois do Close")
	}
	expectNoGoroutines(t)
}

func TestCloseDuringReconnectLeavesNothingBehind(t *testing.T) {
	h := &cabletest.Handler{DropAfterFirstSubscribe: true}
	opts := options(newServer(t, h).URL)
	opts.ReconnectDelays = []time.Duration{20 * time.Millisecond, 20 * time.Millisecond}
	conn, err := Connect(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := conn.WaitFor(context.Background(), "DeployStatusChannel", map[string]any{"deploy_id": 7}, []string{"live"}, nil)
		result <- err
	}()
	waitSubscribed(t, h, 1)

	conn.Close()

	select {
	case <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("WaitFor continua bloqueado depois do Close")
	}
	expectNoGoroutines(t)
}

func TestACancelledWaitForDoesNotKillTheConnection(t *testing.T) {
	h := &cabletest.Handler{
		DropAfterFirstSubscribe: true,
		Messages:                map[string][]map[string]any{"DeployStatusChannel": {{"status": "live"}}},
	}
	conn, err := Connect(context.Background(), options(newServer(t, h).URL))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	first, cancel := context.WithCancel(context.Background())
	if _, err := conn.WaitFor(first, "DeployStatusChannel", map[string]any{"deploy_id": 1}, []string{"live"}, nil); err != nil {
		t.Fatal(err)
	}
	cancel() // o WaitFor acabou; o contexto dele não pode derrubar a conexão

	status, err := conn.WaitFor(context.Background(), "DeployStatusChannel", map[string]any{"deploy_id": 2}, []string{"live"}, nil)

	if err != nil || status != "live" {
		t.Fatalf("status = %q, err = %v", status, err)
	}
}

func TestSubscribingTwiceToTheSameChannelKeepsASingleSubscription(t *testing.T) {
	conn := connect(t, &cabletest.Handler{})
	params := map[string]any{"deploy_id": 7}

	conn.Subscribe(context.Background(), "DeployLogChannel", params)
	conn.Subscribe(context.Background(), "DeployLogChannel", params)

	if len(conn.subs) != 1 {
		t.Fatalf("subs = %v", conn.subs)
	}
}
