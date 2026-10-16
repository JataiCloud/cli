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
	defaults, err := patternmatcher.New(defaultIgnores)
	if err != nil {
		return Result{}, err
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
		if !ignored {
			return addEntry(tw, path, rel, d)
		}
		if !d.IsDir() {
			return nil
		}
		descend, err := descendIgnored(pm, defaults, rel)
		if err != nil {
			return err
		}
		if !descend {
			return filepath.SkipDir
		}
		return nil
	})
	closeErr := errors.Join(tw.Close(), gz.Close(), out.Close())
	if err := errors.Join(walkErr, closeErr); err != nil {
		os.Remove(out.Name())
		return Result{}, fmt.Errorf("falha ao empacotar código: %w", err)
	}

	info, err := os.Stat(out.Name())
	if err != nil {
		os.Remove(out.Name())
		return Result{}, err
	}
	if info.Size() > MaxArchiveSize {
		os.Remove(out.Name())
		return Result{Size: info.Size()}, ErrTooLarge
	}
	return Result{Path: out.Name(), Size: info.Size()}, nil
}

// descendIgnored diz se o walk entra num diretório que o .dockerignore ignora.
// É deliberadamente mais conservador que o walker do Docker: entra sempre que
// alguma negação pode alcançar algo dentro dele, mesmo quando o Docker não
// entraria. A exceção são os diretórios cobertos pelos padrões padrão
// (node_modules, .git, .next…): só uma negação com prefixo literal que aponta
// para dentro deles os reabre. Assim, `*` + `!**/package.json` não traz
// node_modules nem .git para o pacote.
func descendIgnored(user, defaults *patternmatcher.PatternMatcher, rel string) (bool, error) {
	fromDefaults, err := defaults.MatchesOrParentMatches(rel)
	if err != nil {
		return false, err
	}
	for _, p := range user.Patterns() {
		if !p.Exclusion() {
			continue
		}
		if (fromDefaults && mayReopenDefaultIgnored(p.String(), rel)) ||
			(!fromDefaults && mayReachInside(p.String(), rel)) {
			return true, nil
		}
	}
	return false, nil
}

// negationPrefix devolve o trecho literal inicial do padrão, com barras
// normais. patternmatcher aplica filepath.Clean, então no Windows
// Pattern.String() usa barra invertida; a normalização acontece aqui para que
// o mesmo código valha em qualquer SO. Em Unix, `\` é escape no .dockerignore:
// o trecho pode ser cortado onde não devia, o que só torna a decisão mais
// conservadora.
func negationPrefix(pattern string) string {
	slashed := strings.ReplaceAll(filepath.ToSlash(pattern), `\`, "/")
	var literal []string
	for _, seg := range strings.Split(slashed, "/") {
		if strings.ContainsAny(seg, "*?[") {
			break
		}
		literal = append(literal, seg)
	}
	return strings.Join(literal, "/")
}

// reaches diz se o prefixo literal aponta para rel: é o próprio rel, um
// descendente ou um ancestral.
func reaches(prefix, rel string) bool {
	return prefix == rel || strings.HasPrefix(prefix, rel+"/") || strings.HasPrefix(rel, prefix+"/")
}

// mayReachInside: uma negação em diretório não padrão pode reincluir algo
// dentro de rel. Padrão que começa com curinga (`!**/keep.md`) pode casar em
// qualquer pasta, então conta como alcançável.
func mayReachInside(pattern, rel string) bool {
	prefix := negationPrefix(pattern)
	return prefix == "" || reaches(prefix, rel)
}

// mayReopenDefaultIgnored: dentro de um diretório padrão, só negação com
// prefixo literal não vazio reabre algo. Curinga inicial nunca reabre.
func mayReopenDefaultIgnored(pattern, rel string) bool {
	prefix := negationPrefix(pattern)
	return prefix != "" && reaches(prefix, rel)
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
