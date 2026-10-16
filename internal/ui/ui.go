// Package ui concentra a saída da CLI: cores (só em terminal e sem NO_COLOR),
// mensagens, prompts e spinner.
package ui

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

type UI struct {
	Out         io.Writer
	Err         io.Writer
	Color       bool
	Interactive bool
	Prompter    Prompter
}

func New() *UI {
	stdoutTTY := term.IsTerminal(int(os.Stdout.Fd()))
	interactive := stdoutTTY && term.IsTerminal(int(os.Stdin.Fd()))
	var prompter Prompter = NonInteractivePrompter{}
	if interactive {
		prompter = HuhPrompter{}
	}
	return &UI{
		Out: os.Stdout, Err: os.Stderr, Interactive: interactive, Prompter: prompter,
		Color: stdoutTTY && os.Getenv("NO_COLOR") == "",
	}
}

func (u *UI) paint(code, s string) string {
	if !u.Color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (u *UI) Green(s string) string  { return u.paint("32", s) }
func (u *UI) Red(s string) string    { return u.paint("31", s) }
func (u *UI) Yellow(s string) string { return u.paint("33", s) }
func (u *UI) Cyan(s string) string   { return u.paint("36", s) }
func (u *UI) Blue(s string) string   { return u.paint("34", s) }
func (u *UI) Dim(s string) string    { return u.paint("2", s) }
func (u *UI) Bold(s string) string   { return u.paint("1", s) }

func (u *UI) Println(msg string) { fmt.Fprintln(u.Out, msg) }
func (u *UI) Print(msg string)   { fmt.Fprint(u.Out, msg) }
func (u *UI) Success(msg string) { fmt.Fprintln(u.Out, u.Green(msg)) }
func (u *UI) Info(msg string)    { fmt.Fprintln(u.Out, u.Cyan(msg)) }
func (u *UI) Warning(msg string) { fmt.Fprintln(u.Out, u.Yellow(msg)) }
func (u *UI) Error(msg string)   { fmt.Fprintln(u.Err, u.Red(msg)) }
