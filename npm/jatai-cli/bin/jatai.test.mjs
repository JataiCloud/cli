import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { copyFileSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { after, describe, test } from "node:test";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const launcher = join(here, "jatai.js");
const { platformPackage } = createRequire(import.meta.url)("./jatai.js");

describe("platformPackage", () => {
  const cases = [
    ["darwin", "arm64", "@jataicloud/cli-darwin-arm64"],
    ["darwin", "x64", "@jataicloud/cli-darwin-x64"],
    ["linux", "x64", "@jataicloud/cli-linux-x64"],
    ["linux", "arm64", "@jataicloud/cli-linux-arm64"],
    ["win32", "x64", "@jataicloud/cli-win32-x64"],
  ];
  for (const [platform, arch, want] of cases) {
    test(`${platform}/${arch}`, () => assert.equal(platformPackage(platform, arch), want));
  }

  test("an unsupported platform points to the install script", () => {
    assert.throws(
      () => platformPackage("freebsd", "x64"),
      {
        message: "Plataforma freebsd/x64 sem binário do Jataí. Instale pelo script: https://jataicloud.com.br/install.sh",
      },
    );
  });

  test("win32/arm64 is not shipped", () => {
    assert.throws(() => platformPackage("win32", "arm64"), /Plataforma win32\/arm64 sem binário/);
  });
});

// Lançador de ponta a ponta: um node_modules falso com um "binário" em sh.
describe("launcher process", { skip: process.platform === "win32" }, () => {
  const root = mkdtempSync(join(tmpdir(), "jatai-launcher-"));
  after(() => rmSync(root, { recursive: true, force: true }));

  function install(name, script) {
    const dir = join(root, name);
    const mainBin = join(dir, "node_modules", "jatai", "bin");
    mkdirSync(mainBin, { recursive: true });
    copyFileSync(launcher, join(mainBin, "jatai.js"));
    if (script) {
      const platformBin = join(dir, "node_modules", platformPackage(process.platform, process.arch), "bin");
      mkdirSync(platformBin, { recursive: true });
      writeFileSync(join(platformBin, "jatai"), script, { mode: 0o755 });
    }
    return join(mainBin, "jatai.js");
  }

  test("passes arguments through and exits with the binary's code", () => {
    const entry = install("args", '#!/bin/sh\nprintf "args=%s\\n" "$*"\nexit 3\n');
    const out = spawnSync(process.execPath, [entry, "deploy", "--app", "x"], { encoding: "utf8" });
    assert.equal(out.status, 3);
    assert.equal(out.stdout, "args=deploy --app x\n");
  });

  test("keeps stdin and stdout attached to the binary (stdio MCP)", () => {
    const entry = install("stdio", "#!/bin/sh\nread line\nprintf 'echo:%s\\n' \"$line\"\n");
    const out = spawnSync(process.execPath, [entry, "mcp"], { encoding: "utf8", input: "ping\n" });
    assert.equal(out.status, 0);
    assert.equal(out.stdout, "echo:ping\n");
  });

  test("forwards SIGTERM to the binary", async () => {
    const script = "#!/bin/sh\ntrap 'exit 7' TERM\necho ready\nwhile true; do sleep 0.1; done\n";
    const entry = install("signal", script);
    const child = spawn(process.execPath, [entry], { stdio: ["pipe", "pipe", "inherit"] });
    await new Promise((resolve) => child.stdout.once("data", resolve));
    const exited = new Promise((resolve) => child.once("exit", (code, signal) => resolve({ code, signal })));
    child.kill("SIGTERM");
    assert.deepEqual(await exited, { code: 7, signal: null });
  });

  test("explains how to recover when the platform package is missing", () => {
    const entry = install("missing", null);
    const out = spawnSync(process.execPath, [entry], { encoding: "utf8" });
    assert.equal(out.status, 1);
    assert.match(out.stderr, /não está instalado/);
    assert.match(out.stderr, /--omit=optional/);
    assert.match(out.stderr, /install\.sh/);
  });
});
