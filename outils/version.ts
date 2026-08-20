// One version, written in the three files that carry one.
//
// The published artefacts do NOT read these files: `release.yml` derives the
// image tag and `helm package --version` from the git tag, so the tag is the
// real source. These copies exist for whoever installs from a clone, and a
// copy that lies is worse than no copy — hence `versions()` below, which the
// test suite compares.
//
// Rewritten line by line on purpose: parsing and re-serialising Chart.yaml
// would drop every comment in it.

import { readFileSync, renameSync, writeFileSync } from "node:fs";
import { join } from "node:path";

import { ROOT } from "./config.ts";

const ROOT_PACKAGE = "package.json";
const WEB_PACKAGE = join("web", "package.json");
const CHART = join("chart", "paraphe", "Chart.yaml");

const read = (path: string): string => readFileSync(join(ROOT, path), "utf8");

/** The version each file claims, by file. */
export function versions(): Record<string, string> {
  const pkg = (path: string): string => {
    const m = /^\s*"version":\s*"([^"]+)"/m.exec(read(path));
    if (!m) throw new Error(`${path}: no "version" field`);
    return m[1];
  };
  const chart = read(CHART);
  const version = /^version:\s*(\S+)\s*$/m.exec(chart);
  const appVersion = /^appVersion:\s*"?([^"\s]+)"?\s*$/m.exec(chart);
  if (!version || !appVersion) {
    throw new Error(`${CHART}: version or appVersion not found`);
  }
  return {
    [ROOT_PACKAGE]: pkg(ROOT_PACKAGE),
    [WEB_PACKAGE]: pkg(WEB_PACKAGE),
    [`${CHART} (version)`]: version[1],
    [`${CHART} (appVersion)`]: appVersion[1],
  };
}

/** Writes `version` everywhere. Returns the files it changed. */
export function writeVersion(version: string): string[] {
  if (!/^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/.test(version)) {
    throw new Error(`version « ${version} » : x.y.z attendu`);
  }
  const changed: string[] = [];
  const replace = (path: string, apply: (src: string) => string) => {
    const src = read(path);
    const out = apply(src);
    if (out === src) return;
    // Write-then-rename, because package.json has READERS at any moment:
    // every `node` process resolves module formats through it, including the
    // ones the test suite spawns while the version tests rewrite 9.9.9 in a
    // parallel worker. A plain write let such a reader see a torn file —
    // « Invalid package config », in CI, in whichever test spawned node at
    // the wrong instant. A rename on the same filesystem is atomic: readers
    // get the old file or the new one, never half.
    writeFileSync(join(ROOT, `${path}.tmp`), out, "utf8");
    renameSync(join(ROOT, `${path}.tmp`), join(ROOT, path));
    changed.push(path);
  };
  for (const path of [ROOT_PACKAGE, WEB_PACKAGE]) {
    replace(path, (src) =>
      src.replace(/^(\s*"version":\s*")[^"]+(")/m, `$1${version}$2`),
    );
  }
  replace(CHART, (src) =>
    src
      .replace(/^version:\s*\S+$/m, `version: ${version}`)
      .replace(/^appVersion:\s*.*$/m, `appVersion: "${version}"`),
  );
  return changed;
}

if (
  process.argv[1] &&
  import.meta.url.endsWith(process.argv[1].split("/").pop()!)
) {
  const version = process.argv[2];
  if (!version) {
    console.error("usage: node outils/version.ts <x.y.z>");
    process.exit(2);
  }
  const changed = writeVersion(version);
  console.log(
    changed.length
      ? `${version} écrit dans : ${changed.join(", ")}`
      : `${version} : déjà à jour partout`,
  );
}
