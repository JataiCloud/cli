// Package pack gera o .tar.gz do código que vai para o build — sem depender
// do `tar` do sistema.
package pack

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

// MaxArchiveSize é o limite do servidor (Deployment::SourceArchiver).
var MaxArchiveSize int64 = 500 << 20

var ErrTooLarge = errors.New("pacote acima do limite do servidor")

// Mesma lista da gem 1.5.0. O `**/` reproduz o `tar --exclude` antigo, que
// casava o nome em qualquer nível (node_modules de workspaces, por exemplo).
// `.next`, `.vercel` e `.turbo` são saída de build/cache local: o servidor
// refaz o build, e o `.next/cache` de quem rodou `npm run dev` passa de
// centenas de MB. O .dockerignore do projeto entra depois, com a semântica
// do Docker — e pode reincluir algo com `!`.
var defaultIgnores = []string{
	"**/.git", "**/node_modules", "**/tmp", "**/log", "**/.bundle", "**/vendor/bundle",
	"**/.env", "**/.env.*", "**/*.log", "**/coverage",
	"**/.next", "**/.vercel", "**/.turbo",
	"config/master.key", "config/credentials.yml.enc", "config/credentials", "config/credentials/*.yml.enc",
	"**/*.pem", "**/*.key", "storage/*.key",
}

type Result struct {
	Path string
	Size int64
}

func Pack(dir string) (Result, error) {
	patterns, err := ignorePatterns(dir)
	if err != nil {
		return Result{}, err
	}
	pm, err := patternmatcher.New(patterns)
	if err != nil {
		return Result{}, fmt.Errorf(".dockerignore inválido: %w", err)
	}

	out, err := os.CreateTemp("", "jatai-source-*.tar.gz")
	if err != nil {
		return Result{}, err
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		ignored, err := pm.MatchesOrParentMatches(rel)
		if err != nil {
			return err
		}
		if ignored {
			if d.IsDir() && skipDir(pm, rel) {
				return filepath.SkipDir
			}
			return nil
		}
		return addEntry(tw, path, rel, d)
	})
	closeErr := errors.Join(tw.Close(), gz.Close(), out.Close())
	if err := errors.Join(walkErr, closeErr); err != nil {
		os.Remove(out.Name())
		return Result{}, fmt.Errorf("falha ao empacotar código: %w", err)
	}

	info, err := os.Stat(out.Name())
	if err != nil {
		return Result{}, err
	}
	if info.Size() > MaxArchiveSize {
		os.Remove(out.Name())
		return Result{Size: info.Size()}, ErrTooLarge
	}
	return Result{Path: out.Name(), Size: info.Size()}, nil
}

// Pasta ignorada só é visitada se alguma negação (`!pasta/arquivo`) aponta
// para dentro dela — a mesma regra do Docker ao montar o contexto de build.
// Sem isso, um único `!` no .dockerignore faria percorrer node_modules inteiro.
func skipDir(pm *patternmatcher.PatternMatcher, rel string) bool {
	for _, p := range pm.Patterns() {
		if p.Exclusion() && mayReachInside(p.String(), rel) {
			return false
		}
	}
	return true
}

// Conservador: a negação pode reincluir algo dentro de `rel` se o trecho
// literal inicial dela é o próprio `rel`, um descendente ou um ancestral. Um
// padrão que começa com curinga (`!**/keep.md`) pode casar em qualquer pasta.
func mayReachInside(pattern, rel string) bool {
	var literal []string
	for _, seg := range strings.Split(pattern, "/") {
		if strings.ContainsAny(seg, "*?[") {
			break
		}
		literal = append(literal, seg)
	}
	prefix := strings.Join(literal, "/")
	if prefix == "" {
		return true
	}
	return prefix == rel || strings.HasPrefix(prefix, rel+"/") || strings.HasPrefix(rel, prefix+"/")
}

func ignorePatterns(dir string) ([]string, error) {
	patterns := append([]string{}, defaultIgnores...)
	f, err := os.Open(filepath.Join(dir, ".dockerignore"))
	if errors.Is(err, fs.ErrNotExist) {
		return patterns, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	extra, err := ignorefile.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf(".dockerignore inválido: %w", err)
	}
	return append(patterns, extra...), nil
}

func addEntry(tw *tar.Writer, path, rel string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return err
	}
	link := ""
	if info.Mode()&fs.ModeSymlink != 0 {
		if link, err = os.Readlink(path); err != nil {
			return err
		}
	}
	hdr, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return err
	}
	hdr.Name = rel
	if info.IsDir() {
		hdr.Name += "/"
	}
	// No Windows não há bit de execução: como o Docker faz com contextos
	// vindos do Windows, tudo vai como 0755 e os scripts continuam executáveis.
	if runtime.GOOS == "windows" && hdr.Typeflag != tar.TypeSymlink {
		hdr.Mode = 0o755
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}
