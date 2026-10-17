// Package deployflow é o fluxo de deploy da Jataí sem terminal: criar o app,
// provisionar, enviar o código e acompanhar o build. Quem chama (a CLI, o
// servidor MCP) recebe o andamento por um Reporter e decide como mostrá-lo.
package deployflow

import (
	"context"
	"errors"
	"time"

	"github.com/jataicloud/cli/internal/api"
	"github.com/jataicloud/cli/internal/cable"
	"github.com/jataicloud/cli/internal/config"
)

const (
	ProvisionTimeout  = 180 * time.Second
	UpDeployTimeout   = 900 * time.Second
	DeployTimeout     = 1800 * time.Second
	ProvisionMaxPolls = 36
	UpDeployMaxPolls  = 180
	DeployMaxPolls    = 360
)

type App struct {
	ID           int     `json:"id"`
	Slug         string  `json:"slug"`
	Name         string  `json:"name"`
	Status       string  `json:"status"`
	URL          string  `json:"url"`
	Subdomain    string  `json:"subdomain"`
	Stack        string  `json:"stack"`
	Region       string  `json:"region"`
	Preset       string  `json:"preset"`
	DatabasePlan string  `json:"database_plan"`
	LastDeploy   *Deploy `json:"last_deploy"`
}

type ProcessFailure struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type Deploy struct {
	ID              int              `json:"id"`
	Status          string           `json:"status"`
	CommitSHA       string           `json:"commit_sha"`
	CreatedAt       string           `json:"created_at"`
	ProcessFailures []ProcessFailure `json:"process_failures"`
}

// Reporter recebe o andamento do fluxo. A CLI o escreve no terminal; o MCP o
// transforma em notificações de progresso.
type Reporter interface {
	Info(msg string)
	Warn(msg string)
	// Line é uma linha do log do build.
	Line(line string)
	// Step anuncia uma etapa demorada (o envio do pacote); a função devolvida
	// encerra a etapa com o desfecho.
	Step(msg string) func(result string)
	// Polled é cada consulta do polling que terminou sem status final.
	// networkErr marca a consulta que falhou por rede (status vazio).
	Polled(status string, networkErr bool, elapsed time.Duration)
}

// Runner reúne o que o fluxo usa do mundo externo, tudo injetável.
type Runner struct {
	Client       *api.Client
	Cable        cable.Options
	Reporter     Reporter
	Now          func() time.Time
	Sleep        func(context.Context, time.Duration) error
	PollInterval time.Duration
	Git          func(dir string, args ...string) (string, error)
}

// ErrSessionExpired: o token caiu durante o acompanhamento; o deploy segue no servidor.
var ErrSessionExpired = errors.New("\nSua sessão expirou durante o acompanhamento. O deploy continua no servidor.\n" +
	"Rode `jatai login` e acompanhe com `jatai status`.")

func BranchOf(p config.Project, flag string) string {
	switch {
	case flag != "":
		return flag
	case p.Branch != "":
		return p.Branch
	default:
		return "main"
	}
}

// URL canônica vem do servidor; o fallback só cobre servidor antigo.
func AppURL(app App) string {
	if app.URL != "" {
		return app.URL
	}
	sub := app.Subdomain
	if sub == "" {
		sub = app.Slug
	}
	return "https://" + sub + ".jataiapp.com.br"
}

type nopReporter struct{}

func (nopReporter) Info(string)                        {}
func (nopReporter) Warn(string)                        {}
func (nopReporter) Line(string)                        {}
func (nopReporter) Step(string) func(string)           { return func(string) {} }
func (nopReporter) Polled(string, bool, time.Duration) {}

func (r *Runner) reporter() Reporter {
	if r.Reporter == nil {
		return nopReporter{}
	}
	return r.Reporter
}

func (r *Runner) now() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

func (r *Runner) sleep(ctx context.Context, d time.Duration) error {
	if r.Sleep != nil {
		return r.Sleep(ctx, d)
	}
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
