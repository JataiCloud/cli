package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jataicloud/cli/internal/config"
	"github.com/jataicloud/cli/internal/ui"
	"github.com/spf13/cobra"
)

// Preços que a fatura cobra (Pricing::HEX no servidor).
var hexOptions = []ui.Option{
	{Label: "Hex Base — R$ 29/mês (1 grátis por time)", Value: "base"},
	{Label: "Hex Escala 1 — R$ 59/mês", Value: "escala_1"},
	{Label: "Hex Escala 2 — R$ 119/mês", Value: "escala_2"},
}

func stackOptions() []ui.Option {
	out := []ui.Option{}
	for _, key := range []string{"rails", "node", "static", "docker"} {
		out = append(out, ui.Option{Label: config.StackLabels[key], Value: key})
	}
	return out
}

const initNeedsTerminal = "`jatai init` precisa de um terminal interativo."

func newInitCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use: "init", Short: "Inicializar projeto no diretório atual", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return runInit(a) },
	}
}

func runInit(a *App) error {
	dir, err := a.Getwd()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, config.ProjectFile)); err == nil {
		ok, err := a.UI.Prompter.Confirm("Arquivo .jatai.yml já existe. Sobrescrever?")
		if err != nil || !ok {
			return a.ask(err, initNeedsTerminal)
		}
	}
	stack, err := chooseStack(a, dir)
	if err != nil {
		return a.ask(err, initNeedsTerminal)
	}
	branch, gitErr := a.Git(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if gitErr != nil {
		branch = ""
	}
	project, err := askProject(a, stack, branch)
	if err != nil {
		return a.ask(err, initNeedsTerminal)
	}
	preset, err := a.UI.Prompter.Select("Hex:", hexOptions)
	if err != nil {
		return a.ask(err, initNeedsTerminal)
	}
	project.Preset = preset
	if err := config.WriteProject(dir, project); err != nil {
		return err
	}
	a.UI.Success("Arquivo .jatai.yml criado com sucesso!")
	if project.App != project.Name {
		a.UI.Info("Identificador: " + project.App)
	}
	a.UI.Info("Branch: " + project.Branch)
	if stack != "static" {
		a.UI.Info("Banco: PG Base provisionado automaticamente no `jatai up`.")
	}
	a.UI.Info("Execute `jatai up` para criar e fazer deploy do app.")
	return nil
}

func chooseStack(a *App, dir string) (string, error) {
	stack := config.DetectStack(dir)
	if stack == "" {
		a.UI.Info("Não foi possível detectar o stack automaticamente.")
		return a.UI.Prompter.Select("Stack:", stackOptions())
	}
	label := config.StackLabels[stack]
	a.UI.Info("Stack detectado: " + label)
	ok, err := a.UI.Prompter.Confirm("Confirmar stack " + label + "?")
	if err != nil || ok {
		return stack, err
	}
	return a.UI.Prompter.Select("Stack:", stackOptions())
}

// askProject pergunta o nome até dar para derivar um identificador dele.
func askProject(a *App, stack, branch string) (config.Project, error) {
	for {
		name, err := a.UI.Prompter.Input("Nome do app:")
		if err != nil {
			return config.Project{}, err
		}
		if name == "" {
			continue
		}
		project, err := config.NewProject(name, stack, branch)
		if err == nil {
			return project, nil
		}
		if !errors.Is(err, config.ErrNoUsableName) {
			return config.Project{}, err
		}
		a.UI.Error(fmt.Sprintf("Não consegui gerar um identificador a partir de %q. Use letras ou números no nome.", name))
	}
}
