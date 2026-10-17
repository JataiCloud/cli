package auth

import (
	"strings"
	"testing"
)

func TestOpenBrowserRefusesAnythingButHTTPURLs(t *testing.T) {
	for _, u := range []string{
		"-a Calculator", "--help", "file:///etc/passwd", "javascript:alert(1)", "smb://servidor/c", "", "jataicloud.com.br",
		"https://", "-https://jataicloud.com.br",
	} {
		err := OpenBrowser(u)
		if err == nil || !strings.Contains(err.Error(), "navegador") {
			t.Errorf("OpenBrowser(%q) err = %v", u, err)
		}
	}
}
