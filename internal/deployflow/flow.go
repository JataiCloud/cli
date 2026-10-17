package deployflow

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
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

func (r *Runner) gitRemote(dir string) string {
	if r.Git == nil {
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
	rep := r.reporter()
	var app App
	err := r.Client.Get(ctx, "/api/v1/apps/"+guess, nil, &app)
	if err == nil {
		rep.Info(fmt.Sprintf("App %s encontrado (%s).", app.Slug, app.Status))
		if remote := r.gitRemote(dir); remote != "" {
			_ = r.Client.Patch(ctx, "/api/v1/apps/"+app.Slug, map[string]any{"app": map[string]any{"repo_url": remote}}, nil)
		}
		return app, nil
	}
	if !api.IsStatus(err, http.StatusNotFound) {
		return App{}, err
	}
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
	params := map[string]any{"deploy_id": id}
	final := []string{"live", "failed", "partially_live"}
	status, err := r.Watch(ctx, timeout, func(wctx context.Context, conn *cable.Conn) (string, error) {
		if _, err := conn.Subscribe(wctx, "DeployLogChannel", params); err != nil {
			return "", err
		}
		return conn.WaitFor(wctx, "DeployStatusChannel", params, final, r.reporter().Line)
	})
	if err == nil {
		return status, nil
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	r.warnFallback(err)
	return r.poll(ctx, maxPolls, final, func(ctx context.Context) (string, error) {
		var d Deploy
		err := r.Client.Get(ctx, fmt.Sprintf("/api/v1/apps/%s/deploys/%d", slug, id), nil, &d)
		return d.Status, err
	})
}

// BuildLogTail devolve as últimas lines linhas do log do build do deploy.
func (r *Runner) BuildLogTail(ctx context.Context, slug string, id, lines int) ([]string, error) {
	var out struct {
		Log string `json:"log"`
	}
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
