// Package mcpserver é o servidor MCP local da CLI (`jatai mcp`): expõe a
// agentes de IA as ferramentas para publicar a pasta do projeto no Jataí.
// O stdout é só do protocolo; diagnósticos vão para Deps.Log (o stderr).
package mcpserver

import (
	"context"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/jataicloud/cli/internal/config"
)

// CallBudget é o teto de cada chamada de ferramenta: abaixo dos 60 s que o
// Codex usa por padrão, para a resposta chegar antes do corte do cliente.
const CallBudget = 45 * time.Second

// Deps é tudo o que o servidor usa do mundo externo, injetável nos testes.
type Deps struct {
	Version, UserAgent string
	Paths              config.Paths
	Getenv             func(string) string
	Getwd              func() (string, error)
	// HTTP nil = o cliente padrão do pacote api.
	HTTP            *http.Client
	Hostname        func() (string, error)
	OpenBrowser     func(string) error
	CanOpenBrowser  func() bool
	Now             func() time.Time
	Sleep           func(context.Context, time.Duration) error
	PollInterval    time.Duration
	ReconnectDelays []time.Duration
	WelcomeTimeout  time.Duration
	// Budget é o teto por chamada; zero = CallBudget.
	Budget time.Duration
	// UpdateHint é o comando de atualização desta instalação (texto do 426).
	UpdateHint string
	// Log é o stderr: nunca o stdout, que é do protocolo.
	Log io.Writer
	Git func(dir string, args ...string) (string, error)
}

// withDefaults preenche o que faltar com o comportamento real, para que um
// Deps parcial nunca derrube o servidor com ponteiro nulo.
func (d Deps) withDefaults() Deps {
	if d.Getenv == nil {
		d.Getenv = os.Getenv
	}
	if d.Getwd == nil {
		d.Getwd = os.Getwd
	}
	if d.Hostname == nil {
		d.Hostname = os.Hostname
	}
	if d.OpenBrowser == nil {
		d.OpenBrowser = func(string) error { return nil }
	}
	if d.CanOpenBrowser == nil {
		d.CanOpenBrowser = func() bool { return false }
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Sleep == nil {
		d.Sleep = sleepCtx
	}
	if d.Budget <= 0 {
		d.Budget = CallBudget
	}
	if d.Log == nil {
		d.Log = io.Discard
	}
	return d
}

func sleepCtx(ctx context.Context, d time.Duration) error {
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
