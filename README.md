# jatai — CLI da Jataí

Deploy de apps Rails, Node.js, sites estáticos ou qualquer Dockerfile na nuvem brasileira.

## Instalação

- macOS e Linux: `curl -fsSL https://jataicloud.com.br/install.sh | sh`
- macOS (Homebrew): `brew install jataicloud/tap/jatai`
- npm: `npx jatai` ou `npm install -g jatai` (o binário vem de pacotes `@jataicloud/cli-*`; não use `--omit=optional`)
- Windows (Scoop): `scoop bucket add jataicloud https://github.com/jataicloud/scoop-bucket` e `scoop install jatai`

## Uso

    jatai login
    jatai init
    jatai up

Documentação: https://jataicloud.com.br/docs/quickstart

## Contrato com o servidor

`internal/cli/testdata/api` guarda respostas reais da API, gravadas pelos request specs da plataforma. Para regenerar, no repositório da plataforma:

    CLI_FIXTURES_DIR=<este repositório>/internal/cli/testdata/api bundle exec rspec spec/requests/api/v1 --order defined

`go test ./...` confere que cada resposta traz o que a CLI lê (`internal/cli`, `internal/mcpserver` e `internal/deployflow` têm um `contract_test.go`). Regenere antes de cada release e revise os arquivos (o repositório é público).

## Release

Uma tag `vX.Y.Z` (ou `vX.Y.Z-rc.N`) dispara `.github/workflows/release.yml`: testes, GoReleaser (pacotes `jatai_<versão>_<os>_<arch>.tar.gz`, `.zip` no Windows, `checksums.txt`, `install.sh` e `install.ps1`), conferência dos pacotes e publicação no npm (`node scripts/npm-packages.mjs`, plataformas primeiro e `jatai` por último).

- Os pacotes têm o binário na raiz e esse nome é um contrato com `jatai update` e com os instaladores (`internal/update/release_contract_test.go` confere o `.goreleaser.yaml`).
- Tags `-rc.N` são releases normais do GitHub, sem marca de pré-lançamento, para que `/releases/latest` (usado por `install.sh`, `install.ps1` e `jatai update`) tenha o que devolver antes da v2.0.0. No npm, versões com `-` saem na dist-tag `next`; as demais, em `latest`.
- Segredos: `NPM_TOKEN` (publicação no npm, com provenance) e `TAP_GITHUB_TOKEN` (escrita em `JataiCloud/homebrew-tap` e `JataiCloud/scoop-bucket`). Sem o `TAP_GITHUB_TOKEN`, a release sai com binários e npm, mas sem Homebrew e Scoop (o workflow avisa).
- Para ensaiar localmente sem publicar: `goreleaser release --snapshot --clean && node scripts/npm-packages.mjs --version 0.0.0-snapshot`, e `JATAI_DIST_DIR=dist go test ./internal/update -run BuiltArchives`.
- Testes do npm: `node --test "npm/**/*.test.mjs" "scripts/*.test.mjs"` (Node 21 ou mais novo, por causa dos globs).
