package cable

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jataicloud/cli/internal/cable/cabletest"
)

func connect(t *testing.T, h *cabletest.Handler) *Conn {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	conn, err := Connect(context.Background(), Options{
		APIURL: srv.URL, Token: "jat_x", UserAgent: "jatai-cli/test",
		WelcomeTimeout: 2 * time.Second, ReconnectDelays: []time.Duration{time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	return conn
}

func TestWaitForStreamsLinesAndReturnsTheStatus(t *testing.T) {
	h := &cabletest.Handler{Messages: map[string][]map[string]any{
		"DeployLogChannel":    {{"line": "Construindo imagem..."}, {"line": "Enviando imagem..."}},
		"DeployStatusChannel": {{"status": "building"}, {"status": "live"}},
	}}
	conn := connect(t, h)
	ctx := context.Background()
	params := map[string]any{"deploy_id": 42}

	var lines []string
	if _, err := conn.Subscribe(ctx, "DeployLogChannel", params); err != nil {
		t.Fatal(err)
	}
	status, err := conn.WaitFor(ctx, "DeployStatusChannel", params, []string{"live", "failed"}, func(l string) { lines = append(lines, l) })

	if err != nil || status != "live" {
		t.Fatalf("status = %q, err = %v", status, err)
	}
	if !slices.Equal(lines, []string{"Construindo imagem...", "Enviando imagem..."}) {
		t.Fatalf("lines = %v", lines)
	}
	if h.Headers().Get("Authorization") != "Bearer jat_x" || h.Headers().Get("Sec-WebSocket-Protocol") != "actioncable-v1-json" {
		t.Fatalf("headers = %v", h.Headers())
	}
}

func TestRejectedSubscriptionIsAConnectionError(t *testing.T) {
	conn := connect(t, &cabletest.Handler{Reject: map[string]bool{"AppRunChannel": true}})

	_, err := conn.WaitFor(context.Background(), "AppRunChannel", map[string]any{"run_id": 1}, []string{"succeeded"}, nil)
	if !errors.Is(err, ErrConnection) {
		t.Fatalf("err = %v", err)
	}
}

func TestReconnectsAndResubscribesWhenTheServerDrops(t *testing.T) {
	h := &cabletest.Handler{
		DropAfterFirstSubscribe: true,
		Messages:                map[string][]map[string]any{"DeployStatusChannel": {{"status": "live"}}},
	}
	conn := connect(t, h)

	status, err := conn.WaitFor(context.Background(), "DeployStatusChannel", map[string]any{"deploy_id": 7}, []string{"live"}, nil)
	if err != nil || status != "live" {
		t.Fatalf("status = %q, err = %v", status, err)
	}
	if len(h.Subscribed()) != 2 {
		t.Fatalf("subscribed = %v", h.Subscribed())
	}
}

func TestConnectFailsWithoutAServer(t *testing.T) {
	_, err := Connect(context.Background(), Options{APIURL: "http://127.0.0.1:1", WelcomeTimeout: time.Second})
	if !errors.Is(err, ErrConnection) {
		t.Fatalf("err = %v", err)
	}
}

func TestIdentifierIsStableJSON(t *testing.T) {
	got := Identifier("DeployLogChannel", map[string]any{"deploy_id": 42})
	if got != `{"channel":"DeployLogChannel","deploy_id":42}` {
		t.Fatalf("identifier = %s", got)
	}
}

func TestDebugShowsFramesButNeitherPingsNorTheToken(t *testing.T) {
	srv := httptest.NewServer(&cabletest.Handler{Messages: map[string][]map[string]any{
		"AppRunChannel": {{"status": "succeeded"}},
	}})
	t.Cleanup(srv.Close)
	var debug bytes.Buffer
	conn, err := Connect(context.Background(), Options{
		APIURL: srv.URL, Token: "jat_segredo", WelcomeTimeout: 2 * time.Second, Debug: &debug,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)

	if _, err := conn.WaitFor(context.Background(), "AppRunChannel", map[string]any{"run_id": 1}, []string{"succeeded"}, nil); err != nil {
		t.Fatal(err)
	}

	out := debug.String()
	if !strings.Contains(out, `"command":"subscribe"`) || !strings.Contains(out, "succeeded") ||
		strings.Contains(out, "jat_segredo") || strings.Contains(out, "ping") {
		t.Fatalf("debug = %q", out)
	}
}
