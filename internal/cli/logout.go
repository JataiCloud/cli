package cli

import (
	"net/http"

	"github.com/jataicloud/cli/internal/api"
	"github.com/jataicloud/cli/internal/config"
	"github.com/spf13/cobra"
)

func newLogoutCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use: "logout", Short: "Revogar o token e sair", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token := config.Token(a.Paths, a.Getenv)
			if token == "" {
				a.UI.Info("Você não está logado.")
				return nil
			}
			c, err := a.client(token)
			if err != nil {
				return err
			}
			// Token já inválido no servidor (401): segue limpando o arquivo.
			if err := c.Delete(cmd.Context(), "/api/v1/cli/token", nil); err != nil && !api.IsStatus(err, http.StatusUnauthorized) {
				return err
			}
			if err := config.ClearToken(a.Paths); err != nil {
				return err
			}
			if a.Getenv("JATAI_TOKEN") != "" {
				a.UI.Warning("JATAI_TOKEN continua definida no ambiente; remova-a também.")
			}
			a.UI.Success("Logout realizado. O token foi revogado.")
			return nil
		},
	}
}
