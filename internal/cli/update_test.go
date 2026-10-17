package cli

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestNotifiesANewVersionAtMostOncePerDay(t *testing.T) {
	e := newTestEnv(t)
	delete(e.vars, "JATAI_NO_UPDATE_CHECK")
	calls := 0
	e.app.LatestVersion = func(context.Context) (string, error) { calls++; return "9.9.9", nil }
	e.app.Executable = func() (string, error) { return "/opt/homebrew/Caskroom/jatai/2.0.0/jatai", nil }

	e.run("version")
	e.run("version")

	if calls != 1 || strings.Count(e.out.String(), "Nova versão da CLI: 9.9.9") != 1 || !strings.Contains(e.out.String(), "brew upgrade jatai") {
		t.Fatalf("calls = %d, out = %q", calls, e.out)
	}
}

func TestUpdateCheckCanBeDisabled(t *testing.T) {
	e := newTestEnv(t)
	calls := 0
	e.app.LatestVersion = func(context.Context) (string, error) { calls++; return "9.9.9", nil }

	e.run("version")

	if calls != 0 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestUpdateDefersToHomebrew(t *testing.T) {
	e := newTestEnv(t)
	e.app.Executable = func() (string, error) { return "/usr/local/Cellar/jatai/2.0.0/bin/jatai", nil }

	if code := e.run("update"); code != 0 || !strings.Contains(e.out.String(), "brew upgrade jatai") {
		t.Fatalf("code = %d, out = %q", code, e.out)
	}
}

func TestUpdateFromNpmOnlyPrintsTheHint(t *testing.T) {
	e := newTestEnv(t)
	downloads, lookups := 0, 0
	e.handle("/", func(http.ResponseWriter, *http.Request) { downloads++ })
	e.app.DownloadBase = e.vars["JATAI_API_URL"]
	e.app.LatestVersion = func(context.Context) (string, error) { lookups++; return "9.9.9", nil }
	e.app.Executable = func() (string, error) {
		return "/usr/local/lib/node_modules/@jataicloud/cli-darwin-arm64/bin/jatai", nil
	}

	code := e.run("update")

	if code != 0 || !strings.Contains(e.out.String(), "Esta instalação veio do npm. Atualize com: npm update -g jatai") {
		t.Fatalf("code = %d, out = %q, err = %q", code, e.out, e.err)
	}
	if downloads != 0 || lookups != 0 {
		t.Fatalf("downloads = %d, lookups = %d", downloads, lookups)
	}
}

func TestUpdateFromNpxPointsToTheLatestTag(t *testing.T) {
	e := newTestEnv(t)
	e.app.Executable = func() (string, error) {
		return "/home/b/.npm/_npx/1a2b/node_modules/@jataicloud/cli-linux-x64/bin/jatai", nil
	}

	code := e.run("update")

	if code != 0 || !strings.Contains(e.out.String(), "npx -y jatai@latest") {
		t.Fatalf("code = %d, out = %q", code, e.out)
	}
}

func TestOutdatedCLIPointsToThisMachinesUpgradeCommand(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.writeProject("app: loja\n")
	e.app.Executable = func() (string, error) { return `C:\Users\b\scoop\apps\jatai\current\jatai.exe`, nil }
	e.json("GET /api/v1/apps/loja", http.StatusUpgradeRequired, map[string]any{
		"error":   "Sua CLI está desatualizada: a versão mínima é 2.1.0.",
		"install": "https://jataicloud.com.br/install.sh",
	})

	code := e.run("status")

	if code != 1 || !strings.Contains(e.err.String(), "versão mínima é 2.1.0") || !strings.Contains(e.err.String(), "scoop update jatai") {
		t.Fatalf("code = %d, err = %q", code, e.err)
	}
}
