#!/bin/sh
# scripts/install.sh — instala a CLI do Jataí a partir de github.com/jataicloud/cli.
# Uso: curl -fsSL https://jataicloud.com.br/install.sh | sh
# JATAI_VERSION=2.0.0 fixa a versão; JATAI_INSTALL_DIR troca o destino (padrão ~/.jatai/bin).
set -eu

REPO="jataicloud/cli"
INSTALL_DIR="${JATAI_INSTALL_DIR:-$HOME/.jatai/bin}"
tmp=""

fail() {
  printf 'Erro: %s\n' "$1" >&2
  exit 1
}

detect_os() {
  case "$(uname -s)" in
    Darwin) echo "darwin" ;;
    Linux) echo "linux" ;;
    *) fail "sistema não suportado: $(uname -s). No Windows: scoop install jatai" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64)
      # Terminal rodando sob Rosetta num Mac com Apple Silicon: o binário nativo é o certo.
      if [ "$1" = "darwin" ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = "1" ]; then
        echo "arm64"
      else
        echo "amd64"
      fi
      ;;
    arm64 | aarch64) echo "arm64" ;;
    *) fail "arquitetura não suportada: $(uname -m)" ;;
  esac
}

latest_version() {
  curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" | sed 's#.*/tag/v##'
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d' ' -f1
  else
    fail "nenhuma ferramenta de SHA-256 encontrada (sha256sum ou shasum)"
  fi
}

main() {
  os="$(detect_os)"
  arch="$(detect_arch "$os")"

  if [ -n "${JATAI_VERSION:-}" ]; then
    version="${JATAI_VERSION#v}"
  else
    version="$(latest_version || true)"
  fi
  printf '%s' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' ||
    fail "não encontrei uma versão válida da CLI publicada (obtido: '${version}'). Informe uma com JATAI_VERSION=2.0.0"

  archive="jatai_${version}_${os}_${arch}.tar.gz"
  base="https://github.com/$REPO/releases/download/v${version}"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  printf 'Baixando jatai %s (%s/%s)...\n' "$version" "$os" "$arch"
  curl -fsSL -o "$tmp/$archive" "$base/$archive" || fail "download falhou: $base/$archive"
  curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || fail "download do checksums.txt falhou"

  expected="$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1)"
  [ -n "$expected" ] || fail "checksum de $archive não encontrado"
  actual="$(sha256_of "$tmp/$archive")"
  [ "$expected" = "$actual" ] || fail "checksum não confere — download corrompido ou adulterado"

  tar -xzf "$tmp/$archive" -C "$tmp" jatai
  mkdir -p "$INSTALL_DIR"
  mv "$tmp/jatai" "$INSTALL_DIR/jatai"
  chmod 0755 "$INSTALL_DIR/jatai"

  printf 'jatai %s instalada em %s\n' "$version" "$INSTALL_DIR/jatai"
  case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *) printf '\nAdicione ao seu PATH (no ~/.zshrc ou ~/.bashrc):\n  export PATH="%s:%s"\n' "$INSTALL_DIR" "\$PATH" ;;
  esac
  printf '\nPróximo passo: jatai login\n'
}

# Tudo dentro de main, chamada na última linha: se o `curl | sh` for cortado
# no meio, o shell não executa um script pela metade.
main "$@"
