package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newStatusCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "Ver status da aplicação", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, slug, err := a.appContext()
			if err != nil {
				return err
			}
			var app appInfo
			if err := c.Get(cmd.Context(), "/api/v1/apps/"+slug, nil, &app); err != nil {
				return err
			}
			u := a.UI
			u.Println(u.Bold("App: " + app.Name))
			u.Println("  Slug:    " + app.Slug)
			u.Println("  Stack:   " + app.Stack)
			u.Println("  Status:  " + colorizeStatus(u, app.Status))
			u.Println("  Região:  " + app.Region)
			u.Println("  Hex:     " + hexLabel(app.Preset))
			u.Println("  Banco:   " + pgLabel(app.DatabasePlan))
			if app.URL != "" {
				u.Println("  URL:     " + app.URL)
			}
			if d := app.LastDeploy; d != nil {
				u.Println("")
				u.Println(u.Bold("Último deploy:"))
				u.Println(fmt.Sprintf("  ID:      %d", d.ID))
				u.Println("  Status:  " + colorizeStatus(u, d.Status))
				if d.CommitSHA != "" {
					u.Println("  Commit:  " + d.CommitSHA)
				}
				u.Println("  Data:    " + d.CreatedAt)
			}
			return nil
		},
	}
}
