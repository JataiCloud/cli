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
