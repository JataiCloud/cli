package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// releaseTargets são as combinações que o GoReleaser publica (.goreleaser.yaml:
// darwin e linux em amd64 e arm64, windows só em amd64).
var releaseTargets = [][2]string{
	{"darwin", "amd64"}, {"darwin", "arm64"},
	{"linux", "amd64"}, {"linux", "arm64"},
	{"windows", "amd64"},
}

type goreleaserConfig struct {
	Archives []struct {
		NameTemplate    string   `yaml:"name_template"`
		Formats         []string `yaml:"formats"`
		WrapInDirectory any      `yaml:"wrap_in_directory"`
		FormatOverrides []struct {
			Goos    string   `yaml:"goos"`
			Formats []string `yaml:"formats"`
		} `yaml:"format_overrides"`
	} `yaml:"archives"`
}

// O .goreleaser.yaml e o ArchiveName/extract desta CLI são um contrato: o
// `jatai update` e os instaladores procuram exatamente estes nomes.
func TestGoreleaserArchivesMatchArchiveName(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg goreleaserConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Archives) != 1 {
		t.Fatalf("archives = %d, want 1", len(cfg.Archives))
	}
	a := cfg.Archives[0]
	if a.WrapInDirectory != nil {
		t.Errorf("wrap_in_directory = %v: o binário precisa ficar na raiz do pacote", a.WrapInDirectory)
	}
	if len(a.Formats) != 1 || a.Formats[0] != "tar.gz" {
		t.Errorf("formats = %v, want [tar.gz]", a.Formats)
	}
	if len(a.FormatOverrides) != 1 || a.FormatOverrides[0].Goos != "windows" ||
		len(a.FormatOverrides[0].Formats) != 1 || a.FormatOverrides[0].Formats[0] != "zip" {
		t.Errorf("format_overrides = %+v, want windows -> zip", a.FormatOverrides)
	}

	for _, target := range releaseTargets {
		goos, goarch := target[0], target[1]
		stem := strings.NewReplacer(
			"{{ .ProjectName }}", "jatai",
			"{{ .Version }}", "2.0.0",
			"{{ .Os }}", goos,
			"{{ .Arch }}", goarch,
		).Replace(a.NameTemplate)
		ext := ".tar.gz"
		if goos == "windows" {
			ext = ".zip"
		}
		if got, want := stem+ext, ArchiveName("2.0.0", goos, goarch); got != want {
			t.Errorf("%s/%s: GoReleaser gera %q, ArchiveName espera %q", goos, goarch, got, want)
		}
	}
}

// Depois de `goreleaser release --snapshot --clean`, confere os pacotes de
// verdade: nome, checksum no checksums.txt e binário na raiz, lido pelo
// mesmo extract do `jatai update`. Roda com JATAI_DIST_DIR=dist (o release.yml
// faz isso antes de publicar); sem a variável, não se aplica.
func TestBuiltArchivesAreInstallable(t *testing.T) {
	dir := os.Getenv("JATAI_DIST_DIR")
	if dir == "" {
		t.Skip("JATAI_DIST_DIR não definido")
	}
	dir = filepath.Join("..", "..", dir)
	version := os.Getenv("JATAI_DIST_VERSION")
	if version == "" {
		version = distVersion(t, dir)
	}
	sums, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range releaseTargets {
		name := ArchiveName(version, target[0], target[1])
		archive, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if err := verify(archive, sums, name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if binary, err := extract(archive, target[0]); err != nil || len(binary) == 0 {
			t.Errorf("%s: extract = %d bytes, %v", name, len(binary), err)
		}
	}
}

func distVersion(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil || meta.Version == "" {
		t.Fatalf("metadata.json sem version: %v", err)
	}
	return meta.Version
}
