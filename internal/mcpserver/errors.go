package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/jataicloud/cli/internal/api"
	"github.com/jataicloud/cli/internal/config"
	"github.com/jataicloud/cli/internal/deployflow"
)

const (
	billingURL = "https://jataicloud.com.br/billing"
	courtesy   = "1 Hex Base + 1 PG Base sem cartão"
)

var errNotLoggedIn = errors.New("Você não está logado no Jataí. Chame a ferramenta jatai_login.")

// explain traduz um erro no texto que a IA lê, sempre com o próximo passo.
// Fora de um servidor (sem Deps), o 426 sai sem o comando de atualização
// desta instalação e a falha de rede usa o endereço que estava no erro.
func explain(err error) string { return explainWith(err, "", "") }

// explain com o comando de atualização e o endereço da API deste servidor.
func (s *state) explain(err error) string {
	return explainWith(err, s.d.UpdateHint, config.APIURL(s.d.Getenv))
}

func explainWith(err error, updateHint, apiURL string) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, errNotLoggedIn) || errors.Is(err, deployflow.ErrSessionExpired) {
		return errNotLoggedIn.Error()
	}
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return explainAPI(apiErr, updateHint)
	}
	var urlErr *url.Error
	if api.IsNetwork(err) || errors.As(err, &urlErr) {
		if apiURL == "" {
			apiURL = baseOf(urlErr)
		}
		return "Sem conexão com a API do Jataí (" + apiURL + "). Tente de novo em instantes."
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "A operação foi interrompida antes de terminar. Tente de novo."
	}
	// As mensagens do deployflow herdaram da gem recuo e quebras de linha.
	return strings.TrimSpace(err.Error())
}

func explainAPI(e *api.Error, updateHint string) string {
	msg := sentence(e.Message)
	switch e.Status {
	case http.StatusUnauthorized:
		return errNotLoggedIn.Error()
	case http.StatusPaymentRequired:
		return msg + " A cortesia cobre " + courtesy + "; para ir além, cadastre um meio de pagamento em " + billingURL
	case http.StatusForbidden:
		return msg + " Veja o plano e o faturamento em " + billingURL
	case http.StatusNotFound:
		return "App não encontrado neste time. Use jatai_conta para ver os apps."
	case http.StatusConflict:
		return msg + " Use jatai_status para acompanhar."
	case http.StatusUpgradeRequired:
		if updateHint == "" {
			updateHint = "jatai update (ou npm update -g jatai, se veio do npm)"
		}
		return "Esta versão da CLI ficou para trás. Atualize com: " + updateHint +
			". Depois reinicie o servidor MCP jatai no agente para carregar a versão nova."
	default:
		return msg
	}
}

// sentence garante a pontuação final, para o texto seguinte não grudar.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}

// baseOf devolve só esquema e host do endereço que falhou (sem caminho nem query).
func baseOf(e *url.Error) string {
	if e == nil {
		return config.DefaultAPIURL
	}
	u, err := url.Parse(e.URL)
	if err != nil || u.Host == "" {
		return config.DefaultAPIURL
	}
	return u.Scheme + "://" + u.Host
}
