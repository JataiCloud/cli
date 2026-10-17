package cli

import (
	"github.com/jataicloud/cli/internal/deployflow"
	"github.com/spf13/cobra"
)

func newUpCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use: "up", Short: "Criar app e fazer primeiro deploy", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := a.authedClient()
			if err != nil {
				return err
			}
			p, path, err := a.currentProject()
			if err != nil {
				return err
			}
			guess, err := a.appSlug(p)
			if err != nil {
				return err
			}
			dir, err := a.Getwd()
			if err != nil {
				return err
			}
			r := a.runner(c)
			app, err := r.FindOrCreateApp(ctx, dir, p, path, guess)
			if err != nil {
				return err
			}
			if app.Status == "active" || app.Status == "sleeping" {
				a.UI.Info("Iniciando deploy...")
			} else {
				a.UI.Info("Provisionando infraestrutura...")
				if err := r.Provision(ctx, app); err != nil {
					return err
				}
				a.UI.Success("Infraestrutura provisionada!")
				a.UI.Info("Iniciando primeiro deploy...")
			}
			d, err := r.Upload(ctx, dir, app.Slug, deployflow.BranchOf(p, ""))
			if err != nil {
				return err
			}
			a.UI.Info("       Na fila...")
			status, err := r.WaitDeploy(ctx, app.Slug, d.ID, deployflow.UpDeployTimeout, deployflow.UpDeployMaxPolls)
			if err != nil {
				return err
			}
			switch status {
			case "live":
			case "partially_live":
				a.UI.Warning("\nDeploy parcialmente concluído — processo(s) fora do release; ver o deploy no painel.")
			default:
				return userErr("\nDeploy falhou.")
			}
			a.UI.Success("Deploy concluído! App disponível em " + deployflow.AppURL(app))
			return nil
		},
	}
}
