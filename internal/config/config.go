// Package config guarda o que a CLI mantém na máquina: endereço da API,
// credenciais, arquivo do projeto e estado da checagem de versão.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const DefaultAPIURL = "https://api.jataicloud.com.br"

type Paths struct{ Home string }

func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	return Paths{Home: home}, err
}

// ErrNoHome: sem a pasta pessoal, os arquivos da CLI cairiam relativos ao
// diretório atual — a credencial iria parar dentro do projeto e no deploy.
var ErrNoHome = errors.New("Não encontrei a pasta pessoal deste usuário (variável HOME; no Windows, USERPROFILE), " +
	"onde a CLI do Jataí guarda a credencial. Defina HOME com um caminho absoluto e tente de novo.")

// known diz se Home é um caminho absoluto: só então os arquivos da CLI têm
// lugar certo para ficar.
func (p Paths) known() bool { return p.Home != "" && filepath.IsAbs(p.Home) }

func (p Paths) Dir() string             { return filepath.Join(p.Home, ".jatai") }
func (p Paths) CredentialsFile() string { return filepath.Join(p.Dir(), "credentials.json") }
func (p Paths) LegacyCredentialsFile() string {
	return filepath.Join(p.Home, ".brasa", "credentials.json")
}
func (p Paths) StateFile() string { return filepath.Join(p.Dir(), "state.json") }
func (p Paths) BinDir() string    { return filepath.Join(p.Dir(), "bin") }

// APIURL: JATAI_API_URL, depois BRASA_API_URL (legado da gem), depois o padrão.
func APIURL(getenv func(string) string) string {
	for _, key := range []string{"JATAI_API_URL", "BRASA_API_URL"} {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return strings.TrimRight(v, "/")
		}
	}
	return DefaultAPIURL
}

type credentials struct {
	Token string `json:"token"`
}

// Token: JATAI_TOKEN (CI) tem prioridade; depois o arquivo da CLI; depois o
// arquivo da gem antiga (~/.brasa), que continua valendo na transição. Sem a
// pasta pessoal, nenhum arquivo é lido.
func Token(p Paths, getenv func(string) string) string {
	if t := strings.TrimSpace(getenv("JATAI_TOKEN")); t != "" {
		return t
	}
	if !p.known() {
		return ""
	}
	for _, file := range []string{p.CredentialsFile(), p.LegacyCredentialsFile()} {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var c credentials
		if json.Unmarshal(data, &c) == nil && c.Token != "" {
			return c.Token
		}
	}
	return ""
}

// SaveToken grava o token com permissão restrita, mesmo quando o diretório
// ou o arquivo já existem com permissão mais ampla.
func SaveToken(p Paths, token string) error {
	if !p.known() {
		return ErrNoHome
	}
	if err := os.MkdirAll(p.Dir(), 0o700); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(p.Dir(), 0o700); err != nil {
			return err
		}
	}
	data, err := json.Marshal(credentials{Token: token})
	if err != nil {
		return err
	}
	return writeAtomic(p.Dir(), p.CredentialsFile(), data)
}

// writeAtomic escreve num temporário criado com 0600 e troca o destino por
// rename: o token nunca cai num arquivo com permissão mais ampla, nem quando
// credentials.json já existia com 0644.
func writeAtomic(dir, target string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".credentials-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	err = tmp.Chmod(0o600)
	if err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(name, target)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

func ClearToken(p Paths) error {
	if !p.known() {
		return ErrNoHome
	}
	for _, file := range []string{p.CredentialsFile(), p.LegacyCredentialsFile()} {
		if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
