package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/jataicloud/cli/internal/api"
	"github.com/jataicloud/cli/internal/auth"
	"github.com/jataicloud/cli/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// accountInfo é o JSON de GET /api/v1/account.
type accountInfo struct {
	User struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"user"`
	Team struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	} `json:"team"`
	Apps struct {
		Count int `json:"count"`
		Limit int `json:"limit"`
	} `json:"apps"`
	Courtesy struct {
		Available bool `json:"available"`
		// App pode ser nulo com a cortesia ocupada: o app que a usou foi
		// apagado neste período de cobrança.
		App *string `json:"app"`
	} `json:"courtesy"`
	PaymentMethod bool `json:"payment_method"`
}

type appSummary struct {
	Slug   string `json:"slug"`
	Status string `json:"status"`
}

// maxListedApps limita a lista na primeira linha do jatai_conta.
const maxListedApps = 10

func (s *state) account(ctx context.Context, req *mcp.CallToolRequest, in contaInput) *mcp.CallToolResult {
	c, err := s.client(ctx)
	if err != nil {
		return toolError(s.explainAccount(err))
	}
	var info accountInfo
	if err := c.Get(ctx, "/api/v1/account", nil, &info); err != nil {
		return toolError(s.explainAccount(err))
	}
	var apps []appSummary
	if err := c.Get(ctx, "/api/v1/apps", nil, &apps); err != nil {
		return toolError(s.explainAccount(err))
	}
	lines := []string{
		fmt.Sprintf("Logado como %s · time %s · apps: %d de %d%s.",
			who(info.User.Name, info.User.Email), info.Team.Name, info.Apps.Count, info.Apps.Limit, appList(apps)),
		courtesyLine(info),
		paymentLine(info.PaymentMethod),
	}
	if in.Pasta != "" {
		lines = append(lines, s.folderLine(ctx, req, in.Pasta))
	} else {
		lines = append(lines, "Próximo passo: jatai_criar_app com a pasta do projeto (ou jatai_publicar, se a pasta já "+
			"tem .jatai.yml).")
	}
	return textResult(strings.Join(lines, "\n"))
}

func who(name, email string) string {
	if name == "" {
		return email
	}
	return name + " (" + email + ")"
}

func appList(apps []appSummary) string {
	if len(apps) == 0 {
		return ""
	}
	parts := make([]string, 0, min(len(apps), maxListedApps)+1)
	for i, app := range apps {
		if i == maxListedApps {
			parts = append(parts, fmt.Sprintf("e mais %d", len(apps)-maxListedApps))
			break
		}
		parts = append(parts, app.Slug+": "+app.Status)
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func courtesyLine(info accountInfo) string {
	switch {
	case info.Courtesy.Available:
		return "Cortesia: livre — " + courtesy + "."
	case info.Courtesy.App != nil && *info.Courtesy.App != "":
		return "Cortesia: em uso pelo app " + *info.Courtesy.App + "."
	default:
		return "Cortesia: em uso neste período (um app de cortesia foi apagado recentemente)."
	}
}

func paymentLine(registered bool) string {
	if registered {
		return "Meio de pagamento: cadastrado."
	}
	return "Meio de pagamento: não cadastrado — necessário para apps além da cortesia: " + billingURL
}

// folderLine diz se a pasta pedida já está ligada a um app (.jatai.yml).
func (s *state) folderLine(ctx context.Context, req *mcp.CallToolRequest, pasta string) string {
	dir, err := s.resolveDir(ctx, req, pasta)
	if err != nil {
		return err.Error()
	}
	p, _, err := config.LoadProject(dir)
	switch {
	case errors.Is(err, config.ErrNoProject) || (err == nil && p.App == ""):
		return "Pasta " + dir + ": ainda não tem app no Jataí. Próximo passo: jatai_criar_app."
	case err != nil:
		return "Pasta " + dir + ": não consegui ler o .jatai.yml (" + err.Error() + "). Corrija ou apague o arquivo " +
			"e chame jatai_criar_app."
	default:
		return "Pasta " + dir + ": app " + p.App + " (.jatai.yml). Próximo passo: jatai_publicar."
	}
}

// pendingLogin é o código de dispositivo à espera de aprovação entre chamadas.
type pendingLogin struct {
	start auth.Start
	// expires zero = o servidor não disse a validade.
	expires time.Time
}

func (p *pendingLogin) link() string {
	if p.start.VerificationURIComplete != "" {
		return p.start.VerificationURIComplete
	}
	return p.start.VerificationURI
}

func (p *pendingLogin) waitingText() string {
	return "Ainda aguardando a aprovação de " + p.start.UserCode + " em " + p.link() + ". Chame jatai_login de novo."
}

const signupHint = "Ainda não tem conta? Crie em https://jataicloud.com.br e confirme o e-mail antes de aprovar."

func withSignup(res *mcp.CallToolResult) *mcp.CallToolResult {
	tc := res.Content[0].(*mcp.TextContent)
	tc.Text += "\n" + signupHint
	return res
}

// login em duas chamadas, para nunca prender o agente: a primeira devolve na
// hora o link e o código; a seguinte espera a aprovação por até Budget.
func (s *state) login(ctx context.Context, _ *mcp.CallToolRequest, _ loginInput) *mcp.CallToolResult {
	// Uma espera por vez; a chamada simultânea responde na hora, em vez de
	// ficar presa atrás da outra além do tempo limite do cliente.
	if !s.loginMu.TryLock() {
		if p := s.pending.Load(); p != nil {
			return withSignup(textResult(p.waitingText()))
		}
		return withSignup(textResult("Um login já está começando. Chame jatai_login de novo em instantes."))
	}
	defer s.loginMu.Unlock()
	if res, done := s.alreadyLoggedIn(ctx); done {
		return withSignup(res)
	}
	if p := s.pending.Load(); p != nil && (p.expires.IsZero() || s.d.Now().Before(p.expires)) {
		return withSignup(s.awaitApproval(ctx, p))
	}
	// Sem código, ou com um que já venceu: começa outro na hora.
	return withSignup(s.beginLogin(ctx))
}

// alreadyLoggedIn confere o token salvo em /api/v1/account. Token recusado
// segue para um login novo — menos quando vem de JATAI_TOKEN, que teria
// prioridade sobre a credencial nova.
func (s *state) alreadyLoggedIn(ctx context.Context) (*mcp.CallToolResult, bool) {
	token := config.Token(s.d.Paths, s.d.Getenv)
	if token == "" {
		return nil, false
	}
	c, err := s.newClient(token)
	if err != nil {
		return toolError(s.explainAccount(err)), true
	}
	var info accountInfo
	err = c.Get(ctx, "/api/v1/account", nil, &info)
	switch {
	case err == nil:
		s.pending.Store(nil)
		return textResult("Já está logado como " + who(info.User.Name, info.User.Email) + "."), true
	case api.IsStatus(err, 401) && strings.TrimSpace(s.d.Getenv("JATAI_TOKEN")) != "":
		return toolError("A variável de ambiente JATAI_TOKEN tem um token recusado pelo Jataí e tem prioridade " +
			"sobre o login. Remova JATAI_TOKEN da configuração do servidor MCP, reinicie-o e chame jatai_login."), true
	case api.IsStatus(err, 401):
		return nil, false
	default:
		return toolError(s.explainAccount(err)), true
	}
}

func (s *state) beginLogin(ctx context.Context) *mcp.CallToolResult {
	s.pending.Store(nil)
	c, err := s.newClient("")
	if err != nil {
		return toolError(s.explainAccount(err))
	}
	host, _ := s.d.Hostname()
	started := s.d.Now()
	start, err := auth.Begin(ctx, c, auth.ClientInfo{Hostname: host, OS: runtime.GOOS, Arch: runtime.GOARCH, Version: s.d.Version})
	if err != nil {
		return toolError(s.explainAccount(err))
	}
	p := &pendingLogin{start: start}
	if start.ExpiresIn > 0 {
		p.expires = started.Add(time.Duration(start.ExpiresIn) * time.Second)
	}
	s.pending.Store(p)
	text := "Peça ao usuário para abrir " + p.link() + " e aprovar o código " + start.UserCode +
		"; depois chame jatai_login de novo."
	if s.d.CanOpenBrowser() {
		if err := s.d.OpenBrowser(p.link()); err == nil {
			text = "O navegador desta máquina foi aberto na página de aprovação. " + text
		} else {
			fmt.Fprintf(s.d.Log, "jatai mcp: não foi possível abrir o navegador: %v\n", err)
		}
	}
	return textResult(text)
}

func (s *state) awaitApproval(ctx context.Context, p *pendingLogin) *mcp.CallToolResult {
	c, err := s.newClient("")
	if err != nil {
		return toolError(s.explainAccount(err))
	}
	wctx, cancel := context.WithTimeout(ctx, s.d.Budget)
	defer cancel()
	grant, err := auth.Wait(wctx, c, p.start, s.d.Sleep)
	switch {
	case err == nil:
		s.pending.Store(nil)
		if err := config.SaveToken(s.d.Paths, grant.Token); err != nil {
			return toolError("Login aprovado, mas não foi possível gravar a credencial em " +
				s.d.Paths.CredentialsFile() + ": " + err.Error())
		}
		return textResult("Logado como " + who(grant.User.Name, grant.User.Email) + ". Próximo passo: jatai_conta.")
	case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
		return textResult(p.waitingText())
	case errors.Is(err, auth.ErrExpired):
		s.pending.Store(nil)
		return toolError("O código " + p.start.UserCode + " expirou antes da aprovação. Chame jatai_login de novo " +
			"para gerar outro.")
	case errors.Is(err, auth.ErrDenied):
		s.pending.Store(nil)
		return toolError("O pedido " + p.start.UserCode + " foi recusado no navegador. Chame jatai_login de novo " +
			"para gerar outro código.")
	case ctx.Err() != nil:
		return toolError(s.explain(err))
	case api.IsNetwork(err):
		// O código continua valendo: a próxima chamada volta a esperar por ele.
		return textResult("Sem conexão com a API do Jataí agora. " + p.waitingText())
	case temporary(err):
		return textResult("O Jataí respondeu com um erro temporário. " + p.waitingText())
	default:
		// Outra recusa do servidor (4xx): o código não serve mais.
		s.pending.Store(nil)
		return toolError(sentence(s.explainAccount(err)) + " Chame jatai_login de novo para gerar outro código.")
	}
}

// temporary: 5xx (deploy do servidor em curso, proxy) e 429 (limite de taxa)
// passam sozinhos; o código de dispositivo continua valendo.
func temporary(err error) bool {
	var apiErr *api.Error
	return errors.As(err, &apiErr) && (apiErr.Status >= 500 || apiErr.Status == http.StatusTooManyRequests)
}
