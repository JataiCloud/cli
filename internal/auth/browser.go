package auth

import (
	"errors"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

var errUnsafeURL = errors.New("só abro no navegador endereços http ou https")

// OpenBrowser abre a URL no navegador padrão sem esperar por ele. O Wait em
// segundo plano recolhe o processo quando ele termina, para que um processo
// de longa duração (como o `jatai mcp`) não acumule processos zumbis.
// Só http(s): a URL vem do servidor e vai para `open`/`xdg-open`, que também
// abrem arquivos locais e aceitariam um "-..." como opção.
func OpenBrowser(url string) error {
	if !browsable(url) {
		return errUnsafeURL
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func browsable(raw string) bool {
	if strings.HasPrefix(raw, "-") {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// CanOpenBrowser: não em sessão SSH, nem em Linux sem tela.
func CanOpenBrowser(getenv func(string) string) bool {
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return false
	}
	if runtime.GOOS == "linux" && getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
		return false
	}
	return true
}
