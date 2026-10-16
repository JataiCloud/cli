package ui

import (
	"fmt"
	"sync"
	"time"
)

type Spinner struct {
	ui   *UI
	msg  string
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// StartSpinner anima em terminal; fora dele, imprime a mensagem uma vez.
func (u *UI) StartSpinner(msg string) *Spinner {
	s := &Spinner{ui: u, msg: msg, stop: make(chan struct{}), done: make(chan struct{})}
	if !u.Interactive {
		u.Info(msg)
		close(s.done)
		return s
	}
	go s.run()
	return s
}

func (s *Spinner) run() {
	defer close(s.done)
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for i := 0; ; i++ {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			fmt.Fprintf(s.ui.Out, "\r%s %s", s.ui.Cyan(s.msg), frames[i%len(frames)])
		}
	}
}

// Stop para a animação e escreve o desfecho uma vez; chamadas seguintes não fazem nada,
// para que um `defer sp.Stop(...)` somado a um Stop explícito não derrube a CLI.
func (s *Spinner) Stop(final string) {
	if !s.ui.Interactive {
		return
	}
	s.once.Do(func() {
		close(s.stop)
		<-s.done
		fmt.Fprintf(s.ui.Out, "\r%s %s\n", s.ui.Cyan(s.msg), s.ui.Green(final))
	})
}
