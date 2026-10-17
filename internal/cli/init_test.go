package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jataicloud/cli/internal/config"
	"github.com/jataicloud/cli/internal/ui"
)

func TestInitDetectsRailsAndWritesTheProject(t *testing.T) {
	e := newTestEnv(t)
	os.WriteFile(filepath.Join(e.dir, "Gemfile"), []byte(`gem "rails", "~> 8.1"`), 0o644)
	e.app.Git = func(string, ...string) (string, error) { return "develop", nil }
	e.answer("y", "Alves Barbearia", "escala_1")

	code := e.run("init")

	p, _, err := config.LoadProject(e.dir)
	if code != 0 || err != nil {
		t.Fatalf("code = %d, err = %v, stderr = %q", code, err, e.err)
	}
	want := config.Project{App: "alves-barbearia", Name: "Alves Barbearia", Stack: "rails", Preset: "escala_1", Region: "br-se1", Branch: "develop"}
	if p.App != want.App || p.Name != want.Name || p.Stack != want.Stack || p.Preset != want.Preset || p.Region != want.Region || p.Branch != want.Branch {
		t.Fatalf("project = %+v", p)
	}
	for _, s := range []string{"Stack detectado: Rails", "Identificador: alves-barbearia", "PG Base provisionado"} {
		if !strings.Contains(e.out.String(), s) {
			t.Errorf("missing %q", s)
		}
	}
}

func TestInitAsksTheStackWhenItCannotDetect(t *testing.T) {
	e := newTestEnv(t)
	e.answer("static", "Site", "base")

	e.run("init")

	p, _, _ := config.LoadProject(e.dir)
	if p.Stack != "static" || p.Branch != "main" || strings.Contains(e.out.String(), "PG Base") {
		t.Fatalf("project = %+v, out = %q", p, e.out)
	}
}

func TestInitRetriesANameWithoutUsableCharacters(t *testing.T) {
	e := newTestEnv(t)
	e.answer("docker", "🙂", "Loja", "base")

	e.run("init")

	p, _, _ := config.LoadProject(e.dir)
	if p.App != "loja" || !strings.Contains(e.err.String(), "Não consegui gerar um identificador") {
		t.Fatalf("project = %+v, err = %q", p, e.err)
	}
}

func TestInitKeepsAnExistingFileWhenTheAnswerIsNo(t *testing.T) {
	e := newTestEnv(t)
	e.writeProject("app: original\n")
	e.answer("n")

	e.run("init")

	p, _, _ := config.LoadProject(e.dir)
	if p.App != "original" {
		t.Fatalf("project = %+v", p)
	}
}

func TestInitNeedsATerminal(t *testing.T) {
	e := newTestEnv(t)
	e.app.UI.Prompter = ui.NonInteractivePrompter{}

	if code := e.run("init"); code != 1 || !strings.Contains(e.err.String(), "terminal interativo") {
		t.Fatalf("code = %d, err = %q", code, e.err)
	}
}
