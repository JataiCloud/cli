#!/bin/sh
# scripts/install.sh — instala a CLI do Jataí a partir de github.com/jataicloud/cli.
# Uso: curl -fsSL https://jataicloud.com.br/install.sh | sh
# JATAI_VERSION=2.0.0 fixa a versão; JATAI_INSTALL_DIR troca o destino (padrão ~/.jatai/bin).
set -eu

REPO="jataicloud/cli"
INSTALL_DIR="${JATAI_INSTALL_DIR:-$HOME/.jatai/bin}"

fail() {
  printf 'Erro: %s\n' "$1" >&2
  exit 1
}

case "$(uname -s)" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *) fail "sistema não suportado: $(uname -s). No Windows: scoop install jatai" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch="amd64" ;;
  arm64 | aarch64) arch="arm64" ;;
  *) fail "arquitetura não suportada: $(uname -m)" ;;
esac

if [ -n "${JATAI_VERSION:-}" ]; then
  version="${JATAI_VERSION#v}"
else
  version="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" | sed 's#.*/tag/v##')"
fi
[ -n "$version" ] || fail "não consegui descobrir a última versão"

archive="jatai_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/v${version}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

printf 'Baixando jatai %s (%s/%s)...\n' "$version" "$os" "$arch"
curl -fsSL -o "$tmp/$archive" "$base/$archive" || fail "download falhou: $base/$archive"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || fail "download do checksums.txt falhou"

expected="$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1)"
[ -n "$expected" ] || fail "checksum de $archive não encontrado"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$archive" | cut -d' ' -f1)"
else
  actual="$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)"
fi
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
