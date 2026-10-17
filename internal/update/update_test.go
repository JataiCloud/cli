package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetectMethod(t *testing.T) {
	cases := map[string]Method{
		"/opt/homebrew/Caskroom/jatai/2.0.0/jatai":  MethodHomebrew,
		"/usr/local/Cellar/jatai/2.0.0/bin/jatai":   MethodHomebrew,
		`C:\Users\b\scoop\apps\jatai\current\jatai`: MethodScoop,
		"/home/b/.jatai/bin/jatai":                  MethodScript,
		"/usr/local/bin/jatai":                      MethodUnknown,

		"/usr/local/lib/node_modules/@jataicloud/cli-darwin-arm64/bin/jatai":                        MethodNpm,
		`C:\Users\b\AppData\Roaming\npm\node_modules\@jataicloud\cli-win32-x64\bin\jatai.exe`:       MethodNpm,
		"/home/b/.npm/_npx/1a2b/node_modules/@jataicloud/cli-linux-x64/bin/jatai":                   MethodNpx,
		"/opt/homebrew/lib/node_modules/@jataicloud/cli-darwin-arm64/bin/jatai":                     MethodNpm,
		`C:\Users\b\scoop\apps\nodejs\current\node_modules\@jataicloud\cli-win32-x64\bin\jatai.exe`: MethodNpm,
	}
	for path, want := range cases {
		if got := DetectMethod(path, "/home/b/.jatai/bin"); got != want {
			t.Errorf("%s: got %v, want %v", path, got, want)
		}
	}
}

func TestHintForNpmInstalls(t *testing.T) {
	if got := Hint(MethodNpm); got != "npm update -g jatai" {
		t.Errorf("npm hint = %q", got)
	}
	if got := Hint(MethodNpx); got != "npx -y jatai@latest" {
		t.Errorf("npx hint = %q", got)
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"2.1.0", "2.0.9", true}, {"2.10.0", "2.9.0", true}, {"2.0.0", "2.0.0", false},
		{"1.9.9", "2.0.0", false}, {"2.1.0", "dev", false}, {"v2.1.0", "2.0.0", true},
	}
	for _, c := range cases {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.latest, c.current, got)
		}
	}
}

func tarGz(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func releaseServer(t *testing.T, archive []byte, checksum string) *httptest.Server {
	t.Helper()
	name := ArchiveName("2.1.0", runtime.GOOS, runtime.GOARCH)
	mux := http.NewServeMux()
	mux.HandleFunc("/v2.1.0/"+name, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) })
	mux.HandleFunc("/v2.1.0/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", checksum, name)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestInstallReplacesTheBinaryAfterCheckingTheChecksum(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pacote tar.gz; o zip do Windows é coberto pelo extract")
	}
	archive := tarGz(t, "jatai", "binario-novo")
	sum := sha256.Sum256(archive)
	srv := releaseServer(t, archive, hex.EncodeToString(sum[:]))
	exe := filepath.Join(t.TempDir(), "jatai")
	if err := os.WriteFile(exe, []byte("binario-velho"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Install(context.Background(), srv.Client(), srv.URL, "2.1.0", exe); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(exe); string(data) != "binario-novo" {
		t.Fatalf("exe = %q", data)
	}
}

func TestInstallRefusesAWrongChecksum(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pacote tar.gz")
	}
	srv := releaseServer(t, tarGz(t, "jatai", "adulterado"), "00")
	exe := filepath.Join(t.TempDir(), "jatai")
	if err := os.WriteFile(exe, []byte("binario-velho"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Install(context.Background(), srv.Client(), srv.URL, "2.1.0", exe); err == nil {
		t.Fatal("expected checksum error")
	}
	if data, _ := os.ReadFile(exe); string(data) != "binario-velho" {
		t.Fatalf("exe changed: %q", data)
	}
}
