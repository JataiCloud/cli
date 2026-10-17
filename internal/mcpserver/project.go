package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jataicloud/cli/internal/api"
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
	p, path, fresh, err := s.draftProject(dir, in.Nome, in.Stack)
	if err != nil {
		return toolError(err.Error())
	}
	guess, err := projectSlug(p)
	if err != nil {
		return toolError(err.Error())
	}
	cctx, cancel := context.WithTimeout(ctx, s.d.Budget)
	defer cancel()
	if fresh {
		if path, err = s.claimSlug(cctx, c, dir, p, guess); err != nil {
			return toolError(err.Error())
		}
	}
	p.Branch = s.branchFor(dir, p) // repo_branch pela mesma regra do jatai_publicar
	r := s.runner(c, nil)
	var app deployflow.App
	created := fresh
	if fresh {
		app, err = r.CreateApp(cctx, dir, hexBase(p), path)
	} else {
		app, created, err = r.EnsureApp(cctx, dir, hexBase(p), path, guess)
	}
	if err != nil {
		return toolError(s.explain(err))
	}
	stack := p.Stack
	if stack == "" {
		stack = app.Stack
	}
	if !created {
		return textResult(fmt.Sprintf("O app %s já existe no Jataí (stack %s, status %s). Próximo passo: jatai_publicar.",
			app.Slug, stackLabel(stack), app.Status))
	}
	text := fmt.Sprintf("App %s criado no Jataí (stack %s, Hex Base).", app.Slug, stackLabel(stack))
	if stack != "static" {
		text += " O PG Base e o DATABASE_URL entram no primeiro jatai_publicar."
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

// draftProject lê o .jatai.yml da pasta ou, sem ele, monta um como o `jatai
// init` faria sem perguntas: nome (padrão: o da pasta), stack (padrão: a
// detectada) e a branch do git. O rascunho (fresh) ainda não está gravado:
// quem chama confere antes, com claimSlug, que o slug não é de outro app.
// Um arquivo existente é reaproveitado sem sobrescrever nada — ele é do cliente.
func (s *state) draftProject(dir, name, stack string) (p config.Project, path string, fresh bool, err error) {
	p, path, err = config.LoadProject(dir)
	switch {
	case err == nil && strings.TrimSpace(p.App) != "":
		return p, path, false, nil
	case err == nil:
		return p, path, false, fmt.Errorf("O %s desta pasta não tem `app`. Preencha `app` com o identificador do app "+
			"ou apague o arquivo e chame jatai_criar_app.", filepath.Base(path))
	case !errors.Is(err, config.ErrNoProject):
		return p, path, false, unreadableProject(path, err)
	}
	stack = strings.ToLower(strings.TrimSpace(stack))
	switch {
	case stack != "" && !slices.Contains(stacks, stack):
		return p, path, false, fmt.Errorf("Stack %q desconhecida. Informe em `stack` uma destas: %s.", stack, stackList)
	case stack == "":
		if stack = config.DetectStack(dir); stack == "" {
			return p, path, false, fmt.Errorf("Não consegui detectar a stack de %s. Chame de novo informando `stack`: "+
				"%s.", dir, stackList)
		}
	}
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(dir)
	}
	p, err = config.NewProject(name, stack, s.gitBranch(dir))
	if errors.Is(err, config.ErrNoUsableName) {
		return p, path, false, fmt.Errorf("Não consegui gerar um identificador a partir de %q. Informe `nome` com "+
			"letras ou números.", strings.TrimSpace(name))
	}
	if err != nil {
		return p, path, false, err
	}
	return p, path, true, nil
}

// claimSlug é a guarda da pasta nova (sem .jatai.yml): se o slug derivado do
// nome já é de um app do time, nada é gravado nem publicado — o my-app do
// create-next-app não pode cair em silêncio por cima do exercício anterior.
// Livre (404), grava o .jatai.yml e devolve o caminho dele.
func (s *state) claimSlug(ctx context.Context, c *api.Client, dir string, p config.Project, guess string) (string, error) {
	var existing deployflow.App
	err := c.Get(ctx, "/api/v1/apps/"+guess, nil, &existing)
	switch {
	case err == nil:
		return "", homonymError(guess)
	case !api.IsStatus(err, http.StatusNotFound):
		return "", errors.New(s.explain(err))
	}
	if err := config.WriteProject(dir, p); err != nil {
		return "", fmt.Errorf("Não consegui gravar o %s em %s: %v", config.ProjectFile, dir, err)
	}
	return filepath.Join(dir, config.ProjectFile), nil
}

func homonymError(slug string) error {
	return fmt.Errorf("Já existe no seu time um app %s, e esta pasta ainda não tem .jatai.yml: nada foi gravado, "+
		"criado ou publicado. Pergunte ao usuário o que ele quer. Para publicar esta pasta nesse app de propósito (substituindo "+
		"o que está no ar), chame jatai_publicar com app: \"%s\". Para criar um app novo, chame jatai_criar_app "+
		"com `nome` diferente (por exemplo, o nome do exercício).", slug, slug)
}

func unreadableProject(path string, err error) error {
	return fmt.Errorf("Não consegui ler o %s (%v). Corrija ou apague o arquivo e chame jatai_criar_app.",
		filepath.Base(path), err)
}

// gitBranch só chama o git dentro de um repositório: num Mac sem as Command
// Line Tools, qualquer `git` abre a janela de instalação na frente do aluno.
func (s *state) gitBranch(dir string) string {
	if s.d.Git == nil || !config.IsGitRepo(dir) {
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
