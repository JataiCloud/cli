package mcpserver

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"

	"github.com/jataicloud/cli/internal/api"
	"github.com/jataicloud/cli/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Instructions vão ao cliente no initialize: o fluxo e o que fica no painel.
const Instructions = "Publica projetos web da pasta local no Jataí (nuvem brasileira). " +
	"Fluxo: jatai_conta → (se preciso) jatai_login → jatai_criar_app → jatai_publicar → jatai_status/jatai_logs. " +
	"Sempre passe `pasta` com o caminho absoluto do projeto. " +
	"Variáveis de ambiente valem no próximo deploy; os valores nunca são mostrados. " +
	"Next.js: as variáveis só existem em execução, não no build — página que lê o banco não pode ser pré-renderizada. " +
	"Com `cacheComponents` (padrão do create-next-app 16), não exporte `dynamic`: leia o banco dentro de " +
	"`<Suspense>` ou depois de `await connection()` (de `next/server`). " +
	"Next.js 15 ou sem `cacheComponents`: `export const dynamic = \"force-dynamic\"` na página. " +
	"Apagar app ou banco, mudar plano ou escala, domínios, cobrança e comandos remotos não estão disponíveis aqui: " +
	"oriente o usuário a usar o painel em https://jataicloud.com.br/apps."

// state é o que o servidor guarda entre chamadas.
type state struct {
	d Deps

	// loginMu serializa o jatai_login: uma espera de aprovação por vez.
	loginMu sync.Mutex
	pending *pendingLogin
}

func newState(d Deps) *state { return &state{d: d.withDefaults()} }

// New monta o servidor com as nove ferramentas do curso.
func New(d Deps) *mcp.Server { return newState(d).server() }

// Serve atende um cliente pelo transporte até ele desconectar ou o ctx acabar.
func Serve(ctx context.Context, d Deps, t mcp.Transport) error {
	return New(d).Run(ctx, t)
}

func (s *state) server() *mcp.Server {
	opts := &mcp.ServerOptions{
		Instructions: Instructions,
		// Sem a capacidade "logging" padrão: o servidor não manda logs ao cliente.
		Capabilities: &mcp.ServerCapabilities{},
	}
	if s.debug() {
		opts.Logger = slog.New(slog.NewTextHandler(s.d.Log, nil))
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "jatai", Title: "Jataí", Version: s.d.Version}, opts)
	s.registerTools(srv)
	return srv
}

func (s *state) debug() bool { return s.d.Getenv("JATAI_DEBUG") == "1" }

// handlerFunc é o formato das ferramentas daqui: o resultado é sempre texto.
type handlerFunc[In any] func(ctx context.Context, req *mcp.CallToolRequest, in In) *mcp.CallToolResult

// addTool registra a ferramenta e transforma um pânico em erro da ferramenta:
// ao vivo, um bug numa ferramenta não pode derrubar o servidor inteiro.
func addTool[In any](srv *mcp.Server, s *state, name, description string, h handlerFunc[In]) {
	mcp.AddTool(srv, &mcp.Tool{Name: name, Description: description},
		func(ctx context.Context, req *mcp.CallToolRequest, in In) (res *mcp.CallToolResult, _ any, _ error) {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(s.d.Log, "jatai mcp: pânico em %s: %v\n%s", name, r, debug.Stack())
					res = toolError(fmt.Sprintf("Erro interno da CLI do Jataí em %s. Tente de novo; se repetir, "+
						"atualize a CLI.", name))
				}
			}()
			return h(ctx, req, in), nil, nil
		})
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func toolError(msg string) *mcp.CallToolResult {
	res := textResult(msg)
	res.IsError = true
	return res
}

// client lê o token a cada chamada: o jatai_login pode tê-lo trocado no meio
// da sessão.
func (s *state) client(_ context.Context) (*api.Client, error) {
	token := config.Token(s.d.Paths, s.d.Getenv)
	if token == "" {
		return nil, errNotLoggedIn
	}
	return s.newClient(token)
}

func (s *state) newClient(token string) (*api.Client, error) {
	c, err := api.New(config.APIURL(s.d.Getenv), token, s.d.UserAgent)
	if err != nil {
		return nil, err
	}
	if s.d.HTTP != nil {
		c.HTTP = s.d.HTTP
	}
	if s.debug() {
		c.Debug = s.d.Log
	}
	return c, nil
}

// Entradas: campo opcional leva omitempty (sem ele o go-sdk o marca como
// obrigatório); a descrição de cada campo vai na tag jsonschema.
type contaInput struct {
	Pasta string `json:"pasta,omitempty" jsonschema:"Pasta do projeto (opcional): diz se ela já tem app no Jataí."`
	App   string `json:"app,omitempty" jsonschema:"Não usado por esta ferramenta."`
}

type loginInput struct {
	Pasta string `json:"pasta,omitempty" jsonschema:"Não usado por esta ferramenta."`
	App   string `json:"app,omitempty" jsonschema:"Não usado por esta ferramenta."`
}

type criarAppInput struct {
	Pasta string `json:"pasta,omitempty" jsonschema:"Caminho absoluto da pasta do projeto (padrão: o diretório de trabalho do servidor)."`
	App   string `json:"app,omitempty" jsonschema:"Não usado na criação: o slug é gerado a partir de nome."`
	Nome  string `json:"nome,omitempty" jsonschema:"Nome do app (padrão: o nome da pasta); o slug é gerado a partir dele."`
	Stack string `json:"stack,omitempty" jsonschema:"rails, node, static ou docker (padrão: detectar pelos arquivos da pasta)."`
}

type publicarInput struct {
	Pasta string `json:"pasta,omitempty" jsonschema:"Caminho absoluto da pasta do projeto (padrão: o diretório de trabalho do servidor)."`
	App   string `json:"app,omitempty" jsonschema:"Slug do app no Jataí (padrão: o do .jatai.yml da pasta)."`
}

type statusInput struct {
	Pasta    string `json:"pasta,omitempty" jsonschema:"Caminho absoluto da pasta do projeto (padrão: o diretório de trabalho do servidor)."`
	App      string `json:"app,omitempty" jsonschema:"Slug do app no Jataí (padrão: o do .jatai.yml da pasta)."`
	Aguardar *bool  `json:"aguardar,omitempty" jsonschema:"Esperar o deploy em andamento terminar, por até 45 s (padrão: true)."`
}

type logsInput struct {
	Pasta  string `json:"pasta,omitempty" jsonschema:"Caminho absoluto da pasta do projeto (padrão: o diretório de trabalho do servidor)."`
	App    string `json:"app,omitempty" jsonschema:"Slug do app no Jataí (padrão: o do .jatai.yml da pasta)."`
	Filtro string `json:"filtro,omitempty" jsonschema:"Texto que a linha precisa conter (opcional)."`
	Linhas int    `json:"linhas,omitempty" jsonschema:"Quantas linhas recentes (padrão 100, máximo 500)."`
}

type listarInput struct {
	Pasta string `json:"pasta,omitempty" jsonschema:"Caminho absoluto da pasta do projeto (padrão: o diretório de trabalho do servidor)."`
	App   string `json:"app,omitempty" jsonschema:"Slug do app no Jataí (padrão: o do .jatai.yml da pasta)."`
}

type definirInput struct {
	Pasta     string            `json:"pasta,omitempty" jsonschema:"Caminho absoluto da pasta do projeto (padrão: o diretório de trabalho do servidor)."`
	App       string            `json:"app,omitempty" jsonschema:"Slug do app no Jataí (padrão: o do .jatai.yml da pasta)."`
	Variaveis map[string]string `json:"variaveis" jsonschema:"Variáveis a definir, no formato CHAVE: valor."`
	Publicar  bool              `json:"publicar,omitempty" jsonschema:"Publicar de novo logo em seguida, para os valores valerem já (padrão: false)."`
}

type removerInput struct {
	Pasta    string   `json:"pasta,omitempty" jsonschema:"Caminho absoluto da pasta do projeto (padrão: o diretório de trabalho do servidor)."`
	App      string   `json:"app,omitempty" jsonschema:"Slug do app no Jataí (padrão: o do .jatai.yml da pasta)."`
	Chaves   []string `json:"chaves" jsonschema:"Chaves das variáveis a remover."`
	Publicar bool     `json:"publicar,omitempty" jsonschema:"Publicar de novo logo em seguida, para a remoção valer já (padrão: false)."`
}

// Descrições para a IA: o que faz, quando usar, o que devolve.
const (
	contaDesc = "Mostra quem está logado no Jataí, o time, os apps existentes com status, se a cortesia " +
		"(1 Hex Base + 1 PG Base sem cartão) está livre e se há meio de pagamento. Use primeiro, antes de criar " +
		"ou publicar. Sem login, pede para chamar jatai_login."
	loginDesc = "Login no Jataí pelo navegador. 1ª chamada: devolve na hora um link e um código para o usuário " +
		"abrir e aprovar. Chamada seguinte (depois que o usuário aprovar): espera a aprovação por até 45 s e grava " +
		"a credencial nesta máquina. Use quando outra ferramenta disser que não há login."
	criarAppDesc = "Cria o app no Jataí para a pasta do projeto: detecta a stack (rails, node, static, docker), " +
		"grava o .jatai.yml e cria o app (Hex Base; com banco PG Base e DATABASE_URL quando não é static). " +
		"Use uma vez por projeto, antes de jatai_publicar. Devolve o slug do app."
	publicarDesc = "Empacota a pasta local, envia ao Jataí e acompanha build e implantação por até 45 s. Devolve a " +
		"URL publicada; ou o motivo da falha com as últimas linhas do build; ou \"em andamento\" com o número do " +
		"deploy (acompanhe com jatai_status)."
	statusDesc = "Status do app e do último deploy, com as últimas linhas do build se ele falhou. Com aguardar " +
		"(padrão), espera por até 45 s um deploy em andamento terminar. Use depois de jatai_publicar responder " +
		"\"em andamento\"."
	logsDesc = "Linhas recentes do log do app em execução, com filtro de texto opcional. Use para investigar erro " +
		"depois de publicado (página com erro 500, app reiniciando)."
	listarDesc  = "Lista as chaves das variáveis de ambiente do app. Os valores nunca são mostrados."
	definirDesc = "Define (cria ou altera) variáveis de ambiente do app. Valem no próximo deploy; use publicar=true " +
		"para publicar em seguida. A resposta lista só as chaves, nunca os valores."
	removerDesc = "Remove variáveis de ambiente do app pelas chaves. Vale no próximo deploy; use publicar=true para " +
		"publicar em seguida."
)

func (s *state) registerTools(srv *mcp.Server) {
	addTool(srv, s, "jatai_conta", contaDesc, s.account)
	addTool(srv, s, "jatai_login", loginDesc, s.login)
	addTool(srv, s, "jatai_criar_app", criarAppDesc, underConstruction[criarAppInput])
	addTool(srv, s, "jatai_publicar", publicarDesc, underConstruction[publicarInput])
	addTool(srv, s, "jatai_status", statusDesc, underConstruction[statusInput])
	addTool(srv, s, "jatai_logs", logsDesc, underConstruction[logsInput])
	addTool(srv, s, "jatai_variaveis_listar", listarDesc, underConstruction[listarInput])
	addTool(srv, s, "jatai_variaveis_definir", definirDesc, underConstruction[definirInput])
	addTool(srv, s, "jatai_variaveis_remover", removerDesc, underConstruction[removerInput])
}

// underConstruction segura o lugar das ferramentas das Tasks 23–24.
func underConstruction[In any](context.Context, *mcp.CallToolRequest, In) *mcp.CallToolResult {
	return toolError("em construção")
}
