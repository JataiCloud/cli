package mcpserver

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// rootsTimeout limita a pergunta das raízes: um cliente que anuncia a
// capacidade e não responde não pode prender a ferramenta.
const rootsTimeout = 2 * time.Second

// resolveDir escolhe a pasta do projeto: `pasta` (relativa vira absoluta sobre
// o diretório do processo; `~/` vira a home) → a única raiz `file://` que o
// cliente expõe → o diretório do processo. Recusa a home, a raiz do sistema e
// o que não é uma pasta existente: publicar a home inteira seria um desastre.
func (s *state) resolveDir(ctx context.Context, req *mcp.CallToolRequest, pasta string) (string, error) {
	dir, err := s.candidateDir(ctx, req, strings.TrimSpace(pasta))
	if err != nil {
		return "", err
	}
	dir = filepath.Clean(dir)
	if !s.usableDir(dir) {
		return "", fmt.Errorf("Informe `pasta` com o caminho absoluto da pasta do projeto (recebi %s).", dir)
	}
	return dir, nil
}

func (s *state) candidateDir(ctx context.Context, req *mcp.CallToolRequest, pasta string) (string, error) {
	if pasta == "" {
		if root, ok := s.singleRoot(ctx, req); ok {
			return root, nil
		}
		return s.d.Getwd()
	}
	if home := s.d.Paths.Home; home != "" && (pasta == "~" || strings.HasPrefix(pasta, "~/")) {
		return filepath.Join(home, strings.TrimPrefix(pasta, "~")), nil
	}
	if filepath.IsAbs(pasta) {
		return pasta, nil
	}
	wd, err := s.d.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(wd, pasta), nil
}

func (s *state) usableDir(dir string) bool {
	if !filepath.IsAbs(dir) || filepath.Dir(dir) == dir {
		return false
	}
	if home := s.d.Paths.Home; home != "" && samePath(dir, home) {
		return false
	}
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// singleRoot devolve a raiz do cliente quando há exatamente uma, local.
// As raízes estão em desuso no protocolo 2026-07-28 (lá o servidor nem pode
// pedi-las no meio de uma chamada, e o ListRoots falha na hora, sem ida ao
// cliente), mas os agentes em versões anteriores ainda as mandam. Sem elas,
// vale o diretório do processo.
func (s *state) singleRoot(ctx context.Context, req *mcp.CallToolRequest) (string, bool) {
	if req == nil || req.Session == nil {
		return "", false
	}
	caps := req.ClientCapabilities()
	if caps == nil || caps.RootsV2 == nil { //nolint:staticcheck // ver o comentário da função
		return "", false
	}
	rctx, cancel := context.WithTimeout(ctx, rootsTimeout)
	defer cancel()
	res, err := req.Session.ListRoots(rctx, nil) //nolint:staticcheck // ver o comentário da função
	if err != nil || len(res.Roots) != 1 {
		return "", false
	}
	return fileURIPath(res.Roots[0].URI)
}

func fileURIPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || u.Path == "" {
		return "", false
	}
	path := u.Path
	// file:///C:/proj → /C:/proj no Windows.
	if runtime.GOOS == "windows" && len(path) > 2 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path), true
}
