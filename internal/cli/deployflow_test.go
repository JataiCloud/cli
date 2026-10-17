package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jataicloud/cli/internal/ui"
)

func newTermReporter() (*termReporter, *bytes.Buffer, *bytes.Buffer) {
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	return &termReporter{ui: &ui.UI{Out: out, Err: errOut}}, out, errOut
}

func TestTermReporterPrintsPollingProgressLikeTheGem(t *testing.T) {
	r, out, _ := newTermReporter()

	r.Polled("queued", false, time.Second)
	r.Polled("queued", false, 2*time.Second)
	r.Polled("", true, 3*time.Second)
	r.Polled("building", false, 4*time.Second)

	// rótulo (que já termina em "..."), um "." por consulta, "!" na falha de rede
	want := "\n       Na fila.....!\n       Construindo imagem...."
	if got := out.String(); got != want {
		t.Errorf("out = %q, want %q", got, want)
	}
}

func TestTermReporterShowsElapsedTimeEveryTwelvePollsCountingNetworkErrors(t *testing.T) {
	r, out, _ := newTermReporter()

	for i := 0; i < 12; i++ {
		r.Polled("", true, 0) // 12 falhas de rede contam como consultas
	}
	r.Polled("building", false, 125*time.Second) // 13ª consulta, índice 12

	if got := out.String(); !strings.HasSuffix(got, "Construindo imagem.... (2m5s)") {
		t.Errorf("out = %q", got)
	}
}

func TestTermReporterStartsANewPollingSequenceAfterOtherOutput(t *testing.T) {
	r, out, _ := newTermReporter()
	r.Polled("provisioning", false, time.Second)
	out.Reset()

	r.Info("Iniciando primeiro deploy...")
	r.Polled("provisioning", false, time.Second)

	if got := out.String(); !strings.Contains(got, "\n       Provisionando...") {
		t.Errorf("a new sequence must print the label again, out = %q", got)
	}
}

func TestTermReporterWarnGoesToStderr(t *testing.T) {
	r, out, errOut := newTermReporter()

	r.Warn("aviso")

	if out.Len() != 0 || errOut.String() != "aviso\n" {
		t.Errorf("out = %q, err = %q", out, errOut)
	}
}
