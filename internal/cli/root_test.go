package cli

import (
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	e := newTestEnv(t)
	if code := e.run("version"); code != 0 || e.out.String() != "jatai 2.0.0\n" {
		t.Fatalf("code = %d, out = %q", code, e.out)
	}
	e.out.Reset()
	if code := e.run("--version"); code != 0 || e.out.String() != "jatai 2.0.0\n" {
		t.Fatalf("code = %d, out = %q", code, e.out)
	}
}

func TestUsageErrorsExitWithTwo(t *testing.T) {
	e := newTestEnv(t)
	for _, args := range [][]string{{"nao-existe"}, {"version", "extra"}, {"login", "--flag-que-nao-existe"}} {
		if code := e.run(args...); code != 2 {
			t.Errorf("%v: code = %d", args, code)
		}
	}
	if !strings.Contains(e.err.String(), "nao-existe") {
		t.Fatalf("err = %q", e.err)
	}
}
