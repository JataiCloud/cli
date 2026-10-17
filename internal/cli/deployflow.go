package cli

import (
	"fmt"
	"time"

	"github.com/jataicloud/cli/internal/api"
	"github.com/jataicloud/cli/internal/cable"
	"github.com/jataicloud/cli/internal/config"
	"github.com/jataicloud/cli/internal/deployflow"
	"github.com/jataicloud/cli/internal/ui"
)

// Os comandos das demais tarefas leem os mesmos JSONs do deploy.
type appInfo = deployflow.App

type deployInfo = deployflow.Deploy

var statusLabels = map[string]string{
	"pending": "Aguardando...", "provisioning": "Provisionando...", "queued": "Na fila...",
	"building": "Construindo imagem...", "deploying": "Implantando...",
}

func statusLabel(s string) string {
	if label, ok := statusLabels[s]; ok {
		return label
	}
	return s
}

// runner monta o fluxo de deploy com a configuração, o relógio e o git deste App.
func (a *App) runner(c *api.Client) *deployflow.Runner {
	return &deployflow.Runner{
		Client: c,
		Cable: cable.Options{
			APIURL: config.APIURL(a.Getenv), Token: config.Token(a.Paths, a.Getenv), UserAgent: a.userAgent(),
			WelcomeTimeout: a.WelcomeTimeout, ReconnectDelays: a.ReconnectDelays, Debug: a.debugWriter(),
		},
		Reporter:     &termReporter{ui: a.UI},
		Now:          a.Now,
		Sleep:        a.Sleep,
		PollInterval: a.PollInterval,
		Git:          a.Git,
	}
}

// termReporter escreve o andamento do deploy como a gem: texto em ciano, aviso
// de queda do WebSocket no stderr, spinner no envio e, no polling, status novo
// em linha nova, "." por consulta, "!" em falha de rede e o tempo a cada 12.
type termReporter struct {
	ui     *ui.UI
	polls  int
	status string
}

// Qualquer saída que não seja uma consulta encerra a sequência de polling em
// curso: a próxima começa do zero (contagem das consultas e último status).
func (t *termReporter) endPolling() { t.polls, t.status = 0, "" }

func (t *termReporter) Info(msg string) { t.endPolling(); t.ui.Info(msg) }
func (t *termReporter) Line(line string) {
	t.endPolling()
	t.ui.Println(line)
}

func (t *termReporter) Warn(msg string) {
	t.endPolling()
	fmt.Fprintln(t.ui.Err, t.ui.Yellow(msg))
}

func (t *termReporter) Step(msg string) func(string) {
	t.endPolling()
	return t.ui.StartSpinner(msg).Stop
}

func (t *termReporter) Polled(status string, networkErr bool, elapsed time.Duration) {
	i := t.polls
	t.polls++
	if networkErr {
		t.ui.Print("!")
		return
	}
	if status != t.status {
		t.ui.Print("\n       " + statusLabel(status))
		t.status = status
	}
	t.ui.Print(".")
	if i > 0 && i%12 == 0 {
		secs := int(elapsed.Seconds())
		t.ui.Print(fmt.Sprintf(" (%dm%ds)", secs/60, secs%60))
	}
}
