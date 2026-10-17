package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jataicloud/cli/internal/api"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Nenhuma saída destas ferramentas, de sucesso ou de erro, carrega o valor de
// uma variável: o que a IA lê vai para o contexto dela.

const (
	valuesHidden = "Valores não são exibidos."
	nextDeploy   = "Valem no próximo deploy."
	// refusedEnv é o texto quando a mensagem da API ecoaria um valor enviado.
	refusedEnv = "A API recusou as variáveis: verifique os nomes."
)

func (s *state) envList(ctx context.Context, req *mcp.CallToolRequest, in listarInput) *mcp.CallToolResult {
	c, err := s.client(ctx)
	if err != nil {
		return toolError(s.explain(err))
	}
	sl, err := s.slugFor(ctx, req, in.Pasta, in.App)
	if err != nil {
		return toolError(err.Error())
	}
	cctx, cancel := context.WithTimeout(ctx, s.d.Budget)
	defer cancel()
	// Só as chaves interessam: o valor nem chega a ser decodificado.
	var vars map[string]any
	if err := c.Get(cctx, "/api/v1/apps/"+sl+"/env", url.Values{"masked": {"true"}}, &vars); err != nil {
		return toolError(s.explain(err))
	}
	if len(vars) == 0 {
		return textResult(fmt.Sprintf("O app %s não tem variáveis de ambiente.", sl))
	}
	keys := slices.Sorted(maps.Keys(vars))
	return textResult(fmt.Sprintf("Variáveis de %s (%d):\n%s\n%s", sl, len(keys), strings.Join(keys, "\n"), valuesHidden))
}

var (
	errNoVariables = errors.New("Informe em `variaveis` pelo menos uma variável, no formato {\"CHAVE\": \"valor\"}.")
	errNoKeys      = errors.New("Informe em `chaves` pelo menos uma chave de variável a remover.")
)

func (s *state) envSet(ctx context.Context, req *mcp.CallToolRequest, in definirInput) *mcp.CallToolResult {
	deadline := time.Now().Add(s.d.Budget)
	c, err := s.client(ctx)
	if err != nil {
		return toolError(s.explain(err))
	}
	sl, err := s.slugFor(ctx, req, in.Pasta, in.App)
	if err != nil {
		return toolError(err.Error())
	}
	env, err := cleanVariables(in.Variaveis)
	if err != nil {
		return toolError(err.Error())
	}
	cctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err := c.Put(cctx, "/api/v1/apps/"+sl+"/env", map[string]any{"env": env}, nil); err != nil {
		return toolError(explainEnvSet(s, err, env))
	}
	text := fmt.Sprintf("Definidas: %s. %s", strings.Join(slices.Sorted(maps.Keys(env)), ", "), nextDeploy)
	return s.thenPublish(ctx, req, c, in.Publicar, publicarInput{Pasta: in.Pasta, App: in.App}, text, deadline)
}

// cleanVariables aceita nomes aparados e não vazios; o valor vai como veio.
func cleanVariables(vars map[string]string) (map[string]string, error) {
	if len(vars) == 0 {
		return nil, errNoVariables
	}
	out := make(map[string]string, len(vars))
	for k, v := range vars {
		key := strings.TrimSpace(k)
		if key == "" {
			return nil, errors.New("Há uma variável sem nome em `variaveis`: cada chave precisa de um nome.")
		}
		out[key] = v
	}
	return out, nil
}

// minEchoLen é o menor valor que a guarda de eco considera: "1" ou "on" estão
// em qualquer frase e esconderiam mensagens que não ecoam nada.
const minEchoLen = 4

// explainEnvSet é o explain do PUT: num 422 (a validação do servidor às vezes
// cita o valor), a mensagem da API só sai se não ecoar nenhum dos valores
// enviados. As demais mensagens (401, 402, 409, 426…) são constantes do
// servidor e chegam à IA como estão.
func explainEnvSet(s *state, err error, env map[string]string) string {
	var apiErr *api.Error
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnprocessableEntity && echoesValue(apiErr, env) {
		return refusedEnv
	}
	return s.explain(err)
}

func echoesValue(e *api.Error, env map[string]string) bool {
	for _, v := range env {
		if len(v) >= minEchoLen && strings.Contains(e.Message, v) {
			return true
		}
	}
	return false
}

func (s *state) envUnset(ctx context.Context, req *mcp.CallToolRequest, in removerInput) *mcp.CallToolResult {
	deadline := time.Now().Add(s.d.Budget)
	c, err := s.client(ctx)
	if err != nil {
		return toolError(s.explain(err))
	}
	sl, err := s.slugFor(ctx, req, in.Pasta, in.App)
	if err != nil {
		return toolError(err.Error())
	}
	keys, err := cleanKeys(in.Chaves)
	if err != nil {
		return toolError(err.Error())
	}
	escaped := make([]string, len(keys))
	for i, k := range keys {
		escaped[i] = url.PathEscape(k)
	}
	cctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err := c.Delete(cctx, "/api/v1/apps/"+sl+"/env/"+strings.Join(escaped, ","), nil); err != nil {
		return toolError(s.explain(err))
	}
	text := fmt.Sprintf("Removidas: %s. %s", strings.Join(keys, ", "), nextDeploy)
	return s.thenPublish(ctx, req, c, in.Publicar, publicarInput{Pasta: in.Pasta, App: in.App}, text, deadline)
}

// cleanKeys valida as chaves antes do DELETE: a lista segue na URL separada
// por vírgula, então uma chave vazia ou com vírgula removeria a coisa errada.
func cleanKeys(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, errNoKeys
	}
	keys := make([]string, 0, len(raw))
	for _, k := range raw {
		key := strings.TrimSpace(k)
		switch {
		case key == "":
			return nil, errors.New("Há uma chave vazia em `chaves`: informe só nomes de variáveis.")
		case strings.Contains(key, ","):
			return nil, fmt.Errorf("A chave %q tem vírgula: passe cada chave como um item de `chaves`.", key)
		}
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

// thenPublish acrescenta o resultado do jatai_publicar à confirmação quando
// publicar=true: o mesmo caminho (jobs, prazo, textos), com o prazo que sobrou
// da chamada. Se a publicação falhar, as variáveis já foram gravadas e o texto
// diz isso antes do erro.
func (s *state) thenPublish(ctx context.Context, req *mcp.CallToolRequest, c *api.Client, publish bool,
	in publicarInput, confirmation string, deadline time.Time) *mcp.CallToolResult {
	if !publish {
		return textResult(confirmation)
	}
	res := s.publishWith(ctx, req, c, in, deadline)
	var parts []string
	for _, content := range res.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	out := textResult(confirmation + "\n" + strings.Join(parts, "\n"))
	out.IsError = res.IsError
	return out
}
