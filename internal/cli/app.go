package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/jataicloud/cli/internal/api"
	"github.com/jataicloud/cli/internal/auth"
	"github.com/jataicloud/cli/internal/config"
	"github.com/jataicloud/cli/internal/slug"
	"github.com/jataicloud/cli/internal/ui"
)

// App reúne tudo o que os comandos usam do mundo externo — tudo injetável,
// para cada comando ser testado contra um servidor falso.
type App struct {
	UI              *ui.UI
	Paths           config.Paths
	Version         string
	Getenv          func(string) string
	Getwd           func() (string, error)
	Now             func() time.Time
	Sleep           func(context.Context, time.Duration) error
	HTTP            *http.Client
	RetryDelay      time.Duration
	PollInterval    time.Duration
	ReconnectDelays []time.Duration
	WelcomeTimeout  time.Duration
	Git             func(dir string, args ...string) (string, error)
	Hostname        func() (string, error)
	OpenBrowser     func(url string) error
	CanOpenBrowser  func() bool
}

func NewApp(version string) *App {
	paths, _ := config.DefaultPaths()
	return &App{
		UI: ui.New(), Paths: paths, Version: version, Getenv: os.Getenv, Getwd: os.Getwd, Now: time.Now,
		Sleep: sleepCtx, RetryDelay: time.Second, PollInterval: 5 * time.Second,
		ReconnectDelays: []time.Duration{time.Second, 3 * time.Second, 10 * time.Second},
		WelcomeTimeout:  10 * time.Second, Git: runGit, Hostname: os.Hostname,
		OpenBrowser:    auth.OpenBrowser,
		CanOpenBrowser: func() bool { return auth.CanOpenBrowser(os.Getenv) },
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func (a *App) userAgent() string {
	return fmt.Sprintf("jatai-cli/%s (%s; %s)", a.Version, runtime.GOOS, runtime.GOARCH)
}

func (a *App) client(token string) (*api.Client, error) {
	c, err := api.New(config.APIURL(a.Getenv), token, a.userAgent())
	if err != nil {
		return nil, err
	}
	if a.HTTP != nil {
		c.HTTP = a.HTTP
	}
	c.RetryDelay = a.RetryDelay
	c.Debug = a.debugWriter()
	return c, nil
}

// debugWriter: com JATAI_DEBUG=1, requisições e frames do WebSocket vão para o
// stderr (sem o token, que só viaja em cabeçalho).
func (a *App) debugWriter() io.Writer {
	if a.Getenv("JATAI_DEBUG") == "1" {
		return a.UI.Err
	}
	return nil
}

func (a *App) authedClient() (*api.Client, error) {
	token := config.Token(a.Paths, a.Getenv)
	if token == "" {
		return nil, userErr("Não autenticado. Execute `jatai login` primeiro.")
	}
	return a.client(token)
}

func (a *App) currentProject() (config.Project, string, error) {
	dir, err := a.Getwd()
	if err != nil {
		return config.Project{}, "", err
	}
	p, path, err := config.LoadProject(dir)
	if errors.Is(err, config.ErrNoProject) {
		return p, path, userErr(err.Error())
	}
	return p, path, err
}

func (a *App) appSlug(p config.Project) (string, error) {
	s, ok := slug.From(p.App)
	if !ok {
		return "", userErr(fmt.Sprintf("`app` inválido no .jatai.yml (%q). Use letras, números e hífen, ou rode `jatai init` novamente.", p.App))
	}
	return s, nil
}

// appContext: login + projeto + slug, na ordem em que a gem checava.
//
//nolint:unused // usado pelos comandos das próximas tarefas
func (a *App) appContext() (*api.Client, string, error) {
	c, err := a.authedClient()
	if err != nil {
		return nil, "", err
	}
	p, _, err := a.currentProject()
	if err != nil {
		return nil, "", err
	}
	s, err := a.appSlug(p)
	if err != nil {
		return nil, "", err
	}
	return c, s, nil
}

// ask converte "sem terminal" na instrução de qual flag usar.
func (a *App) ask(err error, hint string) error {
	if errors.Is(err, ui.ErrNotInteractive) {
		return userErr(hint)
	}
	return err
}
