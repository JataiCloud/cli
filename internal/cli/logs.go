package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type logEntry struct {
	Timestamp string `json:"timestamp"`
	Source    string `json:"source"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

func (a *App) printLogs(entries []logEntry, empty string) {
	if len(entries) == 0 {
		a.UI.Info(empty)
		return
	}
	for _, e := range entries {
		a.UI.Println(fmt.Sprintf("%s %s [%s] %s", a.UI.Dim(e.Timestamp), a.UI.Cyan(e.Source), colorizeLevel(a.UI, e.Level), e.Message))
	}
}

func newLogsCmd(a *App) *cobra.Command {
	var tail int
	cmd := &cobra.Command{
		Use: "logs", Short: "Ver logs da aplicação", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, slug, err := a.appContext()
			if err != nil {
				return err
			}
			var entries []logEntry
			q := url.Values{"tail": {strconv.Itoa(tail)}}
			if err := c.Get(cmd.Context(), "/api/v1/apps/"+slug+"/logs", q, &entries); err != nil {
				return err
			}
			a.printLogs(entries, "Nenhum log disponível.")
			return nil
		},
	}
	cmd.Flags().IntVar(&tail, "tail", 100, "Número de linhas")
	return cmd
}

func newLogsSearchCmd(a *App) *cobra.Command {
	var query, level, source, since, limit string
	cmd := &cobra.Command{
		Use: "logs:search", Short: "Buscar nos logs da aplicação", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, slug, err := a.appContext()
			if err != nil {
				return err
			}
			q := url.Values{"q": {query}, "limit": {limit}}
			for key, value := range map[string]string{"level": level, "source": source, "since": since} {
				if value != "" {
					q.Set(key, value)
				}
			}
			var entries []logEntry
			if err := c.Get(cmd.Context(), "/api/v1/apps/"+slug+"/logs/search", q, &entries); err != nil {
				return err
			}
			a.printLogs(entries, "Nenhum resultado encontrado.")
			return nil
		},
	}
	cmd.Flags().StringVar(&query, "query", "", "Termo de busca")
	cmd.Flags().StringVar(&level, "level", "", "Filtrar por nível (error, warn, info)")
	cmd.Flags().StringVar(&source, "source", "", "Filtrar por source")
	cmd.Flags().StringVar(&since, "since", "", "Período (ex: 1h, 24h, 7d)")
	cmd.Flags().StringVar(&limit, "limit", "100", "Limite de resultados")
	_ = cmd.MarkFlagRequired("query")
	return cmd
}

func newLogsExportCmd(a *App) *cobra.Command {
	var format, since, until, level string
	cmd := &cobra.Command{
		Use: "logs:export", Short: "Exportar logs da aplicação", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, slug, err := a.appContext()
			if err != nil {
				return err
			}
			q := url.Values{"export_format": {format}}
			for key, value := range map[string]string{"since": since, "until": until, "level": level} {
				if value != "" {
					q.Set(key, value)
				}
			}
			data, contentType, err := c.GetRaw(cmd.Context(), "/api/v1/apps/"+slug+"/logs/export", q)
			if err != nil {
				return err
			}
			if strings.Contains(contentType, "json") {
				var pretty bytes.Buffer
				if json.Indent(&pretty, data, "", "  ") == nil {
					a.UI.Println(pretty.String())
					return nil
				}
			}
			a.UI.Print(string(data))
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "json", "Formato de exportação (json, csv)")
	cmd.Flags().StringVar(&since, "since", "", "Data/hora inicial")
	cmd.Flags().StringVar(&until, "until", "", "Data/hora final")
	cmd.Flags().StringVar(&level, "level", "", "Filtrar por nível (error, warn, info)")
	return cmd
}
