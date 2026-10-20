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
		return "npm update -g jatai-cli"
	case MethodNpx:
		return "npx -y jatai-cli@latest"
	default:
		return "curl -fsSL https://jataicloud.com.br/install.sh | sh"
	}
}

// Newer compara como o SemVer: o núcleo (maior.menor.correção) primeiro; no
// mesmo núcleo, a versão final é mais nova que qualquer pré-lançamento
// (2.0.0 > 2.0.0-rc.3), e os pré-lançamentos se comparam por partes, com as
// numéricas como números (rc.10 > rc.9).
func Newer(latest, current string) bool {
	l, lPre, okL := parse(latest)
	c, cPre, okC := parse(current)
	if !okL || !okC {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return comparePrerelease(lPre, cPre) > 0
}

func parse(v string) ([3]int, string, bool) {
	var out [3]int
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "+")
	core, pre, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return out, "", false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return out, "", false
		}
		out[i] = n
	}
	return out, pre, true
}

// comparePrerelease devolve >0 se a for mais novo que b. Vazio (versão
// final) vence qualquer pré-lançamento.
func comparePrerelease(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if c := compareIdentifier(as[i], bs[i]); c != 0 {
			return c
		}
	}
	return len(as) - len(bs)
}

// compareIdentifier: numéricos como números e antes dos alfanuméricos.
func compareIdentifier(a, b string) int {
	an, aErr := strconv.Atoi(a)
	bn, bErr := strconv.Atoi(b)
	switch {
	case aErr == nil && bErr == nil:
		return an - bn
	case aErr == nil:
		return -1
	case bErr == nil:
		return 1
	default:
		return strings.Compare(a, b)
	}
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
	if r.TagName == "" {
		return "", errors.New("o GitHub não informou a versão mais recente")
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
			if f.Name == "jatai.exe" && f.Mode().IsRegular() {
				return nonEmpty(readZipEntry(f))
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
		if h.Name == "jatai" && h.Typeflag == tar.TypeReg {
			return nonEmpty(io.ReadAll(tr))
		}
	}
}

// nonEmpty recusa um binário vazio: trocar o executável por zero bytes o quebra.
func nonEmpty(data []byte, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("binário vazio no pacote")
	}
	return data, nil
}

func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// replace troca o executável: no Windows o em uso não pode ser sobrescrito,
// mas pode ser renomeado.
func replace(exePath string, binary []byte) error {
	if runtime.GOOS == "windows" {
		return replaceWindowsStyle(exePath, binary, os.Rename)
	}
	tmp, err := stage(exePath, binary)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, exePath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func stage(exePath string, binary []byte) (string, error) {
	tmp := exePath + ".new"
	if err := os.WriteFile(tmp, binary, 0o755); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// replaceWindowsStyle: o antigo vira .old e o novo toma o lugar; se a segunda
// troca falhar, o antigo volta, para a CLI nunca ficar sem executável.
func replaceWindowsStyle(exePath string, binary []byte, rename func(oldpath, newpath string) error) error {
	old := exePath + ".old"
	_ = os.Remove(old)
	tmp, err := stage(exePath, binary)
	if err != nil {
		return err
	}
	if err := rename(exePath, old); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := rename(tmp, exePath); err != nil {
		_ = rename(old, exePath)
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
