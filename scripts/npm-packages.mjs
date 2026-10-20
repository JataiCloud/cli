#!/usr/bin/env node
// Monta (e, com --publish, publica) os pacotes npm do jatai a partir do
// dist/artifacts.json do GoReleaser: um pacote por plataforma com o binário
// (@jataicloud/cli-*) e o pacote principal `jatai-cli`, com o lançador em JS.
// O npm bloqueia o nome `jatai` (parecido com jotai/jiti); o comando instalado segue `jatai`.
//
// Uso: node scripts/npm-packages.mjs --version 2.0.0 [--publish]
//
// Rodar da raiz do repositório, depois do `goreleaser release`.

import { execFileSync } from "node:child_process";
import { chmodSync, copyFileSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");

const REPOSITORY = { type: "git", url: "git+https://github.com/JataiCloud/cli.git" };
const HOMEPAGE = "https://jataicloud.com.br";
const SCOPE = "@jataicloud";

// goos/goarch do GoReleaser -> os/cpu do npm.
export const PLATFORMS = [
  { goos: "darwin", goarch: "arm64", os: "darwin", cpu: "arm64" },
  { goos: "darwin", goarch: "amd64", os: "darwin", cpu: "x64" },
  { goos: "linux", goarch: "amd64", os: "linux", cpu: "x64" },
  { goos: "linux", goarch: "arm64", os: "linux", cpu: "arm64" },
  { goos: "windows", goarch: "amd64", os: "win32", cpu: "x64" },
];

const SEMVER = /^\d+\.\d+\.\d+(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$/;

export const packageName = (p) => `${SCOPE}/cli-${p.os}-${p.cpu}`;
export const binaryFile = (p) => (p.os === "win32" ? "jatai.exe" : "jatai");

// Prerelease (2.0.0-rc.1) não pode virar `latest`: quem faz `npm i jatai-cli` recebe a estável.
export const distTag = (version) => (version.includes("-") ? "next" : "latest");

function findBinary(artifacts, platform) {
  const found = artifacts.filter(
    (a) => a.type === "Binary" && a.goos === platform.goos && a.goarch === platform.goarch,
  );
  if (found.length !== 1) {
    throw new Error(
      `dist/artifacts.json tem ${found.length} binários para ${platform.goos}/${platform.goarch} (esperado 1)`,
    );
  }
  return found[0].path;
}

function writePackage(dir, manifest) {
  writeFileSync(join(dir, "package.json"), `${JSON.stringify(manifest, null, 2)}\n`);
}

// Cria dist/npm/<pacote> para as cinco plataformas e para o principal.
// Devolve os diretórios na ordem de publicação: plataformas primeiro, principal por último.
export function buildPackages({ version, artifactsPath, outDir }) {
  if (!SEMVER.test(version)) {
    throw new Error(`versão inválida: "${version}" (use 2.0.0 ou 2.0.0-rc.1, sem o "v")`);
  }
  const artifacts = JSON.parse(readFileSync(artifactsPath, "utf8"));
  rmSync(outDir, { recursive: true, force: true });

  const dirs = [];
  const optionalDependencies = {};
  for (const platform of PLATFORMS) {
    const name = packageName(platform);
    const dir = join(outDir, name.split("/")[1]);
    const bin = join(dir, "bin", binaryFile(platform));
    mkdirSync(dirname(bin), { recursive: true });
    copyFileSync(findBinary(artifacts, platform), bin);
    chmodSync(bin, 0o755);
    writePackage(dir, {
      name,
      version,
      description: `Binário do jatai (CLI da Jataí) para ${platform.os}/${platform.cpu}`,
      license: "MIT",
      repository: REPOSITORY,
      homepage: HOMEPAGE,
      os: [platform.os],
      cpu: [platform.cpu],
      files: ["bin"],
    });
    optionalDependencies[name] = version;
    dirs.push({ name, dir });
  }

  const dir = join(outDir, "jatai-cli");
  mkdirSync(join(dir, "bin"), { recursive: true });
  const launcher = join(dir, "bin", "jatai.js");
  copyFileSync(join(root, "npm", "jatai-cli", "bin", "jatai.js"), launcher);
  chmodSync(launcher, 0o755);
  copyFileSync(join(root, "npm", "jatai-cli", "README.md"), join(dir, "README.md"));
  writePackage(dir, {
    name: "jatai-cli",
    version,
    description: "CLI da Jataí: deploy de apps na nuvem brasileira, e servidor MCP para agentes de IA",
    license: "MIT",
    repository: REPOSITORY,
    homepage: HOMEPAGE,
    bugs: { url: "https://github.com/JataiCloud/cli/issues" },
    keywords: ["jatai", "paas", "deploy", "cli", "mcp"],
    bin: { jatai: "bin/jatai.js" },
    files: ["bin"],
    engines: { node: ">=18" },
    optionalDependencies,
  });
  dirs.push({ name: "jatai-cli", dir });
  return dirs;
}

function npm(args, options = {}) {
  return execFileSync("npm", args, { encoding: "utf8", ...options });
}

// Uma nova tentativa depois de uma falha no meio não pode quebrar nos pacotes que já subiram.
function alreadyPublished(name, version) {
  try {
    return npm(["view", `${name}@${version}`, "version"], { stdio: ["ignore", "pipe", "ignore"] }).trim() === version;
  } catch {
    return false;
  }
}

function publish(packages, version) {
  const tag = distTag(version);
  for (const { name, dir } of packages) {
    if (alreadyPublished(name, version)) {
      console.log(`${name}@${version} já está publicado; pulando.`);
      continue;
    }
    console.log(`Publicando ${name}@${version} (dist-tag ${tag})...`);
    npm(["publish", dir, "--access", "public", "--provenance", "--tag", tag], { stdio: "inherit" });
  }
}

function parseArgs(argv) {
  const opts = { publish: false, version: undefined };
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === "--publish") opts.publish = true;
    else if (argv[i] === "--version") opts.version = argv[++i];
    else throw new Error(`argumento desconhecido: ${argv[i]}`);
  }
  if (!opts.version) throw new Error("uso: node scripts/npm-packages.mjs --version <versão> [--publish]");
  return opts;
}

function main() {
  const { version, publish: doPublish } = parseArgs(process.argv.slice(2));
  const artifactsPath = resolve("dist", "artifacts.json");
  if (!existsSync(artifactsPath)) throw new Error(`${artifactsPath} não existe: rode o GoReleaser antes`);
  const packages = buildPackages({ version, artifactsPath, outDir: resolve("dist", "npm") });
  console.log(`${packages.length} pacotes em dist/npm (versão ${version}, dist-tag ${distTag(version)}).`);
  if (doPublish) publish(packages, version);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    main();
  } catch (err) {
    console.error(`Erro: ${err.message}`);
    process.exit(1);
  }
}
