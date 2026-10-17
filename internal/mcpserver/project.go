package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jataicloud/cli/internal/config"
	"github.com/jataicloud/cli/internal/deployflow"
	"github.com/jataicloud/cli/internal/slug"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// stacks na ordem em que as mensagens as listam.
var stacks = []string{"rails", "node", "static", "docker"}

const stackList = "rails, node, static, docker"

var errNoApp = errors.New("Esta pasta ainda não tem app no Jataí. Chame jatai_criar_app.")

func (s *state) createApp(ctx context.Context, req *mcp.CallToolRequest, in criarAppInput) *mcp.CallToolResult {
	c, err := s.client(ctx)
	if err != nil {
		return toolError(s.explain(err))
	}
	dir, err := s.resolveDir(ctx, req, in.Pasta)
	if err != nil {
		return toolError(err.Error())
	}
	p, path, err := s.ensureProject(dir, in.Nome, in.Stack)
	if err != nil {
		return toolError(err.Error())
	}
	guess, err := projectSlug(p)
	if err != nil {
		return toolError(err.Error())
	}
	rep := &creationReporter{}
	cctx, cancel := context.WithTimeout(ctx, s.d.Budget)
	defer cancel()
	app, err := s.runner(c, rep).FindOrCreateApp(cctx, dir, hexBase(p), path, guess)
	if err != nil {
		return toolError(s.explain(err))
	}
	stack := p.Stack
	if stack == "" {
		stack = app.Stack
	}
	if !rep.created {
		return textResult(fmt.Sprintf("O app %s já existe no Jataí (stack %s, status %s). Próximo passo: jatai_publicar.",
			app.Slug, stackLabel(stack), app.Status))
	}
	text := fmt.Sprintf("App %s criado no Jataí (stack %s, Hex Base).", app.Slug, stackLabel(stack))
	if stack != "static" {
		text += " PG Base incluído e DATABASE_URL nas variáveis."
	}
	return textResult(text + " Próximo passo: jatai_publicar.")
}

func stackLabel(stack string) string {
	if label, ok := config.StackLabels[stack]; ok {
		return label
	}
	return stack
}

// hexBase: o MCP nunca cria nada acima do Hex Base (plano e escala ficam no
// painel), mesmo que o .jatai.yml do cliente peça outro Hex. O arquivo não muda.
func hexBase(p config.Project) config.Project {
	p.Preset = config.DefaultPreset
	return p
}

// ensureProject lê o .jatai.yml da pasta ou, sem ele, cria um como o `jatai
// init` faria sem perguntas: nome (padrão: o da pasta), stack (padrão: a
// detectada) e a branch do git. Um arquivo existente é reaproveitado sem
// sobrescrever nada — ele é do cliente.
func (s *state) ensureProject(dir, name, stack string) (config.Project, string, error) {
	p, path, err := config.LoadProject(dir)
	switch {
	case err == nil && strings.TrimSpace(p.App) != "":
		return p, path, nil
	case err == nil:
		return p, path, fmt.Errorf("O %s desta pasta não tem `app`. Preencha `app` com o identificador do app ou "+
			"apague o arquivo e chame jatai_criar_app.", filepath.Base(path))
	case !errors.Is(err, config.ErrNoProject):
		return p, path, unreadableProject(path, err)
	}
	stack = strings.ToLower(strings.TrimSpace(stack))
	switch {
	case stack != "" && !slices.Contains(stacks, stack):
		return p, path, fmt.Errorf("Stack %q desconhecida. Informe em `stack` uma destas: %s.", stack, stackList)
	case stack == "":
		if stack = config.DetectStack(dir); stack == "" {
			return p, path, fmt.Errorf("Não consegui detectar a stack de %s. Chame de novo informando `stack`: %s.",
				dir, stackList)
		}
	}
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(dir)
	}
	p, err = config.NewProject(name, stack, s.gitBranch(dir))
	if errors.Is(err, config.ErrNoUsableName) {
		return p, path, fmt.Errorf("Não consegui gerar um identificador a partir de %q. Informe `nome` com letras "+
			"ou números.", strings.TrimSpace(name))
	}
	if err != nil {
		return p, path, err
	}
	if err := config.WriteProject(dir, p); err != nil {
		return p, path, fmt.Errorf("Não consegui gravar o %s em %s: %v", config.ProjectFile, dir, err)
	}
	return p, filepath.Join(dir, config.ProjectFile), nil
}

func unreadableProject(path string, err error) error {
	return fmt.Errorf("Não consegui ler o %s (%v). Corrija ou apague o arquivo e chame jatai_criar_app.",
		filepath.Base(path), err)
}

func (s *state) gitBranch(dir string) string {
	if s.d.Git == nil {
		return ""
	}
	branch, err := s.d.Git(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	return branch
}

// projectSlug é o palpite de slug do .jatai.yml (o servidor pode ter
// desambiguado na criação; o FindOrCreateApp grava o verdadeiro).
func projectSlug(p config.Project) (string, error) {
	sl, ok := slug.From(p.App)
	if !ok {
		return "", fmt.Errorf("`app` inválido no .jatai.yml (%q). Use letras, números e hífen.", p.App)
	}
	return sl, nil
}

// appSlug escolhe o app: `app` explícito → o slug do .jatai.yml de dir →
// erro pedindo jatai_criar_app.
func (s *state) appSlug(dir, app string) (string, error) {
	if app = strings.TrimSpace(app); app != "" {
		sl, ok := slug.From(app)
		if !ok {
			return "", fmt.Errorf("`app` inválido (%q). Use o slug do app, com letras, números e hífen.", app)
		}
		return sl, nil
	}
	p, path, err := config.LoadProject(dir)
	switch {
	case errors.Is(err, config.ErrNoProject) || (err == nil && strings.TrimSpace(p.App) == ""):
		return "", errNoApp
	case err != nil:
		return "", unreadableProject(path, err)
	}
	return projectSlug(p)
}

// slugFor resolve a pasta só quando precisa dela (sem `app` explícito).
func (s *state) slugFor(ctx context.Context, req *mcp.CallToolRequest, pasta, app string) (string, error) {
	if strings.TrimSpace(app) != "" {
		return s.appSlug("", app)
	}
	dir, err := s.resolveDir(ctx, req, pasta)
	if err != nil {
		return "", err
	}
	return s.appSlug(dir, "")
}

// creationReporter só descobre se o FindOrCreateApp criou o app ou achou um
// existente; o resto do andamento não interessa ao jatai_criar_app.
type creationReporter struct{ created bool }

var _ deployflow.Reporter = (*creationReporter)(nil)

func (r *creationReporter) Info(msg string) {
	if strings.HasPrefix(strings.TrimSpace(msg), "Criando app") {
		r.created = true
	}
}
func (*creationReporter) Warn(string)                        {}
func (*creationReporter) Line(string)                        {}
func (*creationReporter) Step(string) func(string)           { return func(string) {} }
func (*creationReporter) Polled(string, bool, time.Duration) {}
