package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	ProjectFile       = ".jatai.yml"
	LegacyProjectFile = ".brasa.yml"
)

var ErrNoProject = errors.New("Arquivo .jatai.yml não encontrado. Execute `jatai init` primeiro.")

type Release struct {
	Command string `yaml:"command,omitempty"`
}

// Project é o .jatai.yml. `app` é o identificador (slug) usado nas URLs da
// API; `name` é o nome de exibição (nos arquivos antigos só havia `app`).
type Project struct {
	App     string   `yaml:"app"`
	Name    string   `yaml:"name,omitempty"`
	Stack   string   `yaml:"stack,omitempty"`
	Preset  string   `yaml:"preset,omitempty"`
	Region  string   `yaml:"region,omitempty"`
	Branch  string   `yaml:"branch,omitempty"`
	Release *Release `yaml:"release,omitempty"`
}

func (p Project) DisplayName() string {
	if p.Name != "" {
		return p.Name
	}
	return p.App
}

func projectPath(dir string) string {
	current := filepath.Join(dir, ProjectFile)
	if _, err := os.Stat(current); err == nil {
		return current
	}
	legacy := filepath.Join(dir, LegacyProjectFile)
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return current
}

func LoadProject(dir string) (Project, string, error) {
	path := projectPath(dir)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Project{}, path, ErrNoProject
	}
	if err != nil {
		return Project{}, path, err
	}
	var p Project
	if err := yaml.Unmarshal(data, &p); err != nil {
		return Project{}, path, fmt.Errorf("%s inválido: %w", filepath.Base(path), err)
	}
	return p, path, nil
}

func WriteProject(dir string, p Project) error {
	data, err := encodeYAML(p)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ProjectFile), data, 0o644)
}

// Dois espaços de recuo, como o arquivo que a gem gravava (o padrão do
// yaml.v3 é quatro, e reformataria o arquivo do cliente).
func encodeYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// PersistSlug grava o slug que o servidor devolveu, preservando as demais
// chaves (static:, release:, …) — o arquivo é do cliente. Arquivo antigo, em
// que `app` guardava o nome de exibição, ganha `name` com esse valor.
func PersistSlug(path, slug string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s não é um mapa YAML", filepath.Base(path))
	}
	root := doc.Content[0]
	if lookup(root, "name") == nil {
		if app := lookup(root, "app"); app != nil {
			setKey(root, "name", app.Value)
		}
	}
	setKey(root, "app", slug)
	out, err := encodeYAML(&doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func lookup(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func setKey(mapping *yaml.Node, key, value string) {
	if node := lookup(mapping, key); node != nil {
		node.Kind, node.Tag, node.Value, node.Style = yaml.ScalarNode, "!!str", value, 0
		return
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value})
}
