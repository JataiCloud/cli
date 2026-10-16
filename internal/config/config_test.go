package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func env(values map[string]string) func(string) string {
	return func(k string) string { return values[k] }
}

func TestAPIURL(t *testing.T) {
	if got := APIURL(env(nil)); got != DefaultAPIURL {
		t.Fatalf("default = %q", got)
	}
	if got := APIURL(env(map[string]string{"JATAI_API_URL": "http://localhost:3000/"})); got != "http://localhost:3000" {
		t.Fatalf("JATAI_API_URL = %q", got)
	}
	if got := APIURL(env(map[string]string{"BRASA_API_URL": "https://api.usebrasa.com.br"})); got != "https://api.usebrasa.com.br" {
		t.Fatalf("legacy = %q", got)
	}
}

func TestTokenPrecedence(t *testing.T) {
	p := Paths{Home: t.TempDir()}
	if Token(p, env(nil)) != "" {
		t.Fatal("expected no token")
	}

	legacy := p.LegacyCredentialsFile()
	os.MkdirAll(filepath.Dir(legacy), 0o700)
	os.WriteFile(legacy, []byte(`{"token":"legado"}`), 0o600)
	if got := Token(p, env(nil)); got != "legado" {
		t.Fatalf("legacy file = %q", got)
	}

	if err := SaveToken(p, "jat_arquivo"); err != nil {
		t.Fatal(err)
	}
	if got := Token(p, env(nil)); got != "jat_arquivo" {
		t.Fatalf("credentials file = %q", got)
	}
	if got := Token(p, env(map[string]string{"JATAI_TOKEN": "jat_ci"})); got != "jat_ci" {
		t.Fatalf("JATAI_TOKEN = %q", got)
	}
}

func TestSaveTokenIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permissões POSIX")
	}
	p := Paths{Home: t.TempDir()}
	SaveToken(p, "jat_x")
	info, err := os.Stat(p.CredentialsFile())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

func TestClearTokenRemovesBothFiles(t *testing.T) {
	p := Paths{Home: t.TempDir()}
	SaveToken(p, "jat_x")
	os.MkdirAll(filepath.Dir(p.LegacyCredentialsFile()), 0o700)
	os.WriteFile(p.LegacyCredentialsFile(), []byte(`{"token":"legado"}`), 0o600)

	if err := ClearToken(p); err != nil {
		t.Fatal(err)
	}
	if Token(p, env(nil)) != "" {
		t.Fatal("token still present")
	}
	if err := ClearToken(p); err != nil {
		t.Fatalf("clearing twice: %v", err)
	}
}

func TestStateRoundTrip(t *testing.T) {
	p := Paths{Home: t.TempDir()}
	if !LoadState(p).LastUpdateCheck.IsZero() {
		t.Fatal("expected empty state")
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	SaveState(p, State{LastUpdateCheck: now, LatestVersion: "2.1.0"})
	got := LoadState(p)
	if !got.LastUpdateCheck.Equal(now) || got.LatestVersion != "2.1.0" {
		t.Fatalf("state = %+v", got)
	}
}
