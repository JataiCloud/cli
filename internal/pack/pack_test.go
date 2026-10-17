package pack

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func entries(t *testing.T, archive string) map[string]*tar.Header {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string]*tar.Header{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = h
	}
}

func TestPackAppliesDefaultIgnoresAtAnyDepth(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "app.rb", "puts 1", 0o644)
	write(t, dir, "node_modules/a.js", "x", 0o644)
	write(t, dir, "packages/web/node_modules/b.js", "x", 0o644)
	write(t, dir, "packages/web/index.js", "x", 0o644)
	write(t, dir, ".env", "SECRET=1", 0o644)
	write(t, dir, "config/.env.local", "SECRET=1", 0o644)
	write(t, dir, "certs/server.pem", "x", 0o644)
	write(t, dir, "log/dev.log", "x", 0o644)

	res, err := Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(res.Path)
	got := entries(t, res.Path)

	for _, want := range []string{"app.rb", "packages/web/index.js"} {
		if got[want] == nil {
			t.Errorf("missing %s", want)
		}
	}
	for _, name := range []string{"node_modules/a.js", "packages/web/node_modules/b.js", ".env", "config/.env.local", "certs/server.pem", "log/dev.log"} {
		if got[name] != nil {
			t.Errorf("%s should be ignored", name)
		}
	}
}

func TestPackHonorsDockerignoreNegation(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".dockerignore", "docs\n!docs/keep.md\n", 0o644)
	write(t, dir, "docs/drop.md", "x", 0o644)
	write(t, dir, "docs/keep.md", "x", 0o644)
	write(t, dir, "node_modules/lib.js", "x", 0o644)

	res, err := Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(res.Path)
	got := entries(t, res.Path)
	if got["docs/keep.md"] == nil || got["docs/drop.md"] != nil || got["node_modules/lib.js"] != nil {
		t.Fatalf("entries = %v", slices.Collect(mapsKeys(got)))
	}
}

func TestPackReincludesWildcardNegationInsideIgnoredDir(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".dockerignore", "docs\n!**/keep.md\n", 0o644)
	write(t, dir, "docs/drop.md", "x", 0o644)
	write(t, dir, "docs/keep.md", "x", 0o644)

	res, err := Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(res.Path)
	got := entries(t, res.Path)
	if got["docs/keep.md"] == nil || got["docs/drop.md"] != nil {
		t.Fatalf("entries = %v", slices.Collect(mapsKeys(got)))
	}
}

func TestPackWildcardNegationDoesNotReopenDefaultIgnores(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".dockerignore", "*\n!**/package.json\n", 0o644)
	write(t, dir, "package.json", "{}", 0o644)
	write(t, dir, "packages/web/package.json", "{}", 0o644)
	write(t, dir, "node_modules/a/package.json", "{}", 0o644)
	write(t, dir, "packages/web/node_modules/b/package.json", "{}", 0o644)
	write(t, dir, ".git/x/package.json", "{}", 0o644)
	write(t, dir, ".next/cache/package.json", "{}", 0o644)

	res, err := Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(res.Path)
	got := entries(t, res.Path)
	for _, want := range []string{"package.json", "packages/web/package.json"} {
		if got[want] == nil {
			t.Errorf("missing %s", want)
		}
	}
	for name := range got {
		for _, seg := range strings.Split(name, "/") {
			if seg == "node_modules" || seg == ".git" || seg == ".next" {
				t.Errorf("%s should be ignored", name)
			}
		}
	}
}

func TestPackLiteralNegationReopensFileInsideDefaultIgnoredDir(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".dockerignore", "*\n!node_modules/keep/x.txt\n", 0o644)
	write(t, dir, "node_modules/keep/x.txt", "x", 0o644)
	write(t, dir, "node_modules/keep/y.txt", "x", 0o644)
	write(t, dir, "node_modules/drop.js", "x", 0o644)

	res, err := Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(res.Path)
	got := entries(t, res.Path)
	if got["node_modules/keep/x.txt"] == nil {
		t.Error("missing node_modules/keep/x.txt")
	}
	if got["node_modules/keep/y.txt"] != nil || got["node_modules/drop.js"] != nil {
		t.Errorf("entries = %v", slices.Collect(mapsKeys(got)))
	}
}

func TestMayReachInsideNormalizesSeparators(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		rel     string
		want    bool
	}{
		{"slash form reaches parent dir", "docs/keep.md", "docs", true},
		{"backslash form reaches parent dir", `docs\keep.md`, "docs", true},
		{"backslash form elsewhere does not reach", `docs\keep.md`, "other", false},
		{"backslash wildcard reaches any dir", `**\keep.md`, "docs", true},
		{"backslash glob in last segment reaches dir", `docs\*.md`, "docs", true},
		{"backslash ancestor reaches descendant", `a\b`, "a/b/c", true},
		{"sibling with shared prefix does not reach", "ab/cd", "abc", false},
		{"literal prefix into default dir", "node_modules/keep/x.txt", "node_modules", true},
		{"backslash literal prefix into default dir", `node_modules\keep\x.txt`, "node_modules", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mayReachInside(tc.pattern, tc.rel); got != tc.want {
				t.Errorf("mayReachInside(%q, %q) = %v, want %v", tc.pattern, tc.rel, got, tc.want)
			}
		})
	}
}

func TestMayReopenDefaultIgnoredRequiresLiteralPrefix(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		rel     string
		want    bool
	}{
		{"wildcard prefix never reopens", "**/package.json", "node_modules", false},
		{"glob first segment never reopens", "*/package.json", "node_modules", false},
		{"literal prefix reopens its dir", "node_modules/keep/x.txt", "node_modules", true},
		{"literal prefix in other dir does not reopen", "node_modules/keep/x.txt", ".git", false},
		{"backslash literal prefix reopens its dir", `.next\cache\keep.md`, ".next", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mayReopenDefaultIgnored(tc.pattern, tc.rel); got != tc.want {
				t.Errorf("mayReopenDefaultIgnored(%q, %q) = %v, want %v", tc.pattern, tc.rel, got, tc.want)
			}
		})
	}
}

func TestPackKeepsSymlinksAndExecutableBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink e bit de execução são POSIX")
	}
	dir := t.TempDir()
	write(t, dir, "bin/start", "#!/bin/sh", 0o755)
	os.Symlink("bin/start", filepath.Join(dir, "start"))

	res, err := Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(res.Path)
	got := entries(t, res.Path)
	if got["bin/start"].Mode&0o111 == 0 {
		t.Error("lost the executable bit")
	}
	if got["start"].Typeflag != tar.TypeSymlink || got["start"].Linkname != "bin/start" {
		t.Errorf("symlink = %+v", got["start"])
	}
}

func TestPackRefusesArchivesOverTheLimit(t *testing.T) {
	previous := MaxArchiveSize
	MaxArchiveSize = 10
	defer func() { MaxArchiveSize = previous }()

	dir := t.TempDir()
	write(t, dir, "big.txt", "conteúdo que comprimido passa de 10 bytes", 0o644)

	res, err := Pack(dir)
	if !errors.Is(err, ErrTooLarge) || res.Size <= 10 || res.Path != "" {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
}

func TestPackSkipsNextBuildOutput(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", "{}", 0o644)
	write(t, dir, ".next/cache/big.pack", "x", 0o644)
	write(t, dir, "app/page.tsx", "x", 0o644)

	res, err := Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(res.Path)
	got := entries(t, res.Path)
	if got["app/page.tsx"] == nil {
		t.Error("missing app/page.tsx")
	}
	if got[".next/cache/big.pack"] != nil {
		t.Error(".next/cache/big.pack should be ignored")
	}
}

func mapsKeys(m map[string]*tar.Header) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func TestPackSkipsTheJataiCredentialsDirButKeepsTheProjectFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", "{}", 0o644)
	write(t, dir, ".jatai.yml", "app: loja\n", 0o644)
	write(t, dir, ".jatai/credentials.json", `{"token":"x"}`, 0o600)
	write(t, dir, "sub/.jatai/state.json", "{}", 0o600)

	res, err := Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(res.Path)
	got := entries(t, res.Path)
	if got[".jatai.yml"] == nil {
		t.Error("missing .jatai.yml")
	}
	for _, name := range []string{".jatai/credentials.json", "sub/.jatai/state.json"} {
		if got[name] != nil {
			t.Errorf("%s should be ignored", name)
		}
	}
}
