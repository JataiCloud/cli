package cli

import (
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

func newEnvCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{Use: "env", Short: "Gerenciar variáveis de ambiente"}
	cmd.AddCommand(
		&cobra.Command{
			Use: "list", Short: "Listar variáveis de ambiente", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				c, slug, err := a.appContext()
				if err != nil {
					return err
				}
				vars := map[string]string{}
				if err := c.Get(cmd.Context(), "/api/v1/apps/"+slug+"/env", nil, &vars); err != nil {
					return err
				}
				if len(vars) == 0 {
					a.UI.Info("Nenhuma variável de ambiente configurada.")
					return nil
				}
				for _, k := range slices.Sorted(maps.Keys(vars)) {
					a.UI.Println(a.UI.Bold(k) + "=" + vars[k])
				}
				return nil
			},
		},
		&cobra.Command{
			Use: "set KEY=VALUE [KEY=VALUE...]", Short: "Definir variáveis de ambiente", Args: cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				env := map[string]string{}
				for _, pair := range args {
					key, value, ok := strings.Cut(pair, "=")
					if !ok || key == "" {
						return usageErr("Formato inválido: " + pair + ". Use KEY=VALUE.")
					}
					env[key] = value
				}
				c, slug, err := a.appContext()
				if err != nil {
					return err
				}
				if err := c.Put(cmd.Context(), "/api/v1/apps/"+slug+"/env", map[string]any{"env": env}, nil); err != nil {
					return err
				}
				a.UI.Success("Variáveis de ambiente atualizadas.")
				return nil
			},
		},
		&cobra.Command{
			Use: "unset KEY [KEY...]", Short: "Remover variáveis de ambiente", Args: cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, slug, err := a.appContext()
				if err != nil {
					return err
				}
				escaped := make([]string, len(args))
				for i, k := range args {
					escaped[i] = url.PathEscape(k)
				}
				if err := c.Delete(cmd.Context(), "/api/v1/apps/"+slug+"/env/"+strings.Join(escaped, ","), nil); err != nil {
					return err
				}
				a.UI.Success("Variáveis removidas: " + strings.Join(args, ", "))
				return nil
			},
		},
	)
	return cmd
}
