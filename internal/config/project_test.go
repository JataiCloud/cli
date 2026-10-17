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

func TestNewProjectDerivesSlugAndDefaults(t *testing.T) {
	p, err := NewProject("Minha Loja", "node", "")

	want := Project{App: "minha-loja", Name: "Minha Loja", Stack: "node", Preset: "base", Region: "br-se1", Branch: "main"}
	if err != nil || p != want {
		t.Fatalf("p = %+v, err = %v", p, err)
	}
	if p, _ := NewProject("Loja", "rails", "develop"); p.Branch != "develop" {
		t.Fatalf("branch = %q", p.Branch)
	}
}

func TestNewProjectRejectsANameWithoutLettersOrDigits(t *testing.T) {
	if _, err := NewProject("!!!", "node", "main"); !errors.Is(err, ErrNoUsableName) {
		t.Fatalf("err = %v", err)
	}
}

func TestDetectStack(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  string
	}{
		"rails":                 {map[string]string{"Gemfile": `gem "rails"`, "package.json": "{}"}, "rails"},
		"node":                  {map[string]string{"package.json": "{}", "Dockerfile": "FROM x"}, "node"},
		"docker":                {map[string]string{"Dockerfile": "FROM x", "index.html": ""}, "docker"},
		"static":                {map[string]string{"index.html": "<html>"}, "static"},
		"gemfile without rails": {map[string]string{"Gemfile": `gem "sinatra"`}, ""},
		"empty":                 {nil, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for f, content := range c.files {
				os.WriteFile(filepath.Join(dir, f), []byte(content), 0o644)
			}
			if got := DetectStack(dir); got != c.want {
				t.Fatalf("DetectStack = %q, want %q", got, c.want)
			}
		})
	}
}

func TestNewProjectTrimsTheNameAndTreatsDetachedHeadAsMain(t *testing.T) {
	p, err := NewProject("  Minha Loja \n", "node", "HEAD")

	if err != nil || p.Name != "Minha Loja" || p.App != "minha-loja" {
		t.Fatalf("p = %+v, err = %v", p, err)
	}
	if p.Branch != "main" {
		t.Fatalf("branch = %q, want main for a detached HEAD", p.Branch)
	}
	if p, _ := NewProject("Loja", "node", " develop\n"); p.Branch != "develop" {
		t.Fatalf("branch = %q", p.Branch)
	}
}

func TestNormalizeBranch(t *testing.T) {
	for in, want := range map[string]string{"": "main", "HEAD": "main", " develop\n": "develop", "main": "main"} {
		if got := NormalizeBranch(in); got != want {
			t.Errorf("NormalizeBranch(%q) = %q, want %q", in, got, want)
		}
	}
}
