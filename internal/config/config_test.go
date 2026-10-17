package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestSaveTokenTightensExistingDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permissões POSIX")
	}
	p := Paths{Home: t.TempDir()}
	if err := os.MkdirAll(p.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SaveToken(p, "jat_x"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v", info.Mode().Perm())
	}
}

func TestSaveTokenReplacesWiderCredentials(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permissões POSIX")
	}
	p := Paths{Home: t.TempDir()}
	if err := os.MkdirAll(p.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.CredentialsFile(), []byte(`{"token":"antigo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.CredentialsFile(), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(p.CredentialsFile())
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveToken(p, "jat_novo"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p.CredentialsFile())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v", info.Mode().Perm())
	}
	// Troca atômica: o arquivo novo nasce com 0600 e substitui o antigo; o
	// inode de 0644 nunca recebe o token.
	if os.SameFile(before, info) {
		t.Fatal("credentials rewritten in place; expected atomic replace")
	}
	if got := Token(p, env(nil)); got != "jat_novo" {
		t.Fatalf("token = %q", got)
	}
	entries, _ := os.ReadDir(p.Dir())
	if len(entries) != 1 {
		t.Fatalf("leftover files in %s: %d entries", p.Dir(), len(entries))
	}
}

func TestWritesRefuseAnUnknownHome(t *testing.T) {
	wd := t.TempDir()
	t.Chdir(wd)
	for _, home := range []string{"", "relativa"} {
		p := Paths{Home: home}
		if err := SaveToken(p, "jat_x"); !errors.Is(err, ErrNoHome) {
			t.Errorf("SaveToken(%q) err = %v", home, err)
		}
		if err := ClearToken(p); !errors.Is(err, ErrNoHome) {
			t.Errorf("ClearToken(%q) err = %v", home, err)
		}
		if err := SaveState(p, State{LatestVersion: "2.0.0"}); !errors.Is(err, ErrNoHome) {
			t.Errorf("SaveState(%q) err = %v", home, err)
		}
	}
	if entries, _ := os.ReadDir(wd); len(entries) != 0 {
		t.Fatalf("wrote into the working directory: %v", entries)
	}
	if !strings.Contains(ErrNoHome.Error(), "HOME") {
		t.Fatalf("message = %q", ErrNoHome)
	}
}

func TestReadsIgnoreFilesRelativeToAnUnknownHome(t *testing.T) {
	wd := t.TempDir()
	t.Chdir(wd)
	os.MkdirAll(filepath.Join(wd, ".jatai"), 0o700)
	os.WriteFile(filepath.Join(wd, ".jatai", "credentials.json"), []byte(`{"token":"do-projeto"}`), 0o600)
	os.WriteFile(filepath.Join(wd, ".jatai", "state.json"), []byte(`{"latest_version":"9.9.9"}`), 0o600)

	p := Paths{}
	if got := Token(p, env(nil)); got != "" {
		t.Fatalf("token = %q", got)
	}
	if got := Token(p, env(map[string]string{"JATAI_TOKEN": "jat_env"})); got != "jat_env" {
		t.Fatalf("JATAI_TOKEN = %q", got)
	}
	if got := LoadState(p); got.LatestVersion != "" {
		t.Fatalf("state = %+v", got)
	}
}
