package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/jataicloud/cli/internal/api"
	"github.com/spf13/cobra"
)

func Main(ctx context.Context, version string, args []string) int {
	return Run(ctx, NewApp(version), args)
}

// Run executa a CLI com o App dado e devolve o código de saída.
func Run(ctx context.Context, a *App, args []string) int {
	root := newRootCmd(a)
	root.SetArgs(args)
	return a.report(root.ExecuteContext(ctx))
}

func newRootCmd(a *App) *cobra.Command {
	root := &cobra.Command{
		Use:           "jatai",
		Short:         "CLI da Jataí — deploy na nuvem brasileira",
		Version:       a.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate("jatai {{.Version}}\n")
	root.SetOut(a.UI.Out)
	root.SetErr(a.UI.Err)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageErr(err.Error()) })
	root.AddCommand(
		&cobra.Command{
			Use: "version", Short: "Exibir versão da CLI", Args: cobra.NoArgs,
			Run: func(*cobra.Command, []string) { a.UI.Println("jatai " + a.Version) },
		},
		newLoginCmd(a), newLogoutCmd(a), newInitCmd(a),
	)
	return root
}

func (a *App) report(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *ExitError
	var usage *UsageError
	var apiErr *api.Error
	msg := err.Error()
	switch {
	case errors.As(err, &exitErr):
		if exitErr.Message != "" {
			a.UI.Error(exitErr.Message)
		}
		return exitErr.Code
	case errors.As(err, &usage), strings.HasPrefix(msg, "unknown command"),
		strings.Contains(msg, "arg(s)"), strings.Contains(msg, "required flag"):
		a.UI.Error(msg)
		return 2
	case errors.Is(err, context.Canceled):
		a.UI.Error("Interrompido.")
		return 130
	case errors.As(err, &apiErr):
		a.UI.Error("Erro: " + apiErr.Message)
		return 1
	default:
		a.UI.Error(msg)
		return 1
	}
}
