// Invariants checked on the REAL outputs of `task build`.
//
// Unit tests cannot see these defects: they bear on the result of crossing
// 34,826 real rows. Nor can a reference-output comparison — a defect the
// reference shares is invisible to it. Only asserting the invariants on
// the real outputs catches both at once.

import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

import { parseRecords } from "../noyau/csv.ts";
import { norm } from "../noyau/texte.ts";
import {
  checkCivilities,
  checkHeader,
  compareIdentity,
  ENDORSEMENT_COLUMNS,
  loadEndorsements,
  RNE_COLUMNS,
} from "./build.ts";
import { ROOT } from "./config.ts";

const TARGETS = join(ROOT, "out", "01_maires_cibles_prioritaires.csv");
const built = existsSync(TARGETS);
const whenBuilt = built ? describe : describe.skip;

// `describe.skip` does not skip the BODY of the suite: anything read there
// runs anyway, and the file crashed on a machine without the outputs
// instead of standing aside. Hence the lazy reads below.
const targetRows = () => parseRecords(readFileSync(TARGETS, "utf8"));
const fullBase = () =>
  parseRecords(readFileSync(join(ROOT, "out", "04_base_complete.csv"), "utf8"));

// And a suite that stands aside proves nothing. Locally that is fine — the
// outputs come from `task build`, not from the repository. Where they were
// promised, it is not: these invariants are the only thing standing between
// a merged commune and 36 letters to the wrong town hall, and a green run
// that skipped them reads exactly like a green run that passed them.
//
// Gated on a variable donnees.yml sets, NOT on CI: GitHub sets CI=true in
// every job, including the one that deliberately does not cross the sources
// — so keying on it turned the whole unit suite red on every push, and
// release.yml goes through it.
describe("the invariants on the real outputs", () => {
  it("run wherever they were promised", () => {
    if (!process.env.PARAPHE_EXPECTED_OUTPUTS) return;
    expect(
      built,
      `${TARGETS} is missing: this job must cross the open ` +
        "sources before running this suite, otherwise it certifies nothing",
    ).toBe(true);
  });
});

whenBuilt("the mass-mailing file", () => {
  it("is not empty", () => {
    expect(targetRows().length).toBeGreaterThan(1000);
  });

  // The letter reads "Mairie de {commune}" and the body "au service de
  // {commune}". Taking the SIGNED commune instead of the one where the
  // official serves sent 36 letters to the wrong town hall — mergers and
  // commune changes.
  it("names the commune where the mayor serves, not the one they signed in", () => {
    const wrong = targetRows().filter(
      (r) => norm(r.commune) !== norm(r.commune_2026),
    );
    expect(
      wrong.map((r) => `${r.insee_code} ${r.commune}≠${r.commune_2026}`),
    ).toEqual([]);
  });

  // The "found by name" fallback only holds when the signed commune said
  // nothing. When it is in the RNE with another mayor, a departmental
  // namesake wins: 12 mayors thanked for an endorsement that is not
  // theirs.
  it('only invokes "renamed/merged commune" when it truly is', () => {
    const rne = parseRecords(
      readFileSync(join(ROOT, "data", "raw", "rne_maires.csv"), "utf8"),
    );
    const byDept = new Map<string, Map<string, string>>();
    for (const r of rne) {
      const values = Object.values(r);
      const dept = norm(values[1] || values[3]);
      let communes = byDept.get(dept);
      if (!communes) {
        communes = new Map();
        byDept.set(dept, communes);
      }
      communes.set(norm(values[5]), values[4]);
    }
    const suspicious = targetRows()
      .filter((r) => r.matching_confidence.startsWith("retrouvé par nom"))
      .filter((r) => {
        const insee = byDept.get(norm(r.department))?.get(norm(r.commune));
        // the letter's commune exists in the RNE but under ANOTHER INSEE
        // code: it is therefore neither a rename nor a merger
        return insee !== undefined && insee !== r.insee_code;
      });
    expect(suspicious.map((r) => `${r.insee_code} ${r.last_name}`)).toEqual([]);
  });

  // A commune absent from the RNE is not a merged commune: 912 communes
  // have a town hall and no mayor row. Invoking the fallback there thanked
  // Régine THOMAS, mayor of Robécourt, for an endorsement signed 130 km
  // away in Le Puid — a commune that never merged and still owns INSEE
  // 88362.
  it("does not invoke a merger when the signed commune still exists", () => {
    // The SIGNED commune is the whole point, and file 01 does not carry
    // it — it names the commune the mayor serves today. It has to come
    // back from the endorsement sources, or this test asserts nothing:
    // "Robécourt owns 88390" is trivially true, "Le Puid owns 88362 and is
    // not Robécourt" is the defect.
    // Joined with the crossing's OWN identity comparison, not on the
    // spelling: the whole point of this fallback is that the two sources
    // spell the name differently ("THOMAS CHINOUILH" at the Conseil
    // constitutionnel, "THOMAS" at the RNE). A join on the exact string
    // matches nothing and the test certifies nothing.
    const endorsements = loadEndorsements().filter((e) =>
      e.office.startsWith("Maire"),
    );
    const byDepartment = new Map<string, typeof endorsements>();
    for (const e of endorsements) {
      const dpt = norm(e.dept);
      byDepartment.set(dpt, [...(byDepartment.get(dpt) ?? []), e]);
    }

    const directory = parseRecords(
      readFileSync(join(ROOT, "data", "raw", "annuaire_mairies.csv"), "utf8"),
    );
    // Same index as the crossing: several communes share a label across
    // departments ("Montignac" exists in more than one), so the code is
    // only conclusive within the department.
    const dept = (insee: string) =>
      /^9[78]/.test(insee) ? insee.slice(0, 3) : insee.slice(0, 2);
    const ownInsee = new Map<string, string[]>();
    for (const card of directory) {
      if (/délégu|annexe/i.test(card.nom ?? "")) continue;
      const insee = (card.code_insee_commune ?? "").trim();
      if (!insee) continue;
      const key = norm((card.nom ?? "").replace(/^mairie[^-]*-\s*/i, ""));
      ownInsee.set(key, [...(ownInsee.get(key) ?? []), insee]);
    }
    const contradicted: string[] = [];
    for (const r of targetRows()) {
      if (!r.matching_confidence.startsWith("retrouvé par nom")) continue;
      const official = {
        lastName: r.last_name,
        firstName: r.first_name,
        sex: r.title === "Mme" ? "F" : "M",
        commune: r.commune,
        insee: r.insee_code,
        dept: r.department,
      };
      const signed = (byDepartment.get(norm(r.department)) ?? [])
        .filter((e) => compareIdentity(e, official) === "ok")
        .map((e) => norm(e.commune));
      for (const signedCommune of new Set(signed)) {
        const own = (ownInsee.get(signedCommune) ?? []).find(
          (insee) => dept(insee) === dept(r.insee_code),
        );
        if (own !== undefined && own !== r.insee_code) {
          contradicted.push(
            `${r.insee_code} ${r.first_name} ${r.last_name} ` +
              `(${r.commune_2026}) ← signé à ${signedCommune}, qui possède ${own}`,
          );
        }
      }
    }
    expect(contradicted).toEqual([]);
  });

  it("leaves no endorsement claim without a candidate and a year", () => {
    const empty = targetRows().filter(
      (r) => !r.recent_candidate || !r.recent_year,
    );
    expect(empty.map((r) => r.insee_code)).toEqual([]);
  });
});

describe("the civility domain", () => {
  it("accepts the two spellings actually present in the sources", () => {
    expect(() =>
      checkCivilities([{ civ: "M" }, { civ: "M." }, { civ: "Mme" }]),
    ).not.toThrow();
  });

  // The day a file writes "Mr", the sex discriminant would silently switch
  // off and Christine would inherit Christian's endorsement.
  it("refuses an unknown spelling rather than disarming", () => {
    expect(() => checkCivilities([{ civ: "Mr" }])).toThrow(
      /outside the domain/,
    );
    expect(() => checkCivilities([{ civ: "" }])).toThrow(/outside the domain/);
  });
});

// The RNE and the endorsement files are read BY POSITION — r[4] is the
// commune, r[8] the sex code — because their headers carry no stable
// machine name. A column inserted upstream therefore shifts every identity
// by one, silently, and `task download` refreshes these files unattended.
// Measured before this guard existed: one inserted column produced file 01
// with its header alone, file 04 with 34 826 rows of garbage, and exit 0.
describe("the sources read by position", () => {
  const RNE = [
    "Code du département",
    "Libellé du département",
    "Code de la collectivité à statut particulier",
    "Libellé de la collectivité à statut particulier",
    "Code de la commune",
    "Libellé de la commune",
    "Nom de l'élu",
    "Prénom de l'élu",
    "Code sexe",
  ];

  it("accepts the header as the sources actually publish it", () => {
    expect(() => checkHeader("rne_maires.csv", RNE, RNE_COLUMNS)).not.toThrow();
  });

  it("halts when a column is inserted upstream", () => {
    expect(() =>
      checkHeader("rne_maires.csv", ["Nouvelle", ...RNE], RNE_COLUMNS),
    ).toThrow(/columns have moved/);
  });

  // The two years do not spell the candidate column the same way. Reading
  // one of them by the other's header is what a naive check would impose.
  it.each([
    ["Candidat", 2022],
    ["Candidat-e parrainé-e", 2017],
  ])("accepts %s, the spelling of %i", (spelling) => {
    const header = [
      "Civilité",
      "Nom",
      "Prénom",
      "Mandat",
      "Circonscription",
      "Département",
      String(spelling),
    ];
    expect(() =>
      checkHeader("parrainages.csv", header, ENDORSEMENT_COLUMNS),
    ).not.toThrow();
  });

  it("refuses a spelling nobody has seen", () => {
    const header = [
      "Civilité",
      "Nom",
      "Prénom",
      "Mandat",
      "Circonscription",
      "Département",
      "Personne parrainée",
    ];
    expect(() =>
      checkHeader("parrainages.csv", header, ENDORSEMENT_COLUMNS),
    ).toThrow(/columns have moved/);
  });
});

// "Sous la municipalité précédente" has to be true on both counts: the
// endorser must have been THE mayor, and recently enough for "previous" to
// mean anything. A 2017 endorser led the 2014-2020 term — two elections
// ago (2020, then March 2026) — so the sentence contradicted the year it
// cited on 1 710 cards. The RNE cannot settle it: all 34 826 mandates
// begin in 2026.
whenBuilt("the communal precedent, on the real base", () => {
  it("never claims the previous term for an endorsement older than it", () => {
    const base = fullBase();
    const wrong = base.filter(
      (r) =>
        r.predecessor_mayor === "oui" &&
        !r.endorsement_history.includes("2022"),
    );
    expect(
      wrong.map(
        (r) => `${r.insee_code} ${r.commune} — ${r.endorsement_history}`,
      ),
    ).toEqual([]);
  });

  // The whole history is cited in that sentence — "avait présenté X en
  // 2017 et 2022" — so a history spanning both years puts half the claim
  // two municipal elections back.
  it("never claims it for a history that reaches beyond the previous term", () => {
    const spanning = fullBase().filter(
      (r) =>
        r.predecessor_mayor === "oui" && r.endorsement_history.includes("2017"),
    );
    expect(
      spanning.map((r) => `${r.insee_code} ${r.endorsement_history}`),
    ).toEqual([]);
  });

  // and the ones that DO qualify keep saying it: the fix must not silence
  // a true and useful sentence
  it("still claims it when the endorsement is from the previous term", () => {
    const kept = fullBase().filter((r) => r.predecessor_mayor === "oui");
    expect(kept.length).toBeGreaterThan(500);
  });
});
