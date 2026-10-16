package ui

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

func newTestUI(color, interactive bool) (*UI, *bytes.Buffer, *bytes.Buffer) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	return &UI{Out: out, Err: errb, Color: color, Interactive: interactive, Prompter: NonInteractivePrompter{}}, out, errb
}

func TestColorsOnlyWhenEnabled(t *testing.T) {
	plain, _, _ := newTestUI(false, false)
	if plain.Green("ok") != "ok" {
		t.Fatal("expected no ANSI without color")
	}
	colored, _, _ := newTestUI(true, false)
	if got := colored.Green("ok"); got != "\x1b[32mok\x1b[0m" {
		t.Fatalf("got %q", got)
	}
}

func TestMessagesGoToTheRightStream(t *testing.T) {
	u, out, errb := newTestUI(false, false)
	u.Success("feito")
	u.Error("falhou")
	if out.String() != "feito\n" || errb.String() != "falhou\n" {
		t.Fatalf("out=%q err=%q", out, errb)
	}
}

func TestNonInteractivePrompterRefuses(t *testing.T) {
	var p NonInteractivePrompter
	if _, err := p.Confirm("?"); !errors.Is(err, ErrNotInteractive) {
		t.Fatalf("err = %v", err)
	}
	if _, err := p.Input("?"); !errors.Is(err, ErrNotInteractive) {
		t.Fatalf("err = %v", err)
	}
	if _, err := p.Select("?", nil); !errors.Is(err, ErrNotInteractive) {
		t.Fatalf("err = %v", err)
	}
}

func TestCtrlCInAPromptInterruptsTheCommand(t *testing.T) {
	if !errors.Is(aborted(huh.ErrUserAborted), context.Canceled) {
		t.Fatal("Ctrl-C in a prompt should cancel the command")
	}
}

func TestSpinnerOutsideTerminalPrintsOnceAndStops(t *testing.T) {
	u, out, _ := newTestUI(false, false)
	s := u.StartSpinner("Enviando 12KB...")
	s.Stop("enviado!")
	if strings.Count(out.String(), "Enviando 12KB...") != 1 {
		t.Fatalf("out = %q", out)
	}
}

func TestStopTwiceInTerminalWritesFinalOnceWithoutPanic(t *testing.T) {
	u, out, _ := newTestUI(false, true)
	s := u.StartSpinner("Enviando 12KB...")
	s.Stop("enviado!")
	s.Stop("enviado!")
	if strings.Count(out.String(), "enviado!") != 1 {
		t.Fatalf("out = %q", out)
	}
}
