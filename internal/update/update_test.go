package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	if got := Hint(MethodNpm); got != "npm update -g jatai-cli" {
		t.Errorf("npm hint = %q", got)
	}
	if got := Hint(MethodNpx); got != "npx -y jatai-cli@latest" {
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
		{"2.0.0", "2.0.0-rc.3", true}, {"2.0.0-rc.3", "2.0.0", false}, {"2.0.0-rc.10", "2.0.0-rc.9", true},
		{"2.0.0-rc.9", "2.0.0-rc.10", false}, {"2.0.0-rc.3", "2.0.0-rc.3", false}, {"2.0.0-rc.1", "1.9.9", true},
		{"2.0.0-rc", "2.0.0-beta.5", true}, {"2.0.0-rc.1", "2.0.0-rc", true}, {"2.0.0-rc.1", "2.0.0-1", true},
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

type tarEntry struct {
	name     string
	typeflag byte
	content  string
}

func tarGzEntries(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typeflag, Mode: 0o755}
		switch e.typeflag {
		case tar.TypeReg:
			h.Size = int64(len(e.content))
		case tar.TypeSymlink:
			h.Linkname = "/bin/sh"
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			if _, err := tw.Write([]byte(e.content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractReturnsTheBinaryFromATarGz(t *testing.T) {
	archive := tarGzEntries(t,
		tarEntry{"README.md", tar.TypeReg, "leia-me"},
		tarEntry{"jatai", tar.TypeReg, "binario"})

	got, err := extract(archive, "linux")

	if err != nil || string(got) != "binario" {
		t.Fatalf("got %q, err %v", got, err)
	}
}

func TestExtractIgnoresEntriesOutsideTheArchiveRoot(t *testing.T) {
	archive := tarGzEntries(t,
		tarEntry{"../jatai", tar.TypeReg, "fora"},
		tarEntry{"sub/jatai", tar.TypeReg, "subpasta"})

	_, err := extract(archive, "linux")

	if err == nil || !strings.Contains(err.Error(), "não encontrado no pacote") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtractRejectsNonRegularEntriesNamedLikeTheBinary(t *testing.T) {
	for name, typeflag := range map[string]byte{"directory": tar.TypeDir, "symlink": tar.TypeSymlink} {
		archive := tarGzEntries(t, tarEntry{"jatai", typeflag, ""})
		if _, err := extract(archive, "linux"); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestExtractRejectsAnEmptyBinary(t *testing.T) {
	archive := tarGzEntries(t, tarEntry{"jatai", tar.TypeReg, ""})

	if _, err := extract(archive, "linux"); err == nil {
		t.Fatal("expected error for an empty binary")
	}
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractReturnsTheBinaryFromAZip(t *testing.T) {
	archive := zipOf(t, map[string]string{"LICENSE": "x", "jatai.exe": "binario-win"})

	got, err := extract(archive, "windows")

	if err != nil || string(got) != "binario-win" {
		t.Fatalf("got %q, err %v", got, err)
	}
}

func TestExtractFailsWhenTheZipHasNoBinary(t *testing.T) {
	archive := zipOf(t, map[string]string{"sub/jatai.exe": "x", "../jatai.exe": "y"})

	_, err := extract(archive, "windows")

	if err == nil || !strings.Contains(err.Error(), "jatai.exe não encontrado no pacote") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtractFailsWhenTheTarHasNoBinary(t *testing.T) {
	_, err := extract(tarGzEntries(t, tarEntry{"outro", tar.TypeReg, "x"}), "linux")

	if err == nil || !strings.Contains(err.Error(), "binário jatai não encontrado no pacote") {
		t.Fatalf("err = %v", err)
	}
}

func TestReplaceWindowsStyleRestoresTheOldBinaryWhenTheSecondRenameFails(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "jatai.exe")
	if err := os.WriteFile(exe, []byte("velho"), 0o755); err != nil {
		t.Fatal(err)
	}
	rename := func(from, to string) error {
		if from == exe+".new" && to == exe {
			return errors.New("arquivo em uso")
		}
		return os.Rename(from, to)
	}

	if err := replaceWindowsStyle(exe, []byte("novo"), rename); err == nil {
		t.Fatal("expected error")
	}

	if data, _ := os.ReadFile(exe); string(data) != "velho" {
		t.Fatalf("exe = %q", data)
	}
	for _, leftover := range []string{exe + ".new", exe + ".old"} {
		if _, err := os.Stat(leftover); !os.IsNotExist(err) {
			t.Errorf("%s should not exist (err %v)", leftover, err)
		}
	}
}

func TestReplaceWindowsStyleCleansUpWhenTheFirstRenameFails(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "jatai.exe")
	if err := os.WriteFile(exe, []byte("velho"), 0o755); err != nil {
		t.Fatal(err)
	}
	rename := func(string, string) error { return errors.New("negado") }

	if err := replaceWindowsStyle(exe, []byte("novo"), rename); err == nil {
		t.Fatal("expected error")
	}

	if data, _ := os.ReadFile(exe); string(data) != "velho" {
		t.Fatalf("exe = %q", data)
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Fatalf(".new left behind (err %v)", err)
	}
}

func TestReplaceWindowsStyleSwapsAndDropsAStaleOldFile(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "jatai.exe")
	for path, content := range map[string]string{exe: "velho", exe + ".old": "muito-velho"} {
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := replaceWindowsStyle(exe, []byte("novo"), os.Rename); err != nil {
		t.Fatal(err)
	}

	if data, _ := os.ReadFile(exe); string(data) != "novo" {
		t.Fatalf("exe = %q", data)
	}
	if data, _ := os.ReadFile(exe + ".old"); string(data) != "velho" {
		t.Fatalf(".old = %q", data)
	}
}

func TestLatestFailsWithoutATagName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name": ""}`))
	}))
	t.Cleanup(srv.Close)

	if v, err := Latest(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatalf("expected error, got %q", v)
	}
}
