package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jataicloud/cli/internal/config"
	"github.com/jataicloud/cli/internal/update"
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

func TestNoNoticeRightAfterASuccessfulUpdate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pacote tar.gz")
	}
	e := newTestEnv(t)
	delete(e.vars, "JATAI_NO_UPDATE_CHECK")
	home, err := filepath.EvalSymlinks(e.app.Paths.Home)
	if err != nil {
		t.Fatal(err)
	}
	e.app.Paths.Home = home
	exe := filepath.Join(e.app.Paths.BinDir(), "jatai")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("velho"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.app.Executable = func() (string, error) { return exe, nil }
	e.app.LatestVersion = func(context.Context) (string, error) { return "9.9.9", nil }
	e.app.Now = func() time.Time { return time.Now() }
	releaseFor(t, e, "9.9.9", "novo")

	code := e.run("update")

	if code != 0 || !strings.Contains(e.out.String(), "jatai atualizada para 9.9.9.") {
		t.Fatalf("code = %d, out = %q, err = %q", code, e.out, e.err)
	}
	if strings.Contains(e.out.String(), "Nova versão") {
		t.Fatalf("notice after update: %q", e.out)
	}
	if data, _ := os.ReadFile(exe); string(data) != "novo" {
		t.Fatalf("exe = %q", data)
	}
	state := config.LoadState(e.app.Paths)
	if state.LatestVersion != "9.9.9" || time.Since(state.LastUpdateCheck) > time.Minute {
		t.Fatalf("state = %+v", state)
	}
}

// releaseFor publica, no servidor falso, o pacote e o checksums.txt de uma versão.
func releaseFor(t *testing.T, e *testEnv, version, binary string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "jatai", Mode: 0o755, Size: int64(len(binary))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(binary)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	name := update.ArchiveName(version, runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(buf.Bytes())
	e.handle("/v"+version+"/"+name, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(buf.Bytes()) })
	e.handle("/v"+version+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	})
	e.app.DownloadBase = e.vars["JATAI_API_URL"]
}
