#!/bin/sh
# scripts/smoke.sh — roda o binário recém-compilado contra produção, com um
# time e um app estático de teste. Rodar antes de cada tag de release.
# Uso: JATAI_TOKEN=jat_… JATAI_SMOKE_APP=smoke-site scripts/smoke.sh
set -eu
: "${JATAI_TOKEN:?defina JATAI_TOKEN (token do time de teste, criado em /access_tokens)}"
: "${JATAI_SMOKE_APP:?defina JATAI_SMOKE_APP (slug de um app estático de teste)}"

root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# smoke_mcp faz o aperto de mãos do MCP com `jatai mcp` e exige que a lista de
# ferramentas traga jatai_publicar. O stdin fica aberto até a resposta chegar
# (o servidor sai quando o stdin fecha); a lista de ferramentas não usa a API.
smoke_mcp() {
	init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'
	initialized='{"jsonrpc":"2.0","method":"notifications/initialized"}'
	list='{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
	out="$({ printf '%s\n' "$init" "$initialized" "$list"; sleep 2; } | "$1" mcp)" || true
	case "$out" in
	*jatai_publicar*) printf 'mcp ok\n' ;;
	*)
		printf 'smoke: o jatai mcp não listou jatai_publicar. Saída:\n%s\n' "$out" >&2
		return 1
		;;
	esac
}

go build -o "$work/jatai" "$root/cmd/jatai"
mkdir "$work/site"
cp -R "$root/testdata/smoke-site/." "$work/site/"
printf 'app: %s\nstack: static\n' "$JATAI_SMOKE_APP" > "$work/site/.jatai.yml"
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$work/site/build.txt"

cd "$work/site"
"$work/jatai" version
smoke_mcp "$work/jatai"
"$work/jatai" status
"$work/jatai" env list
"$work/jatai" logs --tail 5
"$work/jatai" deploy
printf '\nsmoke ok\n'
