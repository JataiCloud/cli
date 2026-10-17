package cli

import (
	"runtime"

	"github.com/jataicloud/cli/internal/auth"
	"github.com/jataicloud/cli/internal/config"
	"github.com/spf13/cobra"
)

func newLoginCmd(a *App) *cobra.Command {
	var noBrowser bool
	cmd := &cobra.Command{
		Use: "login", Short: "Autenticar pelo navegador", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := a.client("")
			if err != nil {
				return err
			}
			host, _ := a.Hostname()
			start, err := auth.Begin(ctx, c, auth.ClientInfo{Hostname: host, OS: runtime.GOOS, Arch: runtime.GOARCH, Version: a.Version})
			if err != nil {
				return err
			}
			a.UI.Println("Abra " + a.UI.Bold(start.VerificationURI) + " e confirme o código " + a.UI.Bold(start.UserCode) + ".")
			if !noBrowser && a.CanOpenBrowser() && a.OpenBrowser(start.VerificationURIComplete) == nil {
				a.UI.Info("Abrimos o navegador para você.")
			}
			a.UI.Info("Aguardando a aprovação no navegador...")
			grant, err := auth.Wait(ctx, c, start, a.Sleep)
			if err != nil {
				return err
			}
			if err := config.SaveToken(a.Paths, grant.Token); err != nil {
				return err
			}
			a.UI.Success("Login realizado com sucesso! Conectado como " + grant.User.Email + ".")
			return nil
		},
	}
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Só mostrar o link, sem abrir o navegador")
	return cmd
}
