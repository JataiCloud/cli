# jatai — CLI da Jataí

Deploy de apps Rails, Node.js, sites estáticos ou qualquer Dockerfile na nuvem brasileira.

## Instalação

- macOS e Linux: `curl -fsSL https://jataicloud.com.br/install.sh | sh`
- macOS (Homebrew): `brew install jataicloud/tap/jatai`
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
