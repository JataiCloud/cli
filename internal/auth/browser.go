package auth

import (
	"os/exec"
	"runtime"
)

// OpenBrowser abre a URL no navegador padrão sem esperar por ele. O Wait em
// segundo plano recolhe o processo quando ele termina, para que um processo
// de longa duração (como o `jatai mcp`) não acumule processos zumbis.
func OpenBrowser(url string) error {
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
