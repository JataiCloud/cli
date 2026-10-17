#!/bin/sh
# scripts/smoke.sh — roda o binário recém-compilado contra produção, com um
# time e um app estático de teste. Rodar antes de cada tag de release.
# Uso: JATAI_TOKEN=jat_… JATAI_SMOKE_APP=smoke-site scripts/smoke.sh
set -eu
: "${JATAI_TOKEN:?defina JATAI_TOKEN (token do time de teste, criado em /access_tokens)}"
: "${JATAI_SMOKE_APP:?defina JATAI_SMOKE_APP (slug de um app estático de teste)}"

root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
server=""
trap '[ -z "$server" ] || kill "$server" 2> /dev/null || true; rm -rf "$work"' EXIT
trap 'exit 130' INT TERM

# smoke_mcp faz o aperto de mãos do MCP com `jatai mcp` e exige que a resposta
# do tools/list (id 2) traga a ferramenta jatai_publicar. A resposta do
# initialize (id 1) não serve: o texto de instruções também cita o nome. O
# servidor lê de um FIFO que o script abre em leitura e escrita antes de
# lançá-lo (assim nada bloqueia, mesmo que o servidor morra cedo); o servidor
# sai quando o script fecha esse lado. A espera é limitada a 10 s. A lista de
# ferramentas não usa a API.
smoke_mcp() {
	init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'
	initialized='{"jsonrpc":"2.0","method":"notifications/initialized"}'
	list='{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
	reply_start='^{"jsonrpc":"2.0","id":2,'
	mkfifo "$work/mcp.in"
	exec 3<> "$work/mcp.in"
	"$1" mcp < "$work/mcp.in" 3>&- > "$work/mcp.out" 2> "$work/mcp.err" &
	server=$!
	printf '%s\n' "$init" "$initialized" "$list" >&3
	tries=0
	while [ "$tries" -lt 10 ] && ! grep -q "$reply_start" "$work/mcp.out"; do
		tries=$((tries + 1))
		sleep 1
	done
	exec 3>&-
	kill "$server" 2> /dev/null || true
	wait "$server" 2> /dev/null || true
	server=""
	reply="$(grep "$reply_start" "$work/mcp.out" | head -n 1)"
	case "$reply" in
	*'"name":"jatai_publicar"'*) printf 'mcp ok\n' ;;
	"")
		printf 'smoke: o jatai mcp não respondeu ao tools/list em 10 s. Saída:\n%s\n%s\n' \
			"$(cat "$work/mcp.out")" "$(cat "$work/mcp.err")" >&2
		return 1
		;;
	*)
		printf 'smoke: o tools/list do jatai mcp não traz jatai_publicar. Resposta:\n%s\n' "$reply" >&2
		return 1
		;;
	esac
}

(cd "$root" && go build -o "$work/jatai" ./cmd/jatai)
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
