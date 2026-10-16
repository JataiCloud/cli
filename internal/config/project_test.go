package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProjectMissing(t *testing.T) {
	_, _, err := LoadProject(t.TempDir())
	if !errors.Is(err, ErrNoProject) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadProjectFallsBackToLegacyFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, LegacyProjectFile), []byte("app: antigo\nstack: node\n"), 0o644)

	p, path, err := LoadProject(dir)
	if err != nil || p.App != "antigo" || filepath.Base(path) != LegacyProjectFile {
		t.Fatalf("p = %+v, path = %s, err = %v", p, path, err)
	}
	if p.DisplayName() != "antigo" {
		t.Fatalf("display name = %q", p.DisplayName())
	}
}

func TestWriteProjectRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Project{App: "loja", Name: "Loja", Stack: "rails", Preset: "base", Region: "br-se1", Branch: "main"}
	if err := WriteProject(dir, want); err != nil {
		t.Fatal(err)
	}
	got, _, err := LoadProject(dir)
	if err != nil || got.App != want.App || got.Name != want.Name || got.Branch != want.Branch {
		t.Fatalf("got = %+v, err = %v", got, err)
	}
}

func TestPersistSlugKeepsOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ProjectFile)
	os.WriteFile(path, []byte("app: Alves Barbearia\nstack: static\nstatic:\n  root: public\n  spa: true\n"), 0o644)

	if err := PersistSlug(path, "alves-barbearia-1"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	for _, want := range []string{"app: alves-barbearia-1", "name: Alves Barbearia", "static:\n  root: public", "spa: true"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}
