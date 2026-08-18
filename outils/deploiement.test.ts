// What breaks a production rollout without breaking a unit test.
//
// The synthetic dataset builds and starts the image in CI. If it does not
// provide exactly the columns the API expects, everything is green here and
// the published image dies at startup.

import {
  existsSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  statSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { parse } from "yaml";

import { parseRecords, parseRows } from "../noyau/csv.ts";
import { RANKS } from "../noyau/messages.ts";
import { ROOT } from "./config.ts";
import { main as syntheticData } from "./faux-jeu.ts";

/** The threshold below which the API refuses to import, read in api/import.go. */
function importThreshold(): number {
  const source = readFileSync(join(ROOT, "api", "import.go"), "utf8");
  const m = /len\(rows\) < (\d+)/.exec(source);
  if (!m) throw new Error("import threshold not found in api/import.go");
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
      parseRows(readFileSync(join(dest, "04_base_complete.csv"), "utf8"))[0],
    );
    const missing = expectedColumns().filter((c) => !header.has(c));
    expect(
      missing,
      "the image would start in error while the tests " + "stayed green",
    ).toEqual([]);
  });

  // A fixture too short builds an image the API refuses to start (the
  // threshold protects against a truncated CSV that would empty the
  // database), with a green CI and an error that only shows at
  // `docker run`.
  it("clears the API's import threshold", () => {
    const dest = build();
    const rows = parseRecords(
      readFileSync(join(dest, "04_base_complete.csv"), "utf8"),
    );
    expect(rows.length).toBeGreaterThanOrEqual(importThreshold());
  });

  // A fixture that fills every field and uses two departments certifies a
  // coverage it does not have: the fallbacks the card relies on, the
  // overseas INSEE, the elision and the ordering are never crossed.
  it("carries the shapes the real base carries", () => {
    const dest = build();
    const rows = parseRecords(
      readFileSync(join(dest, "04_base_complete.csv"), "utf8"),
    );
    const departments = new Set(rows.map((r) => r.department));
    expect(
      departments.size,
      "two departments cannot exercise a perimeter",
    ).toBeGreaterThan(5);
    // the RNE's empty department label for overseas is a source-side trap,
    // covered by outils/sorties.test.ts on the real files; what this
    // fixture must carry is the three-digit INSEE prefix
    const overseas = rows.filter((r) => r.insee_code.startsWith("97"));
    expect(overseas.length, "no overseas row at all").toBeGreaterThan(0);
    expect(
      overseas.every((r) => r.insee_code.length === 5),
      "an INSEE code is five characters",
    ).toBe(true);
    expect(
      rows.some((r) => !r.email),
      "every email filled",
    ).toBe(true);
    expect(
      rows.some((r) => r.email.includes(";")),
      "no concatenated address, which the directory produces 318 times",
    ).toBe(true);
    expect(
      rows.some((r) => !r.town_hall_hours),
      "every opening hour filled",
    ).toBe(true);
    expect(
      rows.some((r) => /^[AEÉIOUÀÂÎÔÛŒ]/.test(r.commune)),
      "no commune the closing line has to elide",
    ).toBe(true);
    expect(
      rows.some((r) => /^(Le|La|Les|L')/.test(r.commune)),
      "no commune carrying its article",
    ).toBe(true);
    const scores = new Set(
      rows.filter((r) => r.rank === "has_endorsed").map((r) => r.score),
    );
    expect(
      scores.size,
      "a single score cannot exercise an ordering",
    ).toBeGreaterThan(1);
  });

  it("covers all three ranks", () => {
    // without them, the message invariant tests would pass while checking
    // nothing
    const dest = build();
    const rows = parseRecords(
      readFileSync(join(dest, "04_base_complete.csv"), "utf8"),
    );
    expect(new Set(rows.map((r) => r.rank))).toEqual(
      new Set(Object.keys(RANKS)),
    );
  });
});

describe("the deployment files", () => {
  // A third-party action referenced by tag runs whatever that tag points at
  // TODAY. Whoever owns the repository — or anyone who takes it over — can
  // move it onto code that reads the workflow's secrets, and the run that
  // does it looks exactly like the one before. A commit is immutable, so the
  // reference is a commit, with the version in a comment for the reader.
  //
  // Globbed, not listed: a workflow added later would be invisible to a
  // hardcoded list, and one unpinned reference is enough.
  it("reference third-party actions by commit, never by tag", () => {
    const workflows = readdirSync(join(ROOT, ".github", "workflows")).filter(
      (f) => f.endsWith(".yml") || f.endsWith(".yaml"),
    );
    expect(workflows.length, "no workflow found to check").toBeGreaterThan(0);
    const floating: string[] = [];
    let pinned = 0;
    for (const name of workflows) {
      const text = readFileSync(
        join(ROOT, ".github", "workflows", name),
        "utf8",
      );
      for (const line of text.split("\n")) {
        const m = line.match(/^\s*(?:-\s*)?uses:\s*(\S+)/);
        if (!m) continue;
        const ref = m[1];
        // A workflow of this repository, called by path: it is versioned
        // with the file that calls it, so there is no tag to pin.
        if (ref.startsWith("./")) continue;
        if (/@[0-9a-f]{40}$/.test(ref)) pinned++;
        else floating.push(`${name}: ${ref}`);
      }
    }
    expect(floating, "these actions are referenced by a movable tag").toEqual(
      [],
    );
    expect(
      pinned,
      "no pinned action found: the check read nothing",
    ).toBeGreaterThan(10);
  });

  // A linter reads its configuration by walking UP from its working
  // directory, and a container stops that walk at the mount point. Mounting
  // api/ alone therefore ran golangci-lint with its DEFAULT rules while the
  // repository declares its own: the clone was green, the CI red, and the
  // release refused to publish over six findings .golangci.yml excludes on
  // purpose. Whatever is mounted has to contain the configuration.
  // The same defect twice in one workflow: golangci-lint mounted api/ alone
  // and missed .golangci.yml at the root; hadolint was PIPED the Dockerfile
  // with nothing mounted and missed .hadolint.yaml entirely, so it ran at
  // its default threshold and failed the build on the one `info` that file
  // exists to accept. A containerised linter sees exactly what is mounted —
  // so every one of them must have the repository under it.
  it("give every containerised linter the repository to read", () => {
    const ci = readFileSync(
      join(ROOT, ".github", "workflows", "ci.yml"),
      "utf8",
    );
    // the tools whose behaviour a file in this repository configures
    const configured: Record<string, string> = {
      "golangci-lint": ".golangci.yml",
      hadolint: ".hadolint.yaml",
      actionlint: ".github",
    };
    const blind: string[] = [];
    let checked = 0;
    for (const line of ci.split("\n")) {
      if (!line.includes("docker run")) continue;
      // the invocation may be wrapped over several lines; take the whole
      // command by joining continuations
      const start = ci.indexOf(line);
      const command = ci
        .slice(start)
        .split("\n")
        .reduce<string[]>((acc, l) => {
          if (acc.length === 0 || acc[acc.length - 1].endsWith("\\")) {
            acc.push(l.trim());
          }
          return acc;
        }, [])
        .join(" ")
        .replace(/\\ /g, " ");
      const tool = Object.keys(configured).find((t) => command.includes(t));
      if (!tool) continue;
      checked++;
      if (!/-v\s+"\$PWD:/.test(command)) {
        blind.push(`${tool}: ${configured[tool]} is not mounted`);
      }
      // piping the subject in leaves the tool with no directory to search
      if (/<\s*\S+/.test(command)) {
        blind.push(`${tool}: fed by stdin, so it reads no configuration`);
      }
    }
    expect(
      blind,
      "these linters run on rules the repository does not declare",
    ).toEqual([]);
    expect(
      checked,
      "no containerised linter found: the check read nothing",
    ).toBeGreaterThanOrEqual(3);
  });

  // A journey that skips is a journey that has stopped running, and it says
  // so in a line nobody reads. The three logo journeys are the only place a
  // BROWSER answers whether the Content-Security-Policy lets the store's
  // origin through — the reason that spec exists rather than a unit test —
  // and they skip when the harness was given no object store. CI gave them
  // none, so they had never run there once: an img-src that drops the logo
  // reaches production over a green suite, and the browser reports it on a
  // console nobody is watching.
  //
  // Read from the harness rather than listed: a setting added to e2e/ with
  // no matching line in the job is the same silent skip, one spec later.
  it("give the journeys every setting their harness reads", () => {
    const ci = readFileSync(
      join(ROOT, ".github", "workflows", "ci.yml"),
      "utf8",
    );
    const read = new Set<string>();
    for (const name of readdirSync(join(ROOT, "e2e"))) {
      if (!name.endsWith(".ts")) continue;
      const text = readFileSync(join(ROOT, "e2e", name), "utf8");
      for (const m of text.matchAll(
        /process\.env\.(PARAPHE_TEST_[A-Z0-9_]+)/g,
      )) {
        read.add(m[1]);
      }
    }
    expect(
      read.size,
      "no setting found in e2e/: the check read nothing",
    ).toBeGreaterThanOrEqual(5);
    // the job, up to the next one at the same indentation
    const job = /^ {2}e2e:\n((?:.*\n)*?)(?=^ {2}\S)/m.exec(ci);
    expect(job, "no e2e job found in ci.yml").not.toBeNull();
    const missing = [...read]
      .filter((name) => !new RegExp(`^\\s*${name}:`, "m").test(job?.[1] ?? ""))
      .sort();
    expect(
      missing,
      "the CI journeys skip whatever these configure, and say so to nobody",
    ).toEqual([]);
  });

  it("lint with the configuration the repository declares", () => {
    const ci = readFileSync(
      join(ROOT, ".github", "workflows", "ci.yml"),
      "utf8",
    );
    const step = /- name: Go\n((?:.*\n)*?)(?=\n\s*(?:#|- name:))/.exec(ci);
    expect(
      step,
      "the Go lint step is no longer where this test reads it",
    ).toBeTruthy();
    const text = step![1];
    expect(text, "the step no longer runs golangci-lint").toContain(
      "golangci-lint run",
    );
    // `working-directory: api` narrows $PWD to a directory that does not hold
    // .golangci.yml, which is what broke it
    expect(
      text,
      "the step narrows its directory below the one holding .golangci.yml",
    ).not.toMatch(/working-directory:\s*api/);
    const mount = /-v\s+"\$PWD:([^"]+)"/.exec(text);
    expect(mount, "the step mounts nothing this test can read").toBeTruthy();
    const workdir = /-w\s+(\S+)/.exec(text);
    expect(workdir, "the step declares no working directory").toBeTruthy();
    expect(
      workdir![1].startsWith(mount![1]),
      `the container works in ${workdir![1]}, outside the mount ${mount![1]}`,
    ).toBe(true);
    // and the configuration really is at the root, where the mount puts it
    expect(existsSync(join(ROOT, ".golangci.yml"))).toBe(true);
  });

  // kubelet injects one legacy Docker-links variable per Service port into
  // every container. The Service is named `paraphe`, so they arrive as
  // PARAPHE_PORT=tcp://<ip>:80 — the name the application reads as its own
  // setting. The flag that refuses them belongs on every pod the chart
  // draws: put on the one where the collision was noticed, it says nothing
  // about the next template somebody adds.
  it("refuse the kubelet's service variables on every pod they draw", () => {
    const templates = join(ROOT, "chart", "paraphe", "templates");
    const bare: string[] = [];
    let covered = 0;
    for (const name of readdirSync(templates).filter((f) =>
      f.endsWith(".yaml"),
    )) {
      // Comments are STRIPPED first. A search for the setting's text found
      // it in `# TODO: enableServiceLinks: false`, so deleting the field and
      // leaving a comment behind kept the guard green — the repository's own
      // rule, paid twice already: mutate by DELETION, never by commenting.
      const text = readFileSync(join(templates, name), "utf8").replace(
        /^[^\S\n]*#.*$/gm,
        "",
      );
      // a pod template of OUR making: `spec.template.spec`. CNPG's Cluster
      // draws its own pods and takes no such field.
      for (const doc of text.split(/^---$/m)) {
        if (!/^\s+template:\s*$/m.test(doc)) continue;
        if (/enableServiceLinks:\s*false/.test(doc)) covered++;
        else bare.push(name);
      }
    }
    expect(
      bare,
      "these pod templates take the kubelet's PARAPHE_* variables",
    ).toEqual([]);
    expect(
      covered,
      "no pod template found: the check read nothing",
    ).toBeGreaterThan(1);
  });

  // `lifecycle.preStop.sleep` is only honoured where its feature gate is on
  // by default — beta in 1.30, alpha (hence OFF) in 1.29. Below that floor
  // the API server strips the field without a word, and the drain the
  // Deployment relies on never happens. The floor and the field are one
  // decision: whoever removes the sleep should lower the floor knowingly,
  // and whoever lowers the floor should learn what it costs here.
  it("bound the cluster version the preStop drain needs", () => {
    const chart = readFileSync(
      join(ROOT, "chart", "paraphe", "Chart.yaml"),
      "utf8",
    );
    // Comments stripped: a single legal comment line between `preStop:` and
    // `sleep:` broke the adjacency, the gate returned, and the whole check
    // was skipped in silence — a guard that examines nothing is worse than
    // none, because it reads as green.
    const deployment = readFileSync(
      join(ROOT, "chart", "paraphe", "templates", "deployment.yaml"),
      "utf8",
    ).replace(/^[^\S\n]*#.*$/gm, "");
    // …and the absence of the field is asserted, never assumed: `return`
    // here used to mean "nothing to check", which is what a reformatting
    // turned into "nothing was checked".
    const hasSleep = /preStop:\s*(?:\n\s*)+sleep:/.test(deployment);
    expect(
      hasSleep,
      "the Deployment no longer drains through preStop.sleep: drop the " +
        "kubeVersion floor with it, knowingly, or put the field back",
    ).toBe(true);
    const floor = /^kubeVersion:\s*">=(\d+)\.(\d+)\./m.exec(chart);
    expect(floor, "the chart declares no kubeVersion floor").toBeTruthy();
    const [major, minor] = [Number(floor![1]), Number(floor![2])];
    expect(
      major * 1000 + minor,
      "preStop.sleep is alpha and OFF by default below 1.30: the chart " +
        "would install where the drain is silently dropped",
    ).toBeGreaterThanOrEqual(1030);
  });

  // The pages and the JSON come from ONE process, so `securityHeaders`
  // wraps both and there is no second implementation to hold in step. What
  // remains checkable here is that the middleware is still what wraps the
  // router — the day it wraps something narrower, the pages lose their
  // policy again, which is exactly what splitting the images did once.
  it("wrap the whole router in the security headers", () => {
    const main = readFileSync(join(ROOT, "api", "main.go"), "utf8");
    expect(
      main,
      "the router is no longer wrapped in securityHeaders: whatever " +
        "it stops covering is served without a Content-Security-Policy",
    ).toMatch(/Handler:\s*securityHeaders\(s\.routes\(\)\)/);
    const go = readFileSync(join(ROOT, "api", "router.go"), "utf8");
    const block = /const policy = ([\s\S]*?)\n\treturn /.exec(go);
    expect(
      block,
      "the policy is no longer where this test reads it",
    ).toBeTruthy();
    const policy = [...block![1].matchAll(/"([^"]*)"/g)]
      .map((m) => m[1])
      .join("");
    expect(policy.length, "an empty policy protects nothing").toBeGreaterThan(
      80,
    );
    for (const header of [
      "X-Content-Type-Options",
      "Referrer-Policy",
      "Cross-Origin-Opener-Policy",
      "frame-ancestors",
    ]) {
      expect(go, `${header} is no longer set`).toContain(header);
    }

    // The images directive moved OUT of the literal the day the campaign
    // logos began being served by the object store's own origin: it is
    // assembled at startup from PARAPHE_MEDIA_PUBLIC_URL. So the literal no
    // longer carries it, and this is what still says it exists.
    expect(
      go,
      "img-src is no longer declared: the policy above passes without it, " +
        "and every campaign logo would be refused by the browser",
    ).toContain("img-src 'self' data:");

    expect(
      go,
      "connect-src is no longer declared: the account-less version " +
        "downloads the campaign logo to inline it, and nothing else in this " +
        "application talks to another origin",
    ).toContain("connect-src 'self'");

    // …and the media origin reaches THOSE TWO directives and no other.
    // Widened into default-src or script-src it stops being a place images
    // come from — and one fetch goes to — and becomes a place code can.
    //
    // WHICH directives, not how many mentions. The count said "it belongs
    // to img-src alone", and the day the self-hosted browser version needed
    // to download a logo the only thing to do with a number was bump it.
    const assembled =
      /func contentSecurityPolicy\(media string\) string \{([\s\S]*?)\n\}/.exec(
        go,
      );
    expect(
      assembled,
      "contentSecurityPolicy is no longer where this test " + "reads it",
    ).toBeTruthy();
    const lines = assembled![1].split("\n").filter((l) => /\bmedia\b/.test(l));
    const appended = lines
      .filter((l) => /\+=/.test(l))
      .map((l) => /^\s*(\w+)\s*\+=/.exec(l)?.[1])
      .sort();
    expect(
      appended,
      "the media origin no longer reaches both the images and the connect " +
        "directive: a logo it cannot be fetched from, or cannot be shown from",
    ).toEqual(["connect", "images"]);
    expect(
      lines.filter((l) => !/\+=/.test(l) && !/if media != ""/.test(l)),
      "the media origin is read somewhere other than the two directives it " +
        "belongs to",
    ).toEqual([]);
  });

  // The interface picks its mode at load: an API answers /api/config → team
  // mode, nothing answers → browser mode, everything in IndexedDB. This
  // marker says "there IS an API here", so a passing failure of /api/config
  // cannot silently drop a volunteer into browser mode and have them write
  // their team's work to local storage.
  //
  // The process that serves the page is the one that stamps it, in memory,
  // at startup — markInterface. One spelling, in one file, and the interface
  // reads that same string.
  it("stamp the mode marker the interface looks for", () => {
    const marker = /<meta name="paraphe-mode" content="team">/;
    expect(
      readFileSync(join(ROOT, "api/pages.go"), "utf8"),
      "api/pages.go no longer carries the marker the interface reads",
    ).toMatch(marker);
    // …and the interface still reads that attribute, under that name
    const reader = readFileSync(join(ROOT, "web", "src", "api.ts"), "utf8");
    expect(
      reader,
      "the interface no longer reads the marker the image stamps",
    ).toContain('meta[name="paraphe-mode"]');
  });

  // …and EVERY marker, not that one. There are two now — `paraphe-mode`,
  // which says an API answers here, and `paraphe-instance`, which says which
  // instance a `?org=` resolves against — stamped by two functions in one
  // file and read in two others. The test above walks one row of that grid,
  // and a grid walked at some positions and not others certifies an
  // agreement that does not hold: rename the second marker on the server,
  // rename it in the Go test beside it, and both sides stay green while
  // `?org=` goes inert on every hosted campaign.
  //
  // So the LIST is read out of the stamper rather than written here: whoever
  // adds a third marker cannot add a stamper without a reader.
  it("stamp no marker the interface does not read", () => {
    const go = readFileSync(join(ROOT, "api/pages.go"), "utf8");
    const stamped = [...go.matchAll(/<meta name="(paraphe-[a-z-]+)"/g)].map(
      (m) => m[1],
    );
    expect(
      [...new Set(stamped)].sort(),
      "api/pages.go stamps a different set of markers than this test knows: " +
        "if that is a new one, the check below already demands its reader",
    ).toEqual(["paraphe-instance", "paraphe-mode"]);

    const src = join(ROOT, "web", "src");
    const readers = readdirSync(src)
      .filter((f) => f.endsWith(".ts") || f.endsWith(".tsx"))
      .filter((f) => !f.includes(".test."))
      .map((f) => readFileSync(join(src, f), "utf8"))
      .join("\n");
    for (const name of new Set(stamped)) {
      expect(
        readers,
        `the image stamps ${name} and no interface file reads it: the ` +
          "marker travels from api/pages.go to a screen, and a stamper " +
          "without a reader is a page carrying an answer nobody asks for",
      ).toContain(`meta[name="${name}"]`);
    }
  });

  // A ":" in an unquoted command makes YAML read the line as a mapping:
  // GitHub rejects the ENTIRE workflow, and without a remote nothing
  // signals it.
  // Checked on the PARSED document, not on the text: a regex knowing only the
  // GitHub Actions shape (`run:`) misses Task, which writes its commands as
  // bare list items under `cmds:`. The invariant is simply that a command is
  // a string, and YAML turning one into a mapping is the whole failure.
  it("write commands YAML does not read back as mappings", () => {
    // Globbed, not listed: a hardcoded list cannot see a workflow added
    // later, and GitHub rejects the whole file for this.
    const workflows = readdirSync(join(ROOT, ".github", "workflows"))
      .filter((f) => f.endsWith(".yml") || f.endsWith(".yaml"))
      .map((f) => join(".github", "workflows", f));
    expect(workflows.length, "no workflow found to check").toBeGreaterThan(0);
    const files = [...workflows, "Taskfile.yml"];
    // Mappings that legitimately sit where a command could: Task calling
    // another task, GitHub's `defaults: run:` block. Anything else means
    // YAML split a shell line on a colon.
    const DECLARED = new Set([
      "task",
      "cmd",
      "cmds",
      "defer",
      "for",
      "vars",
      "silent",
      "ignore_error",
      "platforms",
      "set",
      "shopt",
      "shell",
      "working-directory",
    ]);
    const offenders: Record<string, string[]> = {};

    // `underCommand` follows the PATH, not the key name: a Task named
    // `run` or a GitHub job named `cmds` is an ordinary name, and reacting
    // to the bare key denounced its `desc` and its `runs-on`.
    const isCommandPath = (path: string[]) =>
      // `.+`, not `[^.]+`: a task name is free to contain a dot
      // ("db.reset"), and requiring one path segment made the canary blind
      // to exactly the mutation it exists for.
      /^tasks\..+\.cmds(\.\d+)?$/.test(path.join(".")) ||
      /^jobs\.[^.]+\.steps\.\d+\.run$/.test(path.join(".")) ||
      /^(defaults|jobs\.[^.]+\.defaults)\.run$/.test(path.join("."));

    const walk = (
      node: unknown,
      path: string[],
      found: string[],
      commands: string[],
    ) => {
      if (Array.isArray(node)) {
        node.forEach((item, i) => {
          walk(item, [...path, String(i)], found, commands);
        });
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
          `^[^\\S\\n]*(?:-[^\\S\\n]+)?(?:[a-z-]+:[^\\S\\n]*)?${escaped}[^\\S\\n]+#`,
          "m",
        );
        if (truncated.test(text)) found.push(`tronquée sur # : ${single}`);
      }
      if (found.length) offenders[rel] = found;
    }
    expect(
      offenders,
      'an unquoted ":" makes YAML read a command as a ' +
        "mapping, and GitHub rejects the whole workflow",
    ).toEqual({});
  });

  // A PARAPHE_* the operator gives is an EXPLICIT override, reapplied over
  // what coordination edited in "Mon équipe". The chart shipped the nine
  // template values, so every pod restart wiped the campaign, re-armed the
  // "not configured" banner and put the batch size back — in silence, on
  // the default install.
  it("ship no campaign value that a restart would reimpose", () => {
    const values = readFileSync(
      join(ROOT, "chart", "paraphe", "values.yaml"),
      "utf8",
    );
    const campaign = /\ncampaign:\n((?: {2}.*\n|\n)*)/.exec(values);
    expect(campaign, "the campaign block moved or was renamed").toBeTruthy();
    const filled = [
      ...(campaign as RegExpExecArray)[1].matchAll(/^ {2}(\w+):\s*(.+)$/gm),
    ].filter(([, , v]) => v.trim() !== '""' && !v.trim().startsWith("#"));
    expect(
      filled.map(([, k, v]) => `${k}: ${v}`),
      "a default install would overwrite the campaign at every restart",
    ).toEqual([]);

    const env = readFileSync(join(ROOT, ".env.exemple"), "utf8");
    const lot = /^PARAPHE_BATCH_SIZE=(.*)$/m.exec(env);
    expect(lot?.[1].trim(), ".env.exemple reimposes the batch size").toBe("");
  });

  it("keep no leftover of the project's former names", () => {
    // the project carried other names before paraphe; a
    // leftover produces a database or an image that does not exist,
    // invisible until deployment
    const watched = [
      "docker-compose.yml",
      ".github/workflows/ci.yml",
      ".github/workflows/release.yml",
      "api/Dockerfile",
      ".env.exemple",
      "chart/paraphe/values.yaml",
      "chart/paraphe/Chart.yaml",
    ];
    // "parrainages" stays legitimate as a French word in comments; what is
    // hunted are the identifiers
    const patterns = [
      "CITOYEN_",
      "citoyen-",
      "/citoyen",
      ":citoyen",
      "/parrainages",
      "-U parrainages",
      "DB: parrainages",
    ];
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
    "DEPLOYMENT.md",
    "docker-compose.yml",
  ];
  const READ_IN = [
    "api",
    "outils",
    "noyau",
    "web/src",
    "web/vite.config.ts",
    "Taskfile.yml",
    ".github/workflows",
    "api/Dockerfile",
    "e2e",
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
          if (entry.name !== "node_modules" && entry.name !== "dist")
            walk(child);
        } else if (
          /\.(go|ts|tsx|yml|yaml|json)$/.test(entry.name) &&
          // tests excluded, THIS file first: naming the typo in a comment
          // was enough to make it pass for a variable the code reads
          !/\.test\.tsx?$|_test\.go$/.test(entry.name)
        ) {
          for (const v of variables(readFileSync(join(ROOT, child), "utf8"))) {
            found.add(v);
          }
        }
      }
    };
    for (const path of READ_IN) {
      if (statSync(join(ROOT, path)).isDirectory()) walk(path);
      else
        for (const v of variables(readFileSync(join(ROOT, path), "utf8"))) {
          found.add(v);
        }
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

  // And the other way round, which nothing checked: a setting declared in
  // api/settings.go and named in no operator-facing file exists only for
  // whoever reads Go. PARAPHE_LOG_LEVEL was exactly that — the one knob an
  // operator paged at 2am reaches for, and the guide never mentioned it.
  it("names every setting an operator has to know about", () => {
    const settings = readFileSync(join(ROOT, "api/settings.go"), "utf8");
    const declared = [
      ...settings.matchAll(/Env:\s*"(PARAPHE_[A-Z0-9_]+)"/g),
    ].map((m) => m[1]);
    expect(declared.length, "the settings table was not read").toBeGreaterThan(
      15,
    );

    // Image-embedded paths: the operator overrides them only when running
    // the binary outside the image, and DEPLOYMENT.md carries that case in
    // prose rather than as a variable to set.
    // Paths baked into the image, overridden only when the binary runs
    // outside it — DEPLOYMENT.md carries that case in prose.
    const developerOnly = new Set(["PARAPHE_CSV", "PARAPHE_CONFIG_DIR"]);
    const documented = new Set(
      NAMED_IN.flatMap((file) =>
        variables(readFileSync(join(ROOT, file), "utf8")),
      ),
    );
    const silent = declared.filter(
      (v) => !documented.has(v) && !developerOnly.has(v),
    );
    expect(silent).toEqual([]);
  });

  // The DEFAULT value drifting is the same failure one layer down: the guide
  // promised `campagne` while the binary answered `campaign`, so an operator
  // who trusted the doc pointed DNS at a subdomain that 404s.
  it("agrees with the deployment files on the bootstrap slug", () => {
    const settings = readFileSync(join(ROOT, "api/settings.go"), "utf8");
    const slug =
      /Env:\s*"PARAPHE_ORG_SLUG"[\s\S]{0,200}?Default:\s*"([^"]+)"/.exec(
        settings,
      );
    expect(slug, "the org_slug default was not found").not.toBeNull();
    const value = slug![1];
    for (const [file, pattern] of [
      [".env.exemple", new RegExp(`^PARAPHE_ORG_SLUG=${value}$`, "m")],
      [
        "chart/paraphe/values.yaml",
        new RegExp(`^\\s*orgSlug:\\s*${value}$`, "m"),
      ],
      ["DEPLOYMENT.md", new RegExp(`default \`${value}\``)],
    ] as [string, RegExp][]) {
      expect(
        readFileSync(join(ROOT, file), "utf8"),
        `${file} disagrees with api/settings.go, which defaults PARAPHE_ORG_SLUG to ${value}`,
      ).toMatch(pattern);
    }
  });
});
