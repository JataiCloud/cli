#!/usr/bin/env node
"use strict";

// Lançador do pacote npm `jatai`: o binário Go vem num pacote por plataforma
// (@jataicloud/cli-*, instalado como optionalDependency) e este arquivo só o
// encontra e o executa.

const { spawn } = require("node:child_process");
const { isatty } = require("node:tty");

const INSTALL_HINT = "Instale pelo script: https://jataicloud.com.br/install.sh";

const PACKAGES = {
  "darwin/arm64": "@jataicloud/cli-darwin-arm64",
  "darwin/x64": "@jataicloud/cli-darwin-x64",
  "linux/x64": "@jataicloud/cli-linux-x64",
  "linux/arm64": "@jataicloud/cli-linux-arm64",
  "win32/x64": "@jataicloud/cli-win32-x64",
};

function platformPackage(platform, arch) {
  const name = PACKAGES[`${platform}/${arch}`];
  if (!name) {
    throw new Error(`Plataforma ${platform}/${arch} sem binário do Jataí. ${INSTALL_HINT}`);
  }
  return name;
}

function binaryPath(platform = process.platform, arch = process.arch) {
  const name = platformPackage(platform, arch);
  const file = platform === "win32" ? "jatai.exe" : "jatai";
  try {
    return require.resolve(`${name}/bin/${file}`);
  } catch {
    throw new Error(
      `O pacote ${name} não está instalado, e é ele que traz o binário da sua plataforma. ` +
        `Reinstale sem --omit=optional, ou instale pelo script: https://jataicloud.com.br/install.sh`,
    );
  }
}

function run() {
  let bin;
  try {
    bin = binaryPath();
  } catch (err) {
    console.error(`Erro: ${err.message}`);
    process.exit(1);
  }

  // stdio herdado: é o que mantém o `jatai mcp` (stdio) funcionando via npx.
  const child = spawn(bin, process.argv.slice(2), { stdio: "inherit" });

  // Num terminal, o Ctrl-C já chega ao processo filho (mesmo grupo); repassar
  // de novo contaria como o segundo Ctrl-C, que encerra a CLI na hora.
  // isatty(0) em vez de process.stdin: tocar em process.stdin pode pôr o fd 0
  // (compartilhado com o filho) em modo não bloqueante e quebrar o `jatai mcp`.
  const forward = (signal) => () => {
    if (signal === "SIGINT" && isatty(0)) return;
    child.kill(signal);
  };
  const handlers = { SIGINT: forward("SIGINT"), SIGTERM: forward("SIGTERM") };
  for (const [signal, handler] of Object.entries(handlers)) process.on(signal, handler);

  child.on("error", (err) => {
    console.error(`Erro ao executar ${bin}: ${err.message}`);
    process.exit(1);
  });
  child.on("exit", (code, signal) => {
    if (signal) {
      for (const [name, handler] of Object.entries(handlers)) process.removeListener(name, handler);
      process.kill(process.pid, signal);
      return;
    }
    process.exit(code ?? 1);
  });
}

module.exports = { platformPackage, binaryPath };

if (require.main === module) run();
