package cli

import (
	"context"
	"path/filepath"
	"time"

	"github.com/jataicloud/cli/internal/config"
	"github.com/jataicloud/cli/internal/update"
	"github.com/spf13/cobra"
)

// resolvedExecutable é o executável sem links simbólicos: é ele que se detecta
// e que se troca.
func (a *App) resolvedExecutable() (string, error) {
	exe, err := a.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

func (a *App) installMethod() update.Method {
	exe, err := a.resolvedExecutable()
	if err != nil {
		return update.MethodUnknown
	}
	return update.DetectMethod(exe, a.Paths.BinDir())
}

// maybeNotifyUpdate avisa de versão nova no máximo uma vez por dia, só em
// terminal interativo, e nunca bloqueia mais que 2 s.
func (a *App) maybeNotifyUpdate(ctx context.Context) {
	if !a.UI.Interactive || a.Getenv("JATAI_NO_UPDATE_CHECK") != "" || a.Version == "dev" || a.LatestVersion == nil {
		return
	}
	state := config.LoadState(a.Paths)
	if a.Now().Sub(state.LastUpdateCheck) < 24*time.Hour {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	latest, err := a.LatestVersion(cctx)
	state.LastUpdateCheck = a.Now()
	if err == nil {
		state.LatestVersion = latest
	}
	_ = config.SaveState(a.Paths, state)
	if err == nil && update.Newer(latest, a.Version) {
		a.UI.Warning("Nova versão da CLI: " + latest + " (você usa " + a.Version + "). Atualize com: " + update.Hint(a.installMethod()))
	}
}

func newUpdateCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use: "update", Short: "Atualizar a CLI", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			method := a.installMethod()
			switch method {
			case update.MethodScript:
			case update.MethodNpm, update.MethodNpx:
				a.UI.Info("Esta instalação veio do npm. Atualize com: " + update.Hint(method))
				return nil
			default:
				a.UI.Info("Esta cópia não foi instalada pelo script. Atualize com: " + update.Hint(method))
				return nil
			}
			latest, err := a.LatestVersion(cmd.Context())
			if err != nil {
				return err
			}
			if !update.Newer(latest, a.Version) {
				a.UI.Success("Você já está na versão mais recente (" + a.Version + ").")
				return nil
			}
			exe, err := a.resolvedExecutable()
			if err != nil {
				return err
			}
			a.UI.Info("Atualizando para " + latest + "...")
			if err := update.Install(cmd.Context(), a.httpClient(), a.DownloadBase, latest, exe); err != nil {
				return err
			}
			// A checagem diária já está em dia: o aviso não deve vir logo depois.
			_ = config.SaveState(a.Paths, config.State{LastUpdateCheck: a.Now(), LatestVersion: latest})
			a.UI.Success("jatai atualizada para " + latest + ".")
			return nil
		},
	}
}
