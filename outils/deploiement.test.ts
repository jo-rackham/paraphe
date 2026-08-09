// What breaks a production rollout without breaking a unit test.
//
// The synthetic dataset builds and starts the image in CI. If it does not
// provide exactly the columns the API expects, everything is green here and
// the published image dies at startup — it happened.

import { mkdtempSync, readdirSync, readFileSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { parse } from "yaml";

import { parseRecords, parseRows } from "../noyau/csv.ts";
import { RANKS } from "../noyau/messages.ts";
import { ROOT } from "./config.ts";
import { main as syntheticData } from "./faux-jeu.ts";

/** The threshold below which the API refuses to import, read in api/db.go. */
function importThreshold(): number {
  const source = readFileSync(join(ROOT, "api", "db.go"), "utf8");
  const m = /len\(rows\) < (\d+)/.exec(source);
  if (!m) throw new Error("import threshold not found in api/db.go");
  return Number(m[1]);
}

/** The `Cols` list of api/vocabulary.go, read as is. */
function expectedColumns(): string[] {
  const source = readFileSync(join(ROOT, "api", "vocabulary.go"), "utf8");
  const block = /var Cols = \[\]string\{([\s\S]*?)\n\}/.exec(source);
  if (!block) throw new Error("Cols not found in api/vocabulary.go");
  return [...block[1].matchAll(/"([^"]+)"/g)].map((m) => m[1]);
}

const build = () => {
  const dest = mkdtempSync(join(tmpdir(), "paraphe-"));
  syntheticData(dest);
  return dest;
};

describe("the synthetic dataset", () => {
  it("provides every column the API imports", () => {
    const dest = build();
    const header = new Set(
      parseRows(readFileSync(join(dest, "04_base_complete.csv"), "utf8"))[0]);
    const missing = expectedColumns().filter((c) => !header.has(c));
    expect(missing, "the image would start in error while the tests "
      + "stayed green").toEqual([]);
  });

  // A fixture too short builds an image the API refuses to start (the
  // threshold protects against a truncated CSV that would empty the
  // database), with a green CI and an error that only shows at
  // `docker run`.
  it("clears the API's import threshold", () => {
    const dest = build();
    const rows = parseRecords(readFileSync(join(dest, "04_base_complete.csv"), "utf8"));
    expect(rows.length).toBeGreaterThanOrEqual(importThreshold());
  });

  // A fixture that fills every field and uses two departments certifies a
  // coverage it does not have: the fallbacks the card relies on, the
  // overseas INSEE, the elision and the ordering are never crossed.
  it("carries the shapes the real base carries", () => {
    const dest = build();
    const rows = parseRecords(readFileSync(join(dest, "04_base_complete.csv"), "utf8"));
    const departments = new Set(rows.map((r) => r.department));
    expect(departments.size, "two departments cannot exercise a perimeter")
      .toBeGreaterThan(5);
    // the RNE's empty department label for overseas is a source-side trap,
    // covered by outils/sorties.test.ts on the real files; what this
    // fixture must carry is the three-digit INSEE prefix
    const overseas = rows.filter((r) => r.insee_code.startsWith("97"));
    expect(overseas.length, "no overseas row at all").toBeGreaterThan(0);
    expect(overseas.every((r) => r.insee_code.length === 5),
      "an INSEE code is five characters").toBe(true);
    expect(rows.some((r) => !r.email), "every email filled").toBe(true);
    expect(rows.some((r) => r.email.includes(";")),
      "no concatenated address, which the directory produces 318 times").toBe(true);
    expect(rows.some((r) => !r.town_hall_hours), "every opening hour filled").toBe(true);
    expect(rows.some((r) => /^[AEÉIOUÀÂÎÔÛŒ]/.test(r.commune)),
      "no commune the closing line has to elide").toBe(true);
    expect(rows.some((r) => /^(Le|La|Les|L')/.test(r.commune)),
      "no commune carrying its article").toBe(true);
    const scores = new Set(rows.filter((r) => r.rank === "has_endorsed")
      .map((r) => r.score));
    expect(scores.size, "a single score cannot exercise an ordering")
      .toBeGreaterThan(1);
  });

  it("covers all three ranks", () => {
    // without them, the message invariant tests would pass while checking
    // nothing
    const dest = build();
    const rows = parseRecords(readFileSync(join(dest, "04_base_complete.csv"), "utf8"));
    expect(new Set(rows.map((r) => r.rank))).toEqual(new Set(Object.keys(RANKS)));
  });
});

describe("the deployment files", () => {
  // A ":" in an unquoted command makes YAML read the line as a mapping:
  // GitHub rejects the ENTIRE workflow, and without a remote nothing
  // signals it. The Taskfile paid exactly this trap, ci.yml too.
  // Checked on the PARSED document, not on the text. The regex version of
  // this test only knew the GitHub Actions shape (`run:`), and Task writes
  // its commands as bare list items under `cmds:` — which is the very
  // shape the defect was paid in. The invariant is simply: a command is a
  // string. YAML turning one into a mapping is the whole failure.
  it("write commands YAML does not read back as mappings", () => {
    // Globbed, not listed: a workflow added later was invisible to a
    // hardcoded list, and GitHub rejects the whole file for this.
    const workflows = readdirSync(join(ROOT, ".github", "workflows"))
      .filter((f) => f.endsWith(".yml") || f.endsWith(".yaml"))
      .map((f) => join(".github", "workflows", f));
    expect(workflows.length, "no workflow found to check").toBeGreaterThan(0);
    const files = [...workflows, "Taskfile.yml"];
    // Mappings that legitimately sit where a command could: Task calling
    // another task, GitHub's `defaults: run:` block. Anything else means
    // YAML split a shell line on a colon.
    const DECLARED = new Set(["task", "cmd", "cmds", "defer", "for", "vars",
      "silent", "ignore_error", "platforms", "set", "shopt", "shell",
      "working-directory"]);
    const offenders: Record<string, string[]> = {};

    // `underCommand` follows the PATH, not the key name: a Task named
    // `run` or a GitHub job named `cmds` is an ordinary name, and reacting
    // to the bare key denounced its `desc` and its `runs-on`.
    const isCommandPath = (path: string[]) =>
      // `.+`, not `[^.]+`: a task name is free to contain a dot
      // ("db.reset"), and requiring one path segment made the canary blind
      // to exactly the mutation it exists for.
      /^tasks\..+\.cmds(\.\d+)?$/.test(path.join("."))
      || /^jobs\.[^.]+\.steps\.\d+\.run$/.test(path.join("."))
      || /^(defaults|jobs\.[^.]+\.defaults)\.run$/.test(path.join("."));

    const walk = (
      node: unknown, path: string[], found: string[], commands: string[],
    ) => {
      if (Array.isArray(node)) {
        node.forEach((item, i) => walk(item, [...path, String(i)], found, commands));
        return;
      }
      if (typeof node === "string") {
        if (isCommandPath(path)) commands.push(node);
        return;
      }
      if (node === null || typeof node !== "object") return;
      const entries = Object.entries(node as Record<string, unknown>);
      if (isCommandPath(path) && entries.some(([k]) => !DECLARED.has(k))) {
        found.push(JSON.stringify(node).slice(0, 140));
      }
      for (const [key, value] of entries) {
        walk(value, [...path, key], found, commands);
      }
    };

    for (const rel of files) {
      const found: string[] = [];
      const text = readFileSync(join(ROOT, rel), "utf8");
      const commands: string[] = [];
      walk(parse(text), [], found, commands);
      // The same class of defect, silently: YAML truncates a command on an
      // unquoted "#", and what is left is still a string, so the walk is
      // content. Compared against the source, the amputation shows.
      for (const command of commands) {
        const single = command.split("\n")[0].trim();
        // a block whose first line is a comment is not a command
        if (single.length <= 4 || single.startsWith("#")) continue;
        // YAML eats the whitespace before the "#", so the source may carry
        // any run of it — aligning a comment with two spaces is the
        // commonest shape there is. And the line must not be a comment
        // itself, or commenting out a variant accuses the live command.
        const escaped = single.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
        // [^\S\n] and not \s: the latter crosses the newline, so a comment
        // block matched its own next line and accused itself.
        const truncated = new RegExp(
          `^[^\\S\\n]*(?:-[^\\S\\n]+)?(?:[a-z-]+:[^\\S\\n]*)?${escaped}[^\\S\\n]+#`, "m");
        if (truncated.test(text)) found.push(`tronquée sur # : ${single}`);
      }
      if (found.length) offenders[rel] = found;
    }
    expect(offenders, "an unquoted \":\" makes YAML read a command as a "
      + "mapping, and GitHub rejects the whole workflow").toEqual({});
  });

  // A PARAPHE_* the operator gives is an EXPLICIT override, reapplied over
  // what coordination edited in "Mon équipe". The chart shipped the nine
  // template values, so every pod restart wiped the campaign, re-armed the
  // "not configured" banner and put the batch size back — in silence, on
  // the default install.
  it("ship no campaign value that a restart would reimpose", () => {
    const values = readFileSync(
      join(ROOT, "chart", "paraphe", "values.yaml"), "utf8");
    const campaign = /\ncampaign:\n((?:  .*\n|\n)*)/.exec(values);
    expect(campaign, "the campaign block moved or was renamed").toBeTruthy();
    const filled = [...(campaign as RegExpExecArray)[1].matchAll(/^ {2}(\w+):\s*(.+)$/gm)]
      .filter(([, , v]) => v.trim() !== '""' && !v.trim().startsWith("#"));
    expect(filled.map(([, k, v]) => `${k}: ${v}`),
      "a default install would overwrite the campaign at every restart")
      .toEqual([]);

    const env = readFileSync(join(ROOT, ".env.exemple"), "utf8");
    const lot = /^PARAPHE_BATCH_SIZE=(.*)$/m.exec(env);
    expect(lot?.[1].trim(), ".env.exemple reimposes the batch size").toBe("");
  });

  it("keep no leftover of the project's former names", () => {
    // the project was called citoyen then parrainages before paraphe; a
    // leftover produces a database or an image that does not exist,
    // invisible until deployment
    const watched = ["docker-compose.yml", ".github/workflows/ci.yml",
      ".github/workflows/release.yml", "Dockerfile", ".env.exemple",
      "chart/paraphe/values.yaml", "chart/paraphe/Chart.yaml"];
    // "parrainages" stays legitimate as a French word in comments; what is
    // hunted are the identifiers
    const patterns = ["CITOYEN_", "citoyen-", "/citoyen", ":citoyen",
      "/parrainages", "-U parrainages", "DB: parrainages"];
    const offenders: Record<string, string[]> = {};
    for (const rel of watched) {
      const text = readFileSync(join(ROOT, rel), "utf8");
      const found = patterns.filter((m) => text.includes(m));
      if (found.length) offenders[rel] = found;
    }
    expect(offenders).toEqual({});
  });
});

// A PARAPHE_* variable named in the chart, in .env.exemple or in the
// deployment guide but read by NO code is a typo that only shows up on a
// server: the operator sets it, nothing happens, and the campaign runs with
// the shipped template. `PARAPHE_CANDIDATEE_DESCRIPTION` — one letter — did
// exactly that across three files at once.
describe("the deployment surfaces name real variables", () => {
  const NAMED_IN = [
    "chart/paraphe/templates/deployment.yaml",
    "chart/paraphe/values.yaml",
    ".env.exemple",
    "DEPLOIEMENT.md",
    "docker-compose.yml",
  ];
  const READ_IN = [
    "api", "outils", "noyau", "web/src", "web/vite.config.ts",
    "Taskfile.yml", ".github/workflows", "Dockerfile", "e2e",
  ];

  const variables = (text: string): string[] =>
    [...text.matchAll(/PARAPHE_[A-Z][A-Z0-9_]*/g)].map((m) => m[0]);

  /** Every PARAPHE_* the code actually consults. */
  const known = (): Set<string> => {
    const found = new Set<string>();
    const walk = (path: string) => {
      const full = join(ROOT, path);
      for (const entry of readdirSync(full, { withFileTypes: true })) {
        const child = join(path, entry.name);
        if (entry.isDirectory()) {
          if (entry.name !== "node_modules" && entry.name !== "dist") walk(child);
        } else if (/\.(go|ts|tsx|yml|yaml|json)$/.test(entry.name)
          // tests excluded, THIS file first: naming the typo in a comment
          // was enough to make it pass for a variable the code reads
          && !/\.test\.tsx?$|_test\.go$/.test(entry.name)) {
          variables(readFileSync(join(ROOT, child), "utf8")).forEach((v) => found.add(v));
        }
      }
    };
    for (const path of READ_IN) {
      if (statSync(join(ROOT, path)).isDirectory()) walk(path);
      else variables(readFileSync(join(ROOT, path), "utf8")).forEach((v) => found.add(v));
    }
    return found;
  };

  it("names none the code never reads", () => {
    const read = known();
    expect(read.size, "nothing was scanned").toBeGreaterThan(10);
    const unknown: string[] = [];
    for (const file of NAMED_IN) {
      for (const v of variables(readFileSync(join(ROOT, file), "utf8"))) {
        if (!read.has(v)) unknown.push(`${file}: ${v}`);
      }
    }
    expect(unknown).toEqual([]);
  });
});
