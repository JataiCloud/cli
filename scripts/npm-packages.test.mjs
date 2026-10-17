import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { after, before, describe, test } from "node:test";
import { fileURLToPath } from "node:url";

import { distTag } from "./npm-packages.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const script = join(here, "npm-packages.mjs");
const artifacts = JSON.parse(readFileSync(join(here, "testdata", "artifacts.json"), "utf8"));

const PLATFORMS = {
  "@jataicloud/cli-darwin-arm64": { os: "darwin", cpu: "arm64", file: "jatai" },
  "@jataicloud/cli-darwin-x64": { os: "darwin", cpu: "x64", file: "jatai" },
  "@jataicloud/cli-linux-x64": { os: "linux", cpu: "x64", file: "jatai" },
  "@jataicloud/cli-linux-arm64": { os: "linux", cpu: "arm64", file: "jatai" },
  "@jataicloud/cli-win32-x64": { os: "win32", cpu: "x64", file: "jatai.exe" },
};

// Um diretório de trabalho como o do runner depois do GoReleaser: dist/artifacts.json
// e os binários nos caminhos que ele registra.
function fakeDist() {
  const cwd = mkdtempSync(join(tmpdir(), "jatai-npm-"));
  mkdirSync(join(cwd, "dist"));
  copyFileSync(join(here, "testdata", "artifacts.json"), join(cwd, "dist", "artifacts.json"));
  for (const a of artifacts.filter((x) => x.type === "Binary")) {
    mkdirSync(dirname(join(cwd, a.path)), { recursive: true });
    writeFileSync(join(cwd, a.path), `binario ${a.goos}/${a.goarch}`, { mode: 0o644 });
  }
  return cwd;
}

const readJSON = (path) => JSON.parse(readFileSync(path, "utf8"));
const run = (cwd, ...args) => spawnSync(process.execPath, [script, ...args], { cwd, encoding: "utf8" });

describe("distTag", () => {
  test("prereleases go to next", () => {
    assert.equal(distTag("2.0.0-rc.1"), "next");
    assert.equal(distTag("0.0.0-snapshot"), "next");
  });
  test("stable versions go to latest", () => assert.equal(distTag("2.0.0"), "latest"));
});

describe("npm-packages.mjs --version 2.0.0-rc.1", () => {
  let cwd;
  let result;
  before(() => {
    cwd = fakeDist();
    result = run(cwd, "--version", "2.0.0-rc.1");
  });
  after(() => rmSync(cwd, { recursive: true, force: true }));

  test("succeeds without publishing and says which tag it would use", () => {
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /dist-tag next/);
    assert.doesNotMatch(result.stdout, /Publicando/);
  });

  test("builds one package per platform with os, cpu and the binary in bin/", () => {
    for (const [name, want] of Object.entries(PLATFORMS)) {
      const dir = join(cwd, "dist", "npm", name.split("/")[1]);
      const pkg = readJSON(join(dir, "package.json"));
      assert.equal(pkg.name, name);
      assert.equal(pkg.version, "2.0.0-rc.1");
      assert.deepEqual(pkg.os, [want.os]);
      assert.deepEqual(pkg.cpu, [want.cpu]);
      assert.deepEqual(pkg.files, ["bin"]);
      assert.equal(pkg.license, "MIT");
      const bin = join(dir, "bin", want.file);
      assert.ok(existsSync(bin), `${bin} missing`);
      assert.equal(statSync(bin).mode & 0o777, 0o755);
    }
  });

  test("copies the right binary into each platform package", () => {
    const bin = join(cwd, "dist", "npm", "cli-linux-arm64", "bin", "jatai");
    assert.equal(readFileSync(bin, "utf8"), "binario linux/arm64");
    const win = join(cwd, "dist", "npm", "cli-win32-x64", "bin", "jatai.exe");
    assert.equal(readFileSync(win, "utf8"), "binario windows/amd64");
  });

  test("builds the main package with the launcher and exact optionalDependencies", () => {
    const dir = join(cwd, "dist", "npm", "jatai");
    const pkg = readJSON(join(dir, "package.json"));
    assert.equal(pkg.name, "jatai");
    assert.equal(pkg.version, "2.0.0-rc.1");
    assert.deepEqual(pkg.bin, { jatai: "bin/jatai.js" });
    assert.equal(pkg.engines.node, ">=18");
    assert.equal(pkg.license, "MIT");
    assert.deepEqual(pkg.repository, { type: "git", url: "git+https://github.com/JataiCloud/cli.git" });
    assert.deepEqual(Object.keys(pkg.optionalDependencies).sort(), Object.keys(PLATFORMS).sort());
    for (const version of Object.values(pkg.optionalDependencies)) assert.equal(version, "2.0.0-rc.1");
    assert.equal(pkg.optionalDependencies["@jataicloud/cli-win32-x64"], "2.0.0-rc.1");
    assert.ok(existsSync(join(dir, "bin", "jatai.js")));
    assert.ok(existsSync(join(dir, "README.md")));
    assert.equal(statSync(join(dir, "bin", "jatai.js")).mode & 0o777, 0o755);
  });

  test("platform packages carry the repository needed for provenance", () => {
    const pkg = readJSON(join(cwd, "dist", "npm", "cli-darwin-x64", "package.json"));
    assert.deepEqual(pkg.repository, { type: "git", url: "git+https://github.com/JataiCloud/cli.git" });
  });
});

describe("npm-packages.mjs errors", () => {
  test("a stable version reports the latest tag", () => {
    const cwd = fakeDist();
    try {
      const out = run(cwd, "--version", "2.0.0");
      assert.equal(out.status, 0, out.stderr);
      assert.match(out.stdout, /dist-tag latest/);
    } finally {
      rmSync(cwd, { recursive: true, force: true });
    }
  });

  test("rejects a version with a leading v", () => {
    const cwd = fakeDist();
    try {
      const out = run(cwd, "--version", "v2.0.0");
      assert.notEqual(out.status, 0);
      assert.match(out.stderr, /versão inválida/);
    } finally {
      rmSync(cwd, { recursive: true, force: true });
    }
  });

  test("requires --version", () => {
    const cwd = fakeDist();
    try {
      const out = run(cwd);
      assert.notEqual(out.status, 0);
      assert.match(out.stderr, /--version/);
    } finally {
      rmSync(cwd, { recursive: true, force: true });
    }
  });

  test("fails when a platform binary is missing from artifacts.json", () => {
    const cwd = fakeDist();
    try {
      const path = join(cwd, "dist", "artifacts.json");
      const trimmed = readJSON(path).filter((a) => !(a.type === "Binary" && a.goos === "windows"));
      writeFileSync(path, JSON.stringify(trimmed));
      const out = run(cwd, "--version", "2.0.0");
      assert.notEqual(out.status, 0);
      assert.match(out.stderr, /windows\/amd64/);
    } finally {
      rmSync(cwd, { recursive: true, force: true });
    }
  });
});
