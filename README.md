# jatai — CLI da Jataí

Deploy de apps Rails, Node.js, sites estáticos ou qualquer Dockerfile na nuvem brasileira.

## Instalação

- macOS e Linux: `curl -fsSL https://jataicloud.com.br/install.sh | sh`
- macOS (Homebrew): `brew install jataicloud/tap/jatai`
- npm: `npx jatai-cli` ou `npm install -g jatai-cli`; o comando instalado é `jatai` (o binário vem de pacotes `@jataicloud/cli-*`; não use `--omit=optional`)
- Windows (Scoop): `scoop bucket add jataicloud https://github.com/JataiCloud/scoop-bucket` e `scoop install jatai`
- Windows (PowerShell): `irm https://jataicloud.com.br/install.ps1 | iex`
- Do código-fonte (Go 1.27+): `go install github.com/jataicloud/cli/cmd/jatai@latest`, ou `go build -o jatai ./cmd/jatai` num clone deste repositório

## Uso

    jatai login
    jatai init
    jatai up

## Jataí + IA (MCP)

O `jatai mcp` é um servidor MCP local (stdio): agentes como Claude Code, Cursor, Codex e Gemini publicam a pasta do projeto, acompanham o deploy, leem os logs e cuidam das variáveis de ambiente. Num cliente MCP:

    {
      "mcpServers": {
        "jatai": { "command": "npx", "args": ["-y", "jatai-cli@latest", "mcp"] }
      }
    }

Configuração de cada agente (inclusive no Windows): https://jataicloud.com.br/docs/ia

Documentação: https://jataicloud.com.br/docs/ia

## Contrato com o servidor

`internal/cli/testdata/api` guarda respostas reais da API, gravadas pelos request specs da plataforma. Para regenerar, no repositório da plataforma:

    CLI_FIXTURES_DIR=<este repositório>/internal/cli/testdata/api bundle exec rspec spec/requests/api/v1 --order defined

`go test ./...` confere que cada resposta traz o que a CLI lê (`internal/cli`, `internal/mcpserver` e `internal/deployflow` têm um `contract_test.go`). Regenere antes de cada release e revise os arquivos (o repositório é público).

## Release

Antes de criar a tag: regenere o contrato (seção acima), revise os arquivos e rode `go test ./...`; depois rode o smoke contra produção, com o token de um time de teste (`/access_tokens`) e o slug de um app estático de teste (criado com `jatai init` + `jatai up`):

    JATAI_TOKEN=… JATAI_SMOKE_APP=… scripts/smoke.sh

O script compila o binário, confere que `jatai mcp` lista `jatai_publicar` e roda `version`, `status`, `env list`, `logs` e `deploy` de `testdata/smoke-site`. Cada comando sai com 0 e a última linha é `smoke ok`. Só então `git tag vX.Y.Z && git push origin vX.Y.Z`.

Uma tag `vX.Y.Z` (ou `vX.Y.Z-rc.N`) dispara `.github/workflows/release.yml`, em dois jobs:

1. `release`: testes, GoReleaser (pacotes `jatai_<versão>_<os>_<arch>.tar.gz`, `.zip` no Windows, `checksums.txt`, `install.sh` e `install.ps1`, cask do Homebrew e manifesto do Scoop), attestation de provenance dos pacotes e upload do `dist/` como artefato do workflow.
2. `npm` (depende do `release`): testes do npm, conferência dos pacotes gerados e publicação no npm (`node scripts/npm-packages.mjs`, plataformas primeiro e `jatai-cli` por último).

- Os pacotes têm o binário na raiz e esse nome é um contrato com `jatai update` e com os instaladores (`internal/update/release_contract_test.go` confere o `.goreleaser.yaml`).
- **Se o npm falhar** (token ausente ou expirado, queda do registro), corrija a causa e use "Re-run failed jobs" na execução do workflow: só o job `npm` roda de novo, com o `dist/` do artefato, e os pacotes que já subiram são pulados. Não crie outra tag. Se o job `release` falhar, "Re-run failed jobs" também serve: o GoReleaser substitui os arquivos já enviados à release (`replace_existing_artifacts`).
- Tags `-rc.N` são releases normais do GitHub, sem marca de pré-lançamento, para que `/releases/latest` (usado por `install.sh`, `install.ps1` e `jatai update`) tenha o que devolver antes da v2.0.0.
- **Nome do pacote principal**: é `jatai-cli`, porque o npm bloqueia o nome `jatai` (parecido com jotai/jiti). O comando instalado segue `jatai`, e os pacotes por plataforma seguem `@jataicloud/cli-<plataforma>`.
- **dist-tags do npm**: versões com `-` saem em `next`; as demais, em `latest`. O `latest` de `jatai-cli` aponta para o placeholder 0.0.1 (publicado em julho) até a v2.0.0 sair; a primeira publicação de um pacote novo no npm também define `latest`, o que vale para os pacotes `@jataicloud/cli-*`. Para ensaios use `jatai-cli@next`, e publique a versão do curso sem `-`.
- Segredos: `NPM_TOKEN` (publicação no npm, com provenance) e `TAP_GITHUB_TOKEN` (escrita em `JataiCloud/homebrew-tap` e `JataiCloud/scoop-bucket`). Sem o `TAP_GITHUB_TOKEN`, a release sai com binários e npm, mas sem Homebrew e Scoop (o workflow avisa).
- Para ensaiar localmente sem publicar: `goreleaser release --snapshot --clean && node scripts/npm-packages.mjs --version 0.0.0-snapshot`, e `JATAI_DIST_DIR=dist go test ./internal/update -run BuiltArchives`.
- Testes do npm: `node --test "npm/**/*.test.mjs" "scripts/*.test.mjs"` (Node 21 ou mais novo, por causa dos globs).
