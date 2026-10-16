package auth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jataicloud/cli/internal/api"
)

type fakePoster struct {
	responses []any // *api.Error ou Grant
	calls     int
}

func (f *fakePoster) Post(_ context.Context, _ string, _ any, out any) error {
	r := f.responses[f.calls]
	f.calls++
	if err, ok := r.(error); ok {
		return err
	}
	data, _ := json.Marshal(r)
	return json.Unmarshal(data, out)
}

func pending(code string) error {
	return &api.Error{Status: 400, Message: code, Body: map[string]any{"error": code}}
}

func TestWaitHandlesPendingAndSlowDownUntilTheGrant(t *testing.T) {
	grant := Grant{Token: "jat_novo"}
	grant.User.Email = "bruno@exemplo.com"
	poster := &fakePoster{responses: []any{pending("authorization_pending"), pending("slow_down"), grant}}
	var sleeps []time.Duration
	sleep := func(_ context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil }

	got, err := Wait(context.Background(), poster, Start{DeviceCode: "dev", Interval: 5}, sleep)

	if err != nil || got.Token != "jat_novo" || got.User.Email != "bruno@exemplo.com" {
		t.Fatalf("got = %+v, err = %v", got, err)
	}
	want := []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second}
	for i := range want {
		if sleeps[i] != want[i] {
			t.Fatalf("sleeps = %v", sleeps)
		}
	}
}

func TestWaitStopsOnDenialOrExpiry(t *testing.T) {
	noSleep := func(context.Context, time.Duration) error { return nil }
	for code, want := range map[string]error{"access_denied": ErrDenied, "expired_token": ErrExpired} {
		_, err := Wait(context.Background(), &fakePoster{responses: []any{pending(code)}}, Start{Interval: 1}, noSleep)
		if !errors.Is(err, want) {
			t.Errorf("%s: err = %v", code, err)
		}
	}
}

func TestWaitReturnsOtherErrorsAsIs(t *testing.T) {
	boom := &api.Error{Status: 500, Message: "Erro na API (500)"}
	_, err := Wait(context.Background(), &fakePoster{responses: []any{boom}}, Start{Interval: 1},
		func(context.Context, time.Duration) error { return nil })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestCanOpenBrowser(t *testing.T) {
	ssh := func(k string) string {
		if k == "SSH_CONNECTION" {
			return "1.2.3.4 22 5.6.7.8 22"
		}
		return ""
	}
	if CanOpenBrowser(ssh) {
		t.Fatal("should not open a browser over SSH")
	}
}
