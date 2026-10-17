# jatai

CLI da [Jataí](https://jataicloud.com.br): deploy de apps Rails, Node.js, sites estáticos ou qualquer Dockerfile na nuvem brasileira.

Este pacote instala o binário `jatai` da sua plataforma (macOS arm64/x64, Linux arm64/x64 e Windows x64).

## Uso

    npx jatai --help
    npm install -g jatai

    jatai login
    jatai init
    jatai up

## Agentes de IA (MCP)

O `jatai mcp` expõe a plataforma a agentes por stdio. Num cliente MCP:

    {
      "mcpServers": {
        "jatai": { "command": "npx", "args": ["-y", "jatai", "mcp"] }
      }
    }

## Problemas na instalação

O binário vem de pacotes opcionais (`@jataicloud/cli-*`). Não instale com `--omit=optional` ou `--no-optional`. Se a sua plataforma não for suportada, ou preferir não usar o npm, instale pelo script:

    curl -fsSL https://jataicloud.com.br/install.sh | sh

Documentação: https://jataicloud.com.br/docs/quickstart · Código: https://github.com/JataiCloud/cli
