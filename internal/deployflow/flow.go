package deployflow

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jataicloud/cli/internal/api"
	"github.com/jataicloud/cli/internal/cable"
	"github.com/jataicloud/cli/internal/config"
	"github.com/jataicloud/cli/internal/pack"
)

var (
	scpRemote = regexp.MustCompile(`\Agit@([^:]+):(.+?)(?:\.git)?\z`)
	sshRemote = regexp.MustCompile(`\Assh://git@([^/]+)/(.+?)(?:\.git)?\z`)
)

// O servidor só aceita repo_url https (é metadado da integração GitHub; o
// deploy é por upload). SSH vira https; o resto fica em branco.
func normalizeGitRemote(u string) string {
	if m := scpRemote.FindStringSubmatch(u); m != nil {
		return "https://" + m[1] + "/" + m[2]
	}
	if m := sshRemote.FindStringSubmatch(u); m != nil {
		return "https://" + m[1] + "/" + m[2]
	}
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return strings.TrimSuffix(u, ".git")
	}
	return ""
}

// gitRemote só chama o git dentro de um repositório (ver config.IsGitRepo).
func (r *Runner) gitRemote(dir string) string {
	if r.Git == nil || !config.IsGitRepo(dir) {
		return ""
	}
	out, err := r.Git(dir, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return normalizeGitRemote(out)
}

// FindOrCreateApp procura o app pelo palpite de slug (guess) e, se não existe,
// cria com os dados do projeto. Depois de criar, grava o slug verdadeiro no
// arquivo do projeto (projectPath).
func (r *Runner) FindOrCreateApp(ctx context.Context, dir string, p config.Project, projectPath, guess string) (App, error) {
	app, _, err := r.EnsureApp(ctx, dir, p, projectPath, guess)
	return app, err
}

// EnsureApp é o FindOrCreateApp que também diz se criou o app (created) ou
// achou um existente.
func (r *Runner) EnsureApp(ctx context.Context, dir string, p config.Project, projectPath, guess string) (app App, created bool, err error) {
	rep := r.reporter()
	err = r.Client.Get(ctx, "/api/v1/apps/"+guess, nil, &app)
	if err == nil {
		rep.Info(fmt.Sprintf("App %s encontrado (%s).", app.Slug, app.Status))
		if remote := r.gitRemote(dir); remote != "" {
			_ = r.Client.Patch(ctx, "/api/v1/apps/"+app.Slug, map[string]any{"app": map[string]any{"repo_url": remote}}, nil)
		}
		return app, false, nil
	}
	if !api.IsStatus(err, http.StatusNotFound) {
		return App{}, false, err
	}
	app, err = r.createApp(ctx, dir, p, projectPath)
	return app, err == nil, err
}

// CreateApp cria o app sem procurar antes, para quem já sabe que o slug está
// livre; grava no arquivo do projeto o slug que o servidor escolheu.
func (r *Runner) CreateApp(ctx context.Context, dir string, p config.Project, projectPath string) (App, error) {
	return r.createApp(ctx, dir, p, projectPath)
}

func (r *Runner) createApp(ctx context.Context, dir string, p config.Project, projectPath string) (App, error) {
	rep := r.reporter()
	var app App
	rep.Info("Criando app " + p.DisplayName() + "...")
	params := map[string]any{
		"name": p.DisplayName(), "stack": p.Stack, "preset": p.Preset, "region": p.Region, "repo_branch": p.Branch,
	}
	if remote := r.gitRemote(dir); remote != "" {
		params["repo_url"] = remote
	}
	if p.Release != nil && p.Release.Command != "" {
		params["release_command"] = p.Release.Command
	}
	if err := r.Client.Post(ctx, "/api/v1/apps", map[string]any{"app": params}, &app); err != nil {
		var apiErr *api.Error
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnprocessableEntity {
			return App{}, errors.New("Erro ao criar app: " + apiErr.Message)
		}
		return App{}, err
	}
	r.persistServerSlug(p, projectPath, app.Slug)
	return app, nil
}

// O slug do .jatai.yml é só o palpite: o servidor pode ter desambiguado
// (loja-1). Depois de criar, o arquivo passa a carregar o slug verdadeiro.
func (r *Runner) persistServerSlug(p config.Project, path, serverSlug string) {
	if serverSlug == "" || serverSlug == p.App {
		return
	}
	if err := config.PersistSlug(path, serverSlug); err != nil {
		r.reporter().Warn("       Não foi possível gravar o slug no .jatai.yml: " + err.Error())
		return
	}
	r.reporter().Info("Identificador do app no .jatai.yml: " + serverSlug)
}

// Upload empacota dir e envia como novo deploy da branch dada.
func (r *Runner) Upload(ctx context.Context, dir, slug, branch string) (Deploy, error) {
	rep := r.reporter()
	rep.Info("Empacotando código...")
	res, err := pack.Pack(dir)
	if errors.Is(err, pack.ErrTooLarge) {
		return Deploy{}, fmt.Errorf("O código empacotado tem %d MB; o limite é %d MB. "+
			"Adicione ao .dockerignore o que não vai para produção.", res.Size>>20, pack.MaxArchiveSize>>20)
	}
	if err != nil {
		return Deploy{}, err
	}
	defer os.Remove(res.Path)

	done := rep.Step(fmt.Sprintf("       Enviando %dKB...", res.Size/1024))
	var d Deploy
	err = r.Client.Upload(ctx, "/api/v1/apps/"+slug+"/deploys", res.Path, map[string]string{"branch": branch}, &d)
	if err != nil {
		done("falhou")
		return Deploy{}, err
	}
	done("enviado!")
	return d, nil
}

// Provision pede o provisionamento quando o app está pending/error e espera
// ele ficar ativo (até ProvisionTimeout). App já ativo ou dormindo não precisa.
func (r *Runner) Provision(ctx context.Context, app App) error {
	if app.Status == "active" || app.Status == "sleeping" {
		return nil
	}
	if app.Status == "pending" || app.Status == "error" {
		if err := r.requestProvision(ctx, app.Slug); err != nil {
			return err
		}
	}
	final := []string{"active", "error"}
	status, err := r.Watch(ctx, ProvisionTimeout, func(wctx context.Context, conn *cable.Conn) (string, error) {
		return conn.WaitFor(wctx, "ProvisionStatusChannel", map[string]any{"app_id": app.ID}, final, nil)
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.warnFallback(err)
		status, err = r.poll(ctx, ProvisionMaxPolls, final, func(ctx context.Context) (string, error) {
			var current App
			err := r.Client.Get(ctx, "/api/v1/apps/"+app.Slug, nil, &current)
			return current.Status, err
		})
		if err != nil {
			return err
		}
	}
	if status != "active" {
		return errors.New("\nErro no provisionamento.")
	}
	return nil
}

// requestProvision pede o provisionamento. Cobrança (402), permissão (403) e app
// inexistente (404) não se resolvem esperando: voltam na hora, com a mensagem da
// API. Qualquer outro erro (409 "já provisionando", 5xx, rede) não impede a
// espera, que é o que confirma o resultado.
func (r *Runner) requestProvision(ctx context.Context, slug string) error {
	err := r.Client.Post(ctx, "/api/v1/apps/"+slug+"/provision", nil, nil)
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusPaymentRequired, http.StatusForbidden, http.StatusNotFound:
			return err
		}
	}
	return nil
}

// WaitDeploy acompanha o deploy pelo WebSocket (linhas do build vão para
// Reporter.Line) e cai no polling se ele não estiver disponível. Devolve o
// status final: live, failed ou partially_live.
func (r *Runner) WaitDeploy(ctx context.Context, slug string, id int, timeout time.Duration, maxPolls int) (string, error) {
	return r.waitDeploy(ctx, slug, id, timeout, maxPolls, false)
}

// AwaitDeploy é o WaitDeploy para um deploy que outro processo soltou: logo
// depois de assinar o WebSocket, consulta o status uma vez pela API, para não
// esperar à toa por um deploy que terminou entre a consulta de quem chamou e
// a assinatura (o WebSocket não repete o status final).
func (r *Runner) AwaitDeploy(ctx context.Context, slug string, id int, timeout time.Duration, maxPolls int) (string, error) {
	return r.waitDeploy(ctx, slug, id, timeout, maxPolls, true)
}

var deployFinal = []string{"live", "failed", "partially_live"}

func (r *Runner) waitDeploy(ctx context.Context, slug string, id int, timeout time.Duration, maxPolls int, recheck bool) (string, error) {
	params := map[string]any{"deploy_id": id}
	status, err := r.Watch(ctx, timeout, func(wctx context.Context, conn *cable.Conn) (string, error) {
		if _, err := conn.Subscribe(wctx, "DeployLogChannel", params); err != nil {
			return "", err
		}
		sid, err := conn.Subscribe(wctx, "DeployStatusChannel", params)
		if err != nil {
			return "", err
		}
		if recheck {
			// Erro aqui não decide nada: o WebSocket continua esperando.
			if st, err := r.deployStatus(wctx, slug, id); err == nil && slices.Contains(deployFinal, st) {
				return st, nil
			}
		}
		return conn.Await(wctx, sid, "DeployStatusChannel", deployFinal, r.reporter().Line, r.statusFunc())
	})
	if err == nil {
		return status, nil
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	r.warnFallback(err)
	return r.poll(ctx, maxPolls, deployFinal, func(ctx context.Context) (string, error) {
		return r.deployStatus(ctx, slug, id)
	})
}

func (r *Runner) deployStatus(ctx context.Context, slug string, id int) (string, error) {
	var d Deploy
	err := r.Client.Get(ctx, fmt.Sprintf("/api/v1/apps/%s/deploys/%d", slug, id), nil, &d)
	return d.Status, err
}

// statusFunc repassa os status intermediários do WebSocket a quem implementa
// StatusReporter (o MCP); a CLI não os mostra, como na gem.
func (r *Runner) statusFunc() func(string) {
	if sr, ok := r.reporter().(StatusReporter); ok {
		return sr.Status
	}
	return nil
}

// buildLogResponse é o JSON de GET /api/v1/apps/:slug/deploys/:id/log; o fluxo
// só lê log (o servidor manda também deploy_id, status e line_count).
type buildLogResponse struct {
	Log string `json:"log"`
}

// BuildLogTail devolve as últimas lines linhas do log do build do deploy.
func (r *Runner) BuildLogTail(ctx context.Context, slug string, id, lines int) ([]string, error) {
	var out buildLogResponse
	query := url.Values{"tail": {strconv.Itoa(lines)}}
	if err := r.Client.Get(ctx, fmt.Sprintf("/api/v1/apps/%s/deploys/%d/log", slug, id), query, &out); err != nil {
		return nil, err
	}
	text := strings.TrimRight(out.Log, "\r\n")
	if text == "" {
		return []string{}, nil
	}
	return strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), nil
}
