package deployflow

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/jataicloud/cli/internal/api"
	"github.com/jataicloud/cli/internal/cable"
)

// Watch abre o WebSocket com o tempo-limite dado e roda fn. Não há reserva
// aqui: qualquer erro volta para quem chamou (tempo esgotado já com mensagem em
// português; WebSocket fora; Ctrl-C como o erro do contexto). Quem cai no
// polling, como na gem, é Provision/WaitDeploy, e só quando o contexto de quem
// chamou continua vivo.
func (r *Runner) Watch(ctx context.Context, timeout time.Duration, fn func(context.Context, *cable.Conn) (string, error)) (string, error) {
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := cable.Connect(wctx, r.Cable)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	status, err := fn(wctx, conn)
	if err != nil && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return "", fmt.Errorf("tempo esgotado aguardando status (%ds)", int(timeout.Seconds()))
	}
	return status, err
}

// Como na gem, o aviso de queda para o polling vai para o stderr (Warn).
func (r *Runner) warnFallback(err error) {
	r.reporter().Warn("\n       [WebSocket indisponível: " + err.Error() + ". Usando polling...]")
}

// poll consulta a cada PollInterval até um status final e reporta cada
// consulta que não terminou: status novo, falha de rede e tempo decorrido.
func (r *Runner) poll(ctx context.Context, maxPolls int, final []string, fetch func(context.Context) (string, error)) (string, error) {
	started := r.now()
	for i := 0; i < maxPolls; i++ {
		status, err := fetch(ctx)
		switch {
		case api.IsStatus(err, http.StatusUnauthorized):
			return "", ErrSessionExpired
		case api.IsNetwork(err):
			r.reporter().Polled("", true, r.now().Sub(started))
		case err != nil:
			return "", err
		case slices.Contains(final, status):
			return status, nil
		default:
			r.reporter().Polled(status, false, r.now().Sub(started))
		}
		if err := r.sleep(ctx, r.PollInterval); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("\nTimeout (%ds). Verifique com: jatai status", int(r.now().Sub(started).Seconds()))
}
