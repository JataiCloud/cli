package ui

import (
	"context"
	"errors"
	"strings"

	"github.com/charmbracelet/huh"
)

var ErrNotInteractive = errors.New("prompt indisponível fora de um terminal interativo")

type Option struct{ Label, Value string }

type Prompter interface {
	Confirm(question string) (bool, error)
	Input(question string) (string, error)
	Select(question string, options []Option) (string, error)
}

type HuhPrompter struct{}

func (HuhPrompter) Confirm(question string) (bool, error) {
	var ok bool
	err := huh.NewConfirm().Title(question).Affirmative("Sim").Negative("Não").Value(&ok).Run()
	return ok, aborted(err)
}

func (HuhPrompter) Input(question string) (string, error) {
	var value string
	err := huh.NewInput().Title(question).Value(&value).Run()
	return strings.TrimSpace(value), aborted(err)
}

func (HuhPrompter) Select(question string, options []Option) (string, error) {
	var value string
	opts := make([]huh.Option[string], len(options))
	for i, o := range options {
		opts[i] = huh.NewOption(o.Label, o.Value)
	}
	err := huh.NewSelect[string]().Title(question).Options(opts...).Value(&value).Run()
	return value, aborted(err)
}

// Ctrl-C num prompt é interrupção, como no resto da CLI (código 130).
func aborted(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		return context.Canceled
	}
	return err
}

// NonInteractivePrompter é o de CI e pipes: recusa e deixa o comando dizer
// qual flag dispensa o prompt.
type NonInteractivePrompter struct{}

func (NonInteractivePrompter) Confirm(string) (bool, error)            { return false, ErrNotInteractive }
func (NonInteractivePrompter) Input(string) (string, error)            { return "", ErrNotInteractive }
func (NonInteractivePrompter) Select(string, []Option) (string, error) { return "", ErrNotInteractive }
