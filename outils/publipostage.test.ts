// The mass mailing, run for real: the actual script, a synthetic list, a
// campaign supplied the way an operator supplies one. The unit tests prove
// the engine sentence by sentence; nothing proved that `task messages`
// produces the files a campaign prints and imports — which is the moment
// 1 960 letters leave, and the wrong moment to learn.

import { spawnSync } from "node:child_process";
import {
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { afterAll, describe, expect, it } from "vitest";

import { parseRecords } from "../noyau/csv.ts";
import {
  emailAddresses,
  incompleteAddress,
  type Mayor,
} from "../noyau/messages.ts";
import { main as fauxJeu } from "./faux-jeu.ts";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const work = mkdtempSync(join(tmpdir(), "paraphe-publipostage-"));
afterAll(() => rmSync(work, { recursive: true, force: true }));

/** A campaign the way an operator writes one: every key filled, no template
 *  value left. French values — these reach mayors verbatim. */
const CAMPAIGN_ENV = {
  PARAPHE_CANDIDATE: "Camille Exemple",
  PARAPHE_CANDIDATE_DESCRIPTION: "candidate écologiste, médecin",
  PARAPHE_CANDIDATE_DESCRIPTION_LONG:
    "Je suis médecin. Je porte la santé environnementale.",
  PARAPHE_SIGNATORY: "Sacha Mandataire",
  PARAPHE_SIGNATORY_ROLE: "mandataire de la campagne",
  PARAPHE_CONTACT_PHONE: "06 12 34 56 78",
  PARAPHE_CONTACT_EMAIL: "contact@exemple-fictif.fr",
  PARAPHE_SITE: "https://exemple-fictif.fr",
  PARAPHE_SENDING_CITY: "Lyon",
  PARAPHE_BATCH_SIZE: "10",
};

function runMailing(out: string, extraEnv: Record<string, string> = {}) {
  return spawnSync("node", ["outils/messages-masse.ts"], {
    cwd: ROOT,
    encoding: "utf8",
    env: {
      ...process.env,
      ...CAMPAIGN_ENV,
      PARAPHE_TARGETS_CSV: join(
        work,
        "data",
        "01_maires_cibles_prioritaires.csv",
      ),
      PARAPHE_MESSAGES_DIR: out,
      ...extraEnv,
    },
  });
}

describe("the mass mailing, end to end", () => {
  const dataDir = join(work, "data");
  fauxJeu(dataDir);
  const targets = parseRecords(
    readFileSync(join(dataDir, "01_maires_cibles_prioritaires.csv"), "utf8"),
  ) as Mayor[];

  const out = join(work, "messages");
  mkdirSync(out, { recursive: true });
  const run = runMailing(out);

  it("produces the four files and exits 0", () => {
    expect(run.status, run.stderr).toBe(0);
    expect(readdirSync(out).sort()).toEqual([
      "a_verifier.csv",
      "courriers.html",
      "emails.csv",
      "sans_email.csv",
    ]);
  });

  it("says which file it read, before anything else", () => {
    // the operator's one chance to notice a stale or wrong list: the source
    // path and the recipient count, first line out
    expect(run.stdout).toContain(
      `source : ${join(work, "data", "01_maires_cibles_prioritaires.csv")} ` +
        `(${targets.length} maires)`,
    );
  });

  it("refuses any list that is not file 01", () => {
    // The cardinal invariant, now that the path is overridable: the shape
    // decides, not the path. The FULL base is 34,826 mayors of spam; files
    // 02 and 03 are the people « vous avez parrainé » would LIE to — a
    // former endorser whose commune changed mayors, an unmatched row. The
    // 02/03 header is built the way build.ts builds it (cols1 minus its own
    // notInScope, plus status), read out of build.ts rather than copied:
    // a copy is the referee that drifts.
    const build = readFileSync(join(ROOT, "outils", "build.ts"), "utf8");
    const notInScope = /const notInScope = new Set\(\[([^\]]*)\]\)/.exec(build);
    expect(notInScope, "notInScope not found in build.ts").not.toBeNull();
    const stripped = new Set(
      [...(notInScope?.[1] ?? "").matchAll(/"([^"]+)"/g)].map((m) => m[1]),
    );
    expect(stripped.has("commune_2026"), "the guard's premise").toBe(true);
    const header01 = readFileSync(
      join(work, "data", "01_maires_cibles_prioritaires.csv"),
      "utf8",
    )
      .split("\r\n")[0]
      .replace(/^\uFEFF/, "")
      .split(";");
    const cols02 = [...header01.filter((c) => !stripped.has(c)), "status"];

    const shapes: [string, string][] = [
      ["04", join(work, "data", "04_base_complete.csv")],
      ["02", join(work, "former.csv")],
    ];
    writeFileSync(
      join(work, "former.csv"),
      `${cols02.join(";")}\r\n${cols02.map(() => "x").join(";")}\r\n`,
      "utf8",
    );
    for (const [label, path] of shapes) {
      const untouched = join(work, `refused-${label}`);
      mkdirSync(untouched, { recursive: true });
      const refused = runMailing(untouched, { PARAPHE_TARGETS_CSV: path });
      expect(refused.status, `file ${label} was accepted`).not.toBe(0);
      expect(refused.stderr).toContain("la forme du fichier 01");
      expect(readdirSync(untouched)).toEqual([]);
    }
  });

  it("mails the priority file, all of it and nothing else", () => {
    // the referee is the engine's own reading of the SAME list: every mayor
    // with a usable address gets an email row, every one without gets the
    // postal file, and the two together are the whole file 01 — never the
    // 34 826 of the full base, which is the spam the mailing must not be
    const reachable = targets.filter((m) => emailAddresses(m).valid.length);
    const emails = parseRecords(readFileSync(join(out, "emails.csv"), "utf8"));
    const postal = parseRecords(
      readFileSync(join(out, "sans_email.csv"), "utf8"),
    );
    expect(emails.length).toBe(reachable.length);
    expect(emails.length + postal.length).toBe(targets.length);
  });

  it("speaks to an endorser as an endorser, in the volunteer's name", () => {
    const emails = parseRecords(readFileSync(join(out, "emails.csv"), "utf8"));
    const first = emails[0] as { subject: string; body: string };
    // file 01 carries endorsers only, so every message THANKS: the recent
    // candidate and year are said, and no placeholder leaks through
    expect(first.subject).toContain("2022");
    expect(first.body).toContain("Alex Exemple");
    expect(first.body).toContain("2022");
    expect(first.body).not.toMatch(/\{[a-z_]+\}/);
    // whoever sends it is whoever signs it
    expect(first.body).toContain("Sacha Mandataire, mandataire de la campagne");
  });

  it("prints one letter per deliverable address, signed by the sender", () => {
    const letters = readFileSync(join(out, "courriers.html"), "utf8");
    const deliverable = targets.filter((m) => !incompleteAddress(m));
    expect(letters.match(/class="lettre"/g)?.length).toBe(deliverable.length);
    // the envelope window shows the SENDER — the person who posts it
    expect(letters).toContain(
      '<div class="expediteur">Sacha Mandataire, mandataire de la campagne',
    );
    expect(letters).not.toMatch(/\{[a-z_]+\}/);
  });

  it("promises no call: the campaign never opted in", () => {
    const emails = readFileSync(join(out, "emails.csv"), "utf8");
    const letters = readFileSync(join(out, "courriers.html"), "utf8");
    expect(emails).not.toContain("quelques minutes par téléphone");
    expect(letters).not.toContain("Nous nous permettrons de vous appeler");
  });

  it("refuses to run while the campaign still carries template values", () => {
    const untouched = join(work, "refused");
    mkdirSync(untouched, { recursive: true });
    // « Prénom NOM » is the shipped placeholder: sent as-is, it reaches
    // hundreds of mayors verbatim — the exact failure the gate exists for
    const refused = runMailing(untouched, { PARAPHE_CANDIDATE: "Prénom NOM" });
    expect(refused.status).not.toBe(0);
    expect(refused.stderr).toContain("valeurs de gabarit");
    // and nothing replaced the previous mailing: the refusal writes no file
    expect(readdirSync(untouched)).toEqual([]);
  });
});
