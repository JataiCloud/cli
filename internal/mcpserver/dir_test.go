package mcpserver

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type probeInput struct {
	Pasta string `json:"pasta,omitempty" jsonschema:"pasta"`
}

// probeDir registra uma ferramenta de teste que só devolve o resolveDir.
func probeDir(m *mcpTest) {
	m.setup = func(st *state, srv *mcp.Server) {
		addTool(srv, st, "probe", "Ferramenta de teste que devolve a pasta resolvida.",
			func(ctx context.Context, req *mcp.CallToolRequest, in probeInput) *mcp.CallToolResult {
				dir, err := st.resolveDir(ctx, req, in.Pasta)
				if err != nil {
					return toolError(err.Error())
				}
				return textResult(dir)
			})
	}
}

func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func TestResolveDirUsesTheGivenFolder(t *testing.T) {
	m := newMCPTest(t)
	probeDir(m)
	project := t.TempDir()

	if got, isErr := m.call("probe", map[string]any{"pasta": project}); isErr || got != project {
		t.Fatalf("%v %q", isErr, got)
	}
}

func TestResolveDirMakesARelativeFolderAbsolute(t *testing.T) {
	m := newMCPTest(t)
	probeDir(m)
	os.Mkdir(filepath.Join(m.dir, "web"), 0o755)

	if got, isErr := m.call("probe", map[string]any{"pasta": "web"}); isErr || got != filepath.Join(m.dir, "web") {
		t.Fatalf("%v %q", isErr, got)
	}
}

func TestResolveDirPrefersTheSingleClientRoot(t *testing.T) {
	m := newMCPTest(t)
	probeDir(m)
	m.protocol = "2025-06-18"
	root := t.TempDir()
	m.roots = []string{fileURI(root)}

	if got, isErr := m.call("probe", nil); isErr || got != root {
		t.Fatalf("%v %q", isErr, got)
	}
}

func TestResolveDirIgnoresSeveralRootsAndUsesTheProcessFolder(t *testing.T) {
	m := newMCPTest(t)
	probeDir(m)
	m.protocol = "2025-06-18"
	m.roots = []string{fileURI(t.TempDir()), fileURI(t.TempDir())}

	if got, isErr := m.call("probe", nil); isErr || got != m.dir {
		t.Fatalf("%v %q", isErr, got)
	}
}

// No protocolo 2026-07-28 o servidor não pode mais pedir as raízes no meio
// de uma chamada: vale o diretório do processo, sem erro.
func TestResolveDirFallsBackToTheProcessFolderWhenRootsAreNotAllowed(t *testing.T) {
	m := newMCPTest(t)
	probeDir(m)
	m.roots = []string{fileURI(t.TempDir())}

	if got, isErr := m.call("probe", nil); isErr || got != m.dir {
		t.Fatalf("%v %q", isErr, got)
	}
}

func TestResolveDirRefusesHomeRootAndMissingFolders(t *testing.T) {
	m := newMCPTest(t)
	probeDir(m)
	missing := filepath.Join(m.dir, "nao-existe")
	for _, pasta := range []string{m.deps.Paths.Home, string(filepath.Separator), missing} {
		got, isErr := m.call("probe", map[string]any{"pasta": pasta})
		if !isErr {
			t.Fatalf("%q accepted: %q", pasta, got)
		}
		assertContains(t, got, "Informe `pasta` com o caminho absoluto da pasta do projeto (recebi "+filepath.Clean(pasta)+").")
	}
}

func TestResolveDirRefusesTheHomeWhenTheProcessRunsThere(t *testing.T) {
	m := newMCPTest(t)
	probeDir(m)
	m.dir = m.deps.Paths.Home

	if got, isErr := m.call("probe", nil); !isErr {
		t.Fatalf("home accepted: %q", got)
	}
}
