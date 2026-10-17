// Package update descobre a última versão e troca o binário instalado pelo
// script (Homebrew, Scoop e npm têm os próprios comandos).
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
)

type Method int

const (
	MethodScript Method = iota
	MethodHomebrew
	MethodScoop
	MethodNpm
	MethodNpx
	MethodUnknown
)

// DetectMethod olha o caminho do executável (já sem links simbólicos). A
// comparação ignora barra e caixa: o mesmo teste vale para caminhos do Windows.
// npx e npm vêm antes de Homebrew e Scoop: um Node instalado por eles guarda os
// pacotes globais dentro da própria pasta (/opt/homebrew/lib/node_modules/...).
func DetectMethod(exePath, scriptBinDir string) Method {
	normalize := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, `\`, "/")) }
	p := normalize(exePath)
	switch {
	case strings.Contains(p, "/_npx/"):
		return MethodNpx
	case strings.Contains(p, "/node_modules/@jataicloud/cli-"):
		return MethodNpm
	case strings.Contains(p, "/cellar/"), strings.Contains(p, "/caskroom/"), strings.Contains(p, "/homebrew/"), strings.Contains(p, "/linuxbrew/"):
		return MethodHomebrew
	case strings.Contains(p, "/scoop/"):
		return MethodScoop
	case strings.HasPrefix(p, normalize(scriptBinDir)+"/"):
		return MethodScript
	default:
		return MethodUnknown
	}
}

func Hint(m Method) string {
	switch m {
	case MethodScript:
		return "jatai update"
	case MethodHomebrew:
		return "brew upgrade jatai"
	case MethodScoop:
		return "scoop update jatai"
	case MethodNpm:
		return "npm update -g jatai"
	case MethodNpx:
		return "npx -y jatai@latest"
	default:
		return "curl -fsSL https://jataicloud.com.br/install.sh | sh"
	}
}

func Newer(latest, current string) bool {
	l, okL := parse(latest)
	c, okC := parse(current)
	if !okL || !okC {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	if len(parts) != 3 {
		return out, false
	}
	for i, part := range parts {
		part, _, _ = strings.Cut(part, "-")
		n, err := strconv.Atoi(part)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// ArchiveName é o nome do pacote publicado nas releases do JataiCloud/cli.
func ArchiveName(version, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("jatai_%s_%s_%s.%s", version, goos, goarch, ext)
}

func Latest(ctx context.Context, client *http.Client, apiBase string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/repos/jataicloud/cli/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub respondeu %d", resp.StatusCode)
	}
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	return strings.TrimPrefix(r.TagName, "v"), nil
}

// Install baixa a versão, confere o SHA-256 contra o checksums.txt da release
// e só então troca o executável.
func Install(ctx context.Context, client *http.Client, downloadBase, version, exePath string) error {
	name := ArchiveName(version, runtime.GOOS, runtime.GOARCH)
	base := strings.TrimRight(downloadBase, "/") + "/v" + version
	archive, err := fetch(ctx, client, base+"/"+name)
	if err != nil {
		return err
	}
	sums, err := fetch(ctx, client, base+"/checksums.txt")
	if err != nil {
		return err
	}
	if err := verify(archive, sums, name); err != nil {
		return err
	}
	binary, err := extract(archive, runtime.GOOS)
	if err != nil {
		return err
	}
	return replace(exePath, binary)
}

func fetch(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download de %s respondeu %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

func verify(archive, sums []byte, name string) error {
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[1] == name {
			want = fields[0]
		}
	}
	if want == "" {
		return fmt.Errorf("checksum de %s não encontrado", name)
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return errors.New("checksum não confere — download corrompido ou adulterado")
	}
	return nil
}

func extract(archive []byte, goos string) ([]byte, error) {
	if goos == "windows" {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if f.Name == "jatai.exe" {
				return readZipEntry(f)
			}
		}
		return nil, errors.New("jatai.exe não encontrado no pacote")
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("binário jatai não encontrado no pacote")
		}
		if err != nil {
			return nil, err
		}
		if h.Name == "jatai" {
			return io.ReadAll(tr)
		}
	}
}

func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// No Windows o executável em uso não pode ser sobrescrito, mas pode ser
// renomeado: o antigo vira .old e o novo toma o lugar.
func replace(exePath string, binary []byte) error {
	tmp := exePath + ".new"
	if err := os.WriteFile(tmp, binary, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		old := exePath + ".old"
		_ = os.Remove(old)
		if err := os.Rename(exePath, old); err != nil {
			return err
		}
	}
	return os.Rename(tmp, exePath)
}
