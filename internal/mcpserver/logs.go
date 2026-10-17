package mcpserver

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultLogLines = 100
	maxLogLines     = 500
	// maxLogLineChars limita cada linha devolvida à IA: a API aceita mensagens
	// de até 10 mil caracteres, e 500 delas encheriam o contexto.
	maxLogLineChars = 1000
)

// logEntry é uma linha de GET /api/v1/apps/:slug/logs (a mesma da CLI, que
// este pacote não importa).
type logEntry struct {
	Timestamp string `json:"timestamp"`
	Source    string `json:"source"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

// logs devolve as linhas recentes. Com filtro, lê as últimas maxLogLines
// linhas e filtra aqui: o logs/search responde 402 na Extensão de Logs básica,
// que todo aluno do curso tem.
func (s *state) logs(ctx context.Context, req *mcp.CallToolRequest, in logsInput) *mcp.CallToolResult {
	c, err := s.client(ctx)
	if err != nil {
		return toolError(s.explain(err))
	}
	sl, err := s.slugFor(ctx, req, in.Pasta, in.App)
	if err != nil {
		return toolError(err.Error())
	}
	want := in.Linhas
	if want <= 0 {
		want = defaultLogLines
	}
	want = min(want, maxLogLines)
	filter := strings.TrimSpace(in.Filtro)
	tail := want
	if filter != "" {
		tail = maxLogLines
	}
	cctx, cancel := context.WithTimeout(ctx, s.d.Budget)
	defer cancel()
	var entries []logEntry
	query := url.Values{"tail": {strconv.Itoa(tail)}}
	if err := c.Get(cctx, "/api/v1/apps/"+sl+"/logs", query, &entries); err != nil {
		return toolError(s.explain(err))
	}
	if len(entries) == 0 {
		return textResult(fmt.Sprintf("Sem linhas de log para %s ainda.", sl))
	}
	if filter != "" {
		entries = matchingEntries(entries, filter)
		if len(entries) == 0 {
			return textResult(fmt.Sprintf("Nenhuma linha com %q nas últimas %d linhas de %s.", filter, maxLogLines, sl))
		}
	}
	if len(entries) > want {
		entries = entries[len(entries)-want:] // a API manda do mais antigo ao mais novo
	}
	lines := make([]string, len(entries))
	for i, e := range entries {
		lines[i] = formatLogEntry(e)
	}
	return textResult(strings.Join(lines, "\n"))
}

// matchingEntries guarda as entradas cuja mensagem contém filter, sem
// diferenciar maiúsculas de minúsculas.
func matchingEntries(entries []logEntry, filter string) []logEntry {
	needle := strings.ToLower(filter)
	var out []logEntry
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Message), needle) {
			out = append(out, e)
		}
	}
	return out
}

// formatLogEntry é "<hora> [<nível>] <mensagem>", com a hora no fuso local.
func formatLogEntry(e logEntry) string {
	level := strings.TrimSpace(e.Level)
	if level == "" {
		level = "-"
	}
	msg := strings.TrimRight(e.Message, " \t\r\n")
	if r := []rune(msg); len(r) > maxLogLineChars {
		msg = string(r[:maxLogLineChars]) + "…"
	}
	return fmt.Sprintf("%s [%s] %s", logClock(e.Timestamp), level, msg)
}

// logClock mostra só a hora (15:04:05) de um timestamp ISO-8601, no fuso local;
// o que não der para ler sai como veio.
func logClock(raw string) string {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	return t.Local().Format("15:04:05")
}
