package cli

import (
	"io"
	"os"

	"github.com/jataicloud/cli/internal/mcpserver"
	"github.com/jataicloud/cli/internal/update"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func newMCPCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use: "mcp", Short: "Servidor MCP para agentes de IA (stdio)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// O stdout é do protocolo: nada da ui (mensagem, cor, spinner, prompt) chega nele.
			a.UI.Out = a.UI.Err
			a.UI.Interactive = false
			a.UI.Color = false
			in, out := a.Stdin, a.Stdout
			if in == nil {
				in = os.Stdin
			}
			if out == nil {
				out = os.Stdout
			}
			ctx := cmd.Context()
			err := mcpserver.Serve(ctx, a.mcpDeps(), &mcp.IOTransport{Reader: io.NopCloser(in), Writer: nopWriteCloser{out}})
			if ctx.Err() != nil {
				// Ctrl-C ou SIGTERM do cliente: é o jeito normal de encerrar o servidor.
				return nil
			}
			return err
		},
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func (a *App) mcpDeps() mcpserver.Deps {
	return mcpserver.Deps{
		Version:         a.Version,
		UserAgent:       a.userAgent(),
		Paths:           a.Paths,
		Getenv:          a.Getenv,
		Getwd:           a.Getwd,
		HTTP:            a.HTTP,
		Hostname:        a.Hostname,
		OpenBrowser:     a.OpenBrowser,
		CanOpenBrowser:  a.CanOpenBrowser,
		Now:             a.Now,
		Sleep:           a.Sleep,
		PollInterval:    a.PollInterval,
		ReconnectDelays: a.ReconnectDelays,
		WelcomeTimeout:  a.WelcomeTimeout,
		Budget:          mcpserver.CallBudget,
		UpdateHint:      update.Hint(a.installMethod()),
		Log:             a.UI.Err,
		Git:             a.Git,
	}
}
