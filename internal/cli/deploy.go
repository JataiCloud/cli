package cli

import (
	"context"
	"fmt"

	"github.com/jataicloud/cli/internal/api"
	"github.com/jataicloud/cli/internal/deployflow"
	"github.com/spf13/cobra"
)

func newDeployCmd(a *App) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use: "deploy", Short: "Fazer deploy da aplicação", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := a.authedClient()
			if err != nil {
				return err
			}
			p, _, err := a.currentProject()
			if err != nil {
				return err
			}
			slug, err := a.appSlug(p)
			if err != nil {
				return err
			}
			dir, err := a.Getwd()
			if err != nil {
				return err
			}
			r := a.runner(c)
			d, err := r.Upload(ctx, dir, slug, deployflow.BranchOf(p, branch))
			if err != nil {
				return err
			}
			a.UI.Info(fmt.Sprintf("Deploy #%d criado.", d.ID))
			a.UI.Info("       Na fila...")
			started := a.Now()
			status, err := r.WaitDeploy(ctx, slug, d.ID, deployflow.DeployTimeout, deployflow.DeployMaxPolls)
			if err != nil {
				return err
			}
			elapsed := int(a.Now().Sub(started).Seconds())
			switch status {
			case "live":
				a.UI.Success(fmt.Sprintf("\nDeploy concluído com sucesso! (%ds)", elapsed))
			case "partially_live":
				printPartialSummary(ctx, a, c, slug, d.ID, elapsed)
			default:
				return userErr("\nDeploy falhou.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Branch para deploy")
	return cmd
}

// O deploy parcial é terminal: o site subiu, mas processos declarados ficaram
// no release anterior.
func printPartialSummary(ctx context.Context, a *App, c *api.Client, slug string, id, elapsed int) {
	a.UI.Warning(fmt.Sprintf("\nDeploy parcialmente concluído (%ds)", elapsed))
	var d deployInfo
	if err := c.Get(ctx, fmt.Sprintf("/api/v1/apps/%s/deploys/%d", slug, id), nil, &d); err != nil {
		a.UI.Warning("  (detalhe indisponível — ver o deploy no painel)")
		return
	}
	for _, f := range d.ProcessFailures {
		a.UI.Warning("  " + f.Name + ": " + f.Reason)
	}
	a.UI.Warning("O site está no ar na versão nova; os processos acima seguem na anterior.")
}
