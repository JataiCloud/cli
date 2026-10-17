package cli

import (
	"strings"

	"github.com/jataicloud/cli/internal/ui"
)

// Rótulos do Hex e do banco — o mesmo texto do painel e da fatura.
func hexLabel(tier string) string {
	if tier == "" {
		return "-"
	}
	return "Hex " + capitalize(strings.ReplaceAll(tier, "_", " "))
}

func pgLabel(plan string) string {
	if plan == "" {
		return "sem banco"
	}
	return "PG " + capitalize(strings.ReplaceAll(plan, "_", " "))
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	lower := strings.ToLower(s)
	return strings.ToUpper(lower[:1]) + lower[1:]
}

func colorizeStatus(u *ui.UI, s string) string {
	switch s {
	case "active", "live":
		return u.Green(s)
	case "sleeping":
		return u.Blue(s)
	case "failed", "error":
		return u.Red(s)
	case "building", "deploying", "provisioning":
		return u.Yellow(s)
	default:
		return s
	}
}

//nolint:unused // usado pelos comandos das próximas tarefas
func colorizeLevel(u *ui.UI, level string) string {
	switch level {
	case "error":
		return u.Red(level)
	case "warn":
		return u.Yellow(level)
	case "info":
		return u.Green(level)
	case "":
		return u.Dim("log")
	default:
		return u.Dim(level)
	}
}
