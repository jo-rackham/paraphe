// Crosses the 2017/2022 presidential endorsements (Conseil constitutionnel),
// the national register of elected officials (RNE, mayors in office) and the
// government services directory (town hall contacts) to produce the list of
// mayors who endorsed "small" candidates and are still in office, with their
// contact details.
//
// The pieces live beside this file: sources.ts reads and validates the three
// sources, matching.ts holds the identity rules. This file keeps the
// classification — the definition of "small candidate", meant to be adjusted
// here — and the crossing itself.
//
// Outputs in out/ (file names stay French — the campaign team opens them):
//   01_maires_cibles_prioritaires.csv  mayors still in office (the target)
//   02_anciens_parrains.csv            endorsers not found as mayors in 2026
//   03_non_apparies.csv                rows to check by hand
//   04_base_complete.csv               every mayor, ordered by signal
//   rapport.md                         methodology + stats (French: team doc)
//
// Run with `node outils/build.ts` — no compilation, no dependency.

import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

import { writeCsv } from "../noyau/csv.ts";
import { proseName, RANKS } from "../noyau/messages.ts";
import {
  closestMatch,
  communeLabel,
  norm,
  stripControls,
  tight,
} from "../noyau/texte.ts";
import { ROOT } from "./config.ts";
import {
  cmp,
  compareIdentity,
  dedupeByInsee,
  keyAmong,
  type Person,
  type Target,
} from "./matching.ts";
import {
  loadDirectory,
  loadEndorsements,
  loadRne,
  type Official,
  YEARS,
} from "./sources.ts";

const OUT = join(ROOT, "out");

// ---------------------------------------------------------------------------
// Candidate classification — the definition of "small candidate".
//
// A = strong signal: a real candidate (>=5 endorsements) who did NOT reach
//     the 500, mainstream figures excluded. Endorsing a candidate who risked
//     not qualifying is the clearest pluralist gesture.
// B = real signal: outsiders who qualified narrowly (< ~710) with a large
//     share of "republican endorsements", + Taubira (a campaign outside any
//     party machine that stayed under 500).
// Everything else (mainstream, courtesy endorsements of non-candidates,
// party figures) does not count as a pluralist signal.
// The keys are the EXACT names from the CC files, spaces collapsed.
// ---------------------------------------------------------------------------
const TIER_A: Record<number, Set<string>> = {
  2022: new Set([
    "ASSELINEAU François",
    "KAZIB Anasse",
    "THOUY Hélène",
    "KOENIG Gaspard",
    "KUZMANOVIC Georges",
    "MIGUET Nicolas",
    "EGGER Clara",
    "CHICHE Arnaud",
    "MARTINEZ Antoine",
    "FORTANÉ Jean-Marc",
    "SMATI Rafik",
    "ROCCA Martin",
    "CAU Marie",
    "WAECHTER Antoine",
    "MEURICE Guillaume",
    "BÉKAERT Corinne",
  ]),
  2017: new Set([
    "JARDIN Alexandre",
    "MARCHANDISE Charlotte",
    "TEMARU Oscar",
    "TAUZIN Didier",
    "GORGES Jean-Pierre",
    "TROADEC Christian",
    "LARROUTUROU Pierre",
    "FAUDOT Bastien",
    "MIGUET Nicolas",
    "MUMBACH Paul",
    "WAECHTER Antoine",
    "TONIUTTI Emmanuel",
    "GUYOT Stéphane",
    "REGIS Olivier",
    "NIKONOFF Jacques",
  ]),
};

const TIER_B: Record<number, Set<string>> = {
  2022: new Set([
    "POUTOU Philippe",
    "ARTHAUD Nathalie",
    "LASSALLE Jean",
    "TAUBIRA Christiane",
  ]),
  2017: new Set([
    "ARTHAUD Nathalie",
    "POUTOU Philippe",
    "CHEMINADE Jacques",
    "ASSELINEAU François",
    "LASSALLE Jean",
  ]),
};

// Candidates whose campaign explicitly bore on democratic functioning
// (citizen primary, citizens' initiative referendum, subsidiarity, popular
// sovereignty, decentralisation). The tag qualifies a PUBLIC ACT — having
// endorsed that candidacy — not a conviction: we do not presume the
// official's sincerity, we record what they signed.
const DEMOCRATIC_THEME = [
  "MARCHANDISE Charlotte", // LaPrimaire.org, citizen primary
  "JARDIN Alexandre", // citizen movement, hands-on democracy
  "NIKONOFF Jacques", // popular sovereignty, constituent assembly
  "FAUDOT Bastien", // VIth Republic
  "EGGER Clara", // citizens' initiative referendum
  "KOENIG Gaspard", // subsidiarity, decentralisation
  "TROADEC Christian", // decentralisation, local democracy
];

/**
 * Department code of an INSEE code. Overseas takes three digits (97120 is
 * Martinique, not department 97), mainland two — 2A/2B included, Corsican
 * INSEE codes carrying the letter.
 */
function departmentOfInsee(insee: string): string {
  return /^9[78]/.test(insee) ? insee.slice(0, 3) : insee.slice(0, 2);
}

/** Python's `max(iterable, key=…)`: returns the FIRST maximal element. */
function maxBy<T>(items: T[], key: (x: T) => string): T {
  let best = items[0];
  let bestKey = key(items[0]);
  for (const x of items.slice(1)) {
    const k = key(x);
    if (k > bestKey) {
      best = x;
      bestKey = k;
    }
  }
  return best;
}

const tagYear = (tag: string): string => tag.split(":")[0];
const tagCandidate = (tag: string): string =>
  tag.slice(tag.indexOf(": ") + 2).replace(/ \([AB]\)$/, "");

export function main(): void {
  mkdirSync(OUT, { recursive: true });
  const endorsements = loadEndorsements();
  const { byCommune, byPerson } = loadRne();
  const contacts = loadDirectory();

  // Which communes EXIST, independently of who leads them. The RNE only
  // knows communes that have a mayor row; the directory knows the town
  // halls. The difference — a commune with a town hall and no RNE row — is
  // exactly where "renamed/merged commune" must not be invoked.
  const communesByLabel = new Map<string, string[]>();
  for (const [insee, c] of contacts) {
    // A commune whose only card is a DELEGATE town hall is a merged
    // commune — that card keeps the historical INSEE (Béon, 01039, inside
    // Culoz-Béon), and counting it would refuse the very mergers the
    // fallback exists for.
    if (/délégu|annexe/i.test(c.cardName)) continue;
    // "Mairie - Le Puid" and the address's own commune name: two spellings
    // of the same place, and either may be the one signed
    const label = c.cardName.replace(/^mairie[^-]*-\s*/i, "");
    for (const name of [label, c.city]) {
      const key = norm(name);
      if (!key) continue;
      const known = communesByLabel.get(key);
      if (known) known.push(insee);
      else communesByLabel.set(key, [insee]);
    }
  }
  // Department label -> department code, learned from the RNE itself: the
  // endorsement files name departments, the directory numbers them.
  const deptCode = new Map<string, string>();
  for (const [deptN, communes] of byCommune) {
    const first = communes.values().next().value;
    if (first) deptCode.set(deptN, departmentOfInsee(first.insee));
  }

  // totals per candidate (for the report) — first-appearance order
  const totals = new Map<number, Map<string, number>>(
    YEARS.map((y) => [y, new Map()]),
  );
  for (const p of endorsements) {
    const t = totals.get(p.year) as Map<string, number>;
    t.set(p.candidate, (t.get(p.candidate) ?? 0) + 1);
  }
  // the configuration keys do exist in the data
  for (const y of [2017, 2022]) {
    for (const c of [...TIER_A[y], ...TIER_B[y]]) {
      if (!(totals.get(y) as Map<string, number>).has(c)) {
        throw new Error(
          `unknown candidate in the ${y} configuration: ${JSON.stringify(c)}`,
        );
      }
    }
  }

  // --- aggregate per person (mayors only) ----------------------------------
  const persons = new Map<string, Person>();
  for (const p of endorsements) {
    if (!p.office.startsWith("Maire")) continue;
    // the first name is part of the identity: without it, two namesakes of
    // the same commune (predecessor/successor, spouses) merge and the
    // current mayor inherits the other's endorsements
    const deptN = norm(p.dept);
    const communeN = norm(p.commune);
    const key = keyAmong(p, persons);
    let rec = persons.get(key);
    if (!rec) {
      rec = {
        civ: p.civ,
        lastName: p.lastName,
        firstName: p.firstName,
        commune: p.commune,
        dept: p.dept,
        office: p.office,
        small: [],
        others: [],
        years: new Set(),
        score: 0,
        deptN,
        communeN,
      };
      persons.set(key, rec);
    }
    const tag = `${p.year}: ${p.candidate}`;
    if (TIER_A[p.year].has(p.candidate)) {
      rec.small.push(`${tag} (A)`);
      rec.score += 2;
      rec.years.add(p.year);
    } else if (TIER_B[p.year].has(p.candidate)) {
      rec.small.push(`${tag} (B)`);
      rec.score += 1;
      rec.years.add(p.year);
    } else {
      rec.others.push(tag);
    }
  }

  const targets: Person[] = [...persons.values()].filter((v) => v.small.length);
  for (const v of targets) if (v.years.size >= 2) v.score += 1;

  // --- 2026 RNE matching ---------------------------------------------------
  const stillMayor: Target[] = [];
  const former: Person[] = [];
  const unmatched: Person[] = [];
  // stat labels are French: they go verbatim into rapport.md, a team document
  const stats = new Map<string, number>();
  const count = (k: string) => stats.set(k, (stats.get(k) ?? 0) + 1);

  for (const rec of targets) {
    const { deptN, communeN } = rec;
    const deptCommunes = byCommune.get(deptN) ?? new Map<string, Official>();
    let row = deptCommunes.get(communeN);
    let approx = false;
    if (row === undefined && deptCommunes.size) {
      // THE SAME NAME WITHOUT ITS SEPARATORS, and it is an EXACT tier and not a
      // similarity: « Cramchaban » at the Conseil constitutionnel is
      // « Cram-Chaban » in the register, and `norm` cannot say so because it
      // turns a hyphen into a space.
      //
      // It matters far past the spelling. Reached by the fuzzy fallback
      // instead, the match is `approx`, and `approx` DISABLES the counter
      // proof below — so a commune whose mayor has since changed could not be
      // concluded « successeur en place » and fell to file 03 « à vérifier »
      // instead of file 02. Laurent RENAUD, who presented Jean Lassalle in
      // 2017 and 2022, is no longer mayor of Cram-Chaban — Martine DURVAUX
      // is — and that is exactly the case the crossing is supposed to state.
      //
      // Safe to prefer over the fuzzy tier because it is not a similarity:
      // measured on the register, no two distinct communes of one department
      // share a tight form, and an ambiguous one falls through rather than
      // guessing.
      const sameTight = [...deptCommunes.keys()].filter(
        (name) => tight(name) === tight(communeN),
      );
      if (sameTight.length === 1) {
        row = deptCommunes.get(sameTight[0]);
      } else {
        // high cutoff: at 0.87, "Goncourt" catches "Voncourt" and
        // "Esnes-en-Argonne" catches "Gesnes-en-Argonne"
        const close = closestMatch(communeN, deptCommunes.keys(), 0.93);
        if (close !== null) {
          row = deptCommunes.get(close);
          approx = true;
        }
      }
    }

    const verdict = row !== undefined ? compareIdentity(rec, row) : null;
    let rneRow: Official | null = null;
    let conf = "";
    if (verdict === "ok") {
      rneRow = row as Official;
      conf = approx ? "commune approchée" : "exact";
      count("même commune, même maire");
    } else {
      // Renamed/merged commune: is the person mayor elsewhere in the same
      // department?
      //
      // This fallback only holds when the signed commune said nothing.
      // When it is in the RNE with another mayor, we have proof the
      // endorser is gone: letting a departmental namesake win thanked 12
      // mayors for an endorsement that is not theirs, under the
      // "renamed/merged commune" label the data contradicts.
      // successor in place: the commune IS in the RNE, under someone else
      const counterProof = verdict === "different" && !approx;
      const hits = counterProof
        ? []
        : (byPerson.get(deptN) ?? []).filter(
            (r) => compareIdentity(rec, r) === "ok",
          );
      // A commune absent from the RNE is not thereby a merged commune: it
      // may simply have no mayor row this month — 912 communes have a town
      // hall in the directory and none in the RNE. The signed commune's own
      // INSEE settles it. Renamed or merged, it points at the commune the
      // namesake leads (Vertus and Blancs-Coteaux are both 51612); a
      // DIFFERENT code means two distinct communes, and the fallback would
      // thank someone 130 km away for an endorsement that is not theirs.
      const signedInsee =
        row === undefined
          ? (communesByLabel.get(communeN) ?? []).find(
              (insee) => departmentOfInsee(insee) === deptCode.get(deptN),
            )
          : undefined;
      const otherCommune =
        hits.length === 1 &&
        signedInsee !== undefined &&
        signedInsee !== hits[0].insee;
      if (hits.length === 1 && !otherCommune) {
        rneRow = hits[0];
        // THE LABEL STATES WHAT WAS OBSERVED, not what this branch usually
        // means. « Commune renommée ou fusionnée » is the reason the person
        // was found elsewhere ONLY when the signed commune is absent from the
        // register; reached with the commune present — which happens when the
        // spelling was approximate and the identity there did not match — it
        // would name a merger the data contradicts. Measured on the real
        // sources, all 21 rows are the first case, so this changes no output
        // today; it is written this way so the day it does, the file says so
        // instead of asserting a merger nobody can find.
        conf =
          row === undefined
            ? "retrouvé par nom (commune renommée/fusionnée)"
            : "retrouvé par nom (même commune, identité rapprochée)";
        count("retrouvé par nom");
      } else if (counterProof) {
        count("commune trouvée, maire différent");
        rec.status = "plus maire (successeur en place)";
        // the commune's INSEE qualifies the successor: the commune already
        // presented a small candidate, they did not
        rec.communeInsee = (row as Official).insee;
        former.push(rec);
        continue;
      } else {
        // assert nothing: neither "still mayor" nor "successor"
        count("à vérifier à la main");
        rec.status =
          "à vérifier : " +
          (otherCommune
            ? "commune sans maire au RNE, homonyme dans une autre commune"
            : row === undefined
              ? "commune introuvable"
              : "identité proche mais non certaine") +
          (row === undefined
            ? ""
            : ` (RNE : ${row.firstName} ${row.lastName}, ${row.commune})`);
        unmatched.push(rec);
        continue;
      }
    }
    stillMayor.push({
      ...rec,
      rne: rneRow,
      contact: contacts.get(rneRow.insee),
      conf,
    });
  }

  // dedup: the same person counted twice when the commune or name spelling
  // (compound, accents) differs between 2017 and 2022 -> merge. The first
  // name is already checked against the RNE: at equal INSEE it is the same
  // person — otherwise it is a matching bug, which we want to see.
  const { kept, mergedSpellings } = dedupeByInsee(stillMayor);

  // --- writing -------------------------------------------------------------
  const priority = (rec: Person): string =>
    rec.small.some((t) => t.includes("(A)")) ? "P1" : "P2";
  const democraticTheme = (rec: Person): string =>
    [...rec.small, ...rec.others].some((t) =>
      DEMOCRATIC_THEME.some((c) => t.includes(c)),
    )
      ? "oui"
      : "";
  const common = (rec: Person): Record<string, string> => ({
    priority: priority(rec),
    score: String(rec.score),
    democratic_theme_endorsement: democraticTheme(rec),
    title: rec.civ,
    first_name: rec.firstName,
    last_name: rec.lastName,
    commune: rec.commune,
    department: rec.dept,
    small_candidate_endorsements: [...rec.small].sort(cmp).join(" | "),
    other_endorsements: [...rec.others].sort(cmp).join(" | "),
  });

  const cols1 = [
    "priority",
    "score",
    "democratic_theme_endorsement",
    "title",
    "first_name",
    "last_name",
    "commune",
    "department",
    "insee_code",
    "small_candidate_endorsements",
    "other_endorsements",
    "recent_candidate",
    "recent_year",
    "email",
    "phone",
    "town_hall_hours",
    "postal_address",
    "postal_code",
    "city",
    "website",
    "contact_form",
    "commune_2026",
    "matching_confidence",
  ];

  const rows1 = kept.map((r) => {
    const c = r.contact;
    const rne = r.rne;
    // civil status, gender and department from the RNE (official and up to
    // date) rather than from the variable endorsement files: two Conseil
    // constitutionnel civilities contradict the RNE
    const civ = rne.sex === "F" ? "Mme" : rne.sex === "M" ? "M." : r.civ;
    // The COMMUNE also comes from the RNE: the endorsement file's is the
    // SIGNED commune, which a merger makes the wrong one, as does the case where the
    // official changed town halls — 36 letters read "Mairie de
    // <elsewhere>". The spelling, though, is taken where it is best: the
    // directory first, then the endorsement file if it designates the same
    // commune, else the RNE, which writes "Rieux-En-Val".
    const commune = communeLabel(
      communeLabel(rne.commune, c?.city ?? ""),
      r.commune,
    );
    const official: Person = {
      ...r,
      lastName: rne.lastName,
      firstName: rne.firstName,
      dept: rne.dept,
      civ,
      commune,
    };
    // most recent "small candidate" endorsement: fields ready for the mass
    // mailing ("en {annee_recente}, vous avez présenté {candidat_recent}")
    const recent = maxBy(r.small, tagYear);
    return {
      ...common(official),
      insee_code: rne.insee,
      email: c?.email ?? "",
      phone: c?.phone ?? "",
      town_hall_hours: c?.hours ?? "",
      postal_address: stripControls(c?.street ?? ""),
      postal_code: c?.zip ?? "",
      city: stripControls(c?.city ?? ""),
      website: c?.website ?? "",
      contact_form: c?.contactForm ?? "",
      recent_candidate: proseName(tagCandidate(recent)),
      recent_year: tagYear(recent),
      commune_2026: rne.commune,
      matching_confidence: r.conf,
    };
  });
  writeFileSync(
    join(OUT, "01_maires_cibles_prioritaires.csv"),
    writeCsv(cols1, rows1),
    "utf8",
  );

  const notInScope = new Set([
    "insee_code",
    "email",
    "phone",
    "town_hall_hours",
    "postal_address",
    "postal_code",
    "city",
    "website",
    "contact_form",
    "commune_2026",
    "matching_confidence",
    "recent_candidate",
    "recent_year",
  ]);
  const cols2 = [...cols1.filter((c) => !notInScope.has(c)), "status"];
  for (const [name, rows] of [
    ["02_anciens_parrains.csv", former],
    ["03_non_apparies.csv", unmatched],
  ] as [string, Person[]][]) {
    const sorted = [...rows].sort(
      (a, b) => b.score - a.score || cmp(a.dept, b.dept),
    );
    writeFileSync(
      join(OUT, name),
      writeCsv(
        cols2,
        sorted.map((r) => ({ ...common(r), status: r.status ?? "" })),
      ),
      "utf8",
    );
  }

  // --- full base: every mayor in France, ordered by signal ------------------
  // The 500 signatures cannot come from the priority targets alone (at
  // 10-15% conversion they cap around 250). The team must be able to widen
  // — while always knowing whom they write to: the rank drives the message
  // template, and "you already endorsed" is said ONLY to those who did.
  const targetsByInsee = new Map(kept.map((r) => [r.rne.insee, r]));
  const communesThatEndorsed = new Map<string, Person>();
  for (const r of former) {
    const i = r.communeInsee;
    if (!i || targetsByInsee.has(i)) continue;
    const held = communesThatEndorsed.get(i);
    if (!held || r.score > held.score) communesThatEndorsed.set(i, r);
  }

  const cols4 = [
    "rank",
    "rank_label",
    "score",
    "priority",
    "democratic_theme_endorsement",
    "title",
    "first_name",
    "last_name",
    "commune",
    "department",
    "insee_code",
    "endorsement_history",
    "predecessor",
    "predecessor_mayor",
    "recent_candidate",
    "recent_year",
    "email",
    "phone",
    "town_hall_hours",
    "postal_address",
    "postal_code",
    "city",
    "website",
    "contact_form",
  ];
  const rankOrder = Object.keys(RANKS);
  const baseRows: Record<string, string>[] = [];
  const rankCounts = new Map<string, number>(rankOrder.map((r) => [r, 0]));
  for (const people of byPerson.values()) {
    for (const row of people) {
      const insee = row.insee;
      const c = contacts.get(insee);
      const target = targetsByInsee.get(insee);
      const past = communesThatEndorsed.get(insee);
      let rankKey: string;
      let hist: string;
      let predecessor = "";
      // empty by default: outside a communal precedent the column
      // means nothing, and "oui" on a mayor who endorsed themselves
      // is a claim about a predecessor there is none of
      let predecessorMayor = "";
      if (target) {
        rankKey = "has_endorsed";
        hist = [...target.small].sort(cmp).join(" | ");
      } else if (past) {
        rankKey = "commune_has_endorsed";
        hist = [...past.small].sort(cmp).join(" | ");
        predecessor = `${past.firstName} ${past.lastName}`;
        // "sous la municipalité précédente" has to be true on both counts.
        // A deputy mayor is not THE mayor — and a 2017 endorser led the
        // 2014-2020 term, two elections ago (2020, then March 2026), so
        // the sentence contradicts the year it cites. A local official
        // spots either one immediately. The RNE cannot settle it: all
        // 34 826 mandates begin in 2026, nothing says who led in between.
        // ONLY 2022. A history spanning both years is cited whole — "avait
        // présenté X en 2017 et 2022" — and the 2017 half belongs to the
        // 2014-2020 term, two municipal elections back. Those 380 lines
        // fall to "par le passé", which is true of both years.
        predecessorMayor =
          past.office === "Maire" &&
          past.years.has(2022) &&
          !past.years.has(2017)
            ? "oui"
            : "";
      } else {
        rankKey = "no_signal";
        hist = "";
      }
      rankCounts.set(rankKey, (rankCounts.get(rankKey) ?? 0) + 1);
      const recent = target ? maxBy(target.small, tagYear) : null;
      baseRows.push({
        rank: rankKey,
        rank_label: RANKS[rankKey],
        score: String(target ? target.score : 0),
        priority: target ? priority(target) : "",
        democratic_theme_endorsement: target ? democraticTheme(target) : "",
        title: row.sex === "F" ? "Mme" : "M.",
        first_name: row.firstName,
        last_name: row.lastName,
        commune: communeLabel(row.commune, c?.city ?? ""),
        department: row.dept,
        insee_code: insee,
        endorsement_history: hist,
        predecessor,
        predecessor_mayor: predecessorMayor,
        recent_candidate: recent ? proseName(tagCandidate(recent)) : "",
        recent_year: recent ? tagYear(recent) : "",
        email: c?.email ?? "",
        phone: c?.phone ?? "",
        town_hall_hours: c?.hours ?? "",
        postal_address: stripControls(c?.street ?? ""),
        postal_code: c?.zip ?? "",
        city: stripControls(c?.city ?? ""),
        website: c?.website ?? "",
        contact_form: c?.contactForm ?? "",
      });
    }
  }
  baseRows.sort(
    (a, b) =>
      rankOrder.indexOf(a.rank) - rankOrder.indexOf(b.rank) ||
      Number(b.score) - Number(a.score) ||
      cmp(a.department, b.department) ||
      cmp(a.commune, b.commune),
  );
  writeFileSync(
    join(OUT, "04_base_complete.csv"),
    writeCsv(cols4, baseRows),
    "utf8",
  );

  // --- report (French: it is a document for the campaign team) -------------
  const nEmail = kept.filter((r) => r.contact?.email).length;
  const nAddr = kept.filter((r) => r.contact?.zip).length;
  const p1 = kept.filter((r) => r.small.some((t) => t.includes("(A)"))).length;
  const both = kept.filter((r) => r.years.size >= 2).length;
  const total = (y: number) =>
    [...(totals.get(y) as Map<string, number>).values()].reduce(
      (s, n) => s + n,
      0,
    );
  const today = new Date();
  const dd = String(today.getDate()).padStart(2, "0");
  const mm = String(today.getMonth() + 1).padStart(2, "0");

  const lines = [
    "# Maires parrains de petits candidats — rapport de build\n",
    `- Parrainages chargés : ${endorsements.length} (2022: ${total(2022)}, 2017: ${total(2017)})`,
    `- Maires parrains d'un petit candidat (2017 et/ou 2022) : ${targets.length}`,
    `- **Toujours maires en 2026 : ${kept.length}** (P1 signal fort : ${p1} ; les deux années : ${both})`,
    `  - avec email mairie : ${nEmail} (${Math.floor((100 * nEmail) / Math.max(kept.length, 1))} %) ; avec adresse postale : ${nAddr}`,
    `- Plus maires en 2026 : ${former.length} ; à vérifier à la main : ` +
      `${unmatched.length}`,
    `\n## Matching RNE (${dd}/${mm}/${today.getFullYear()})\n`,
    ...[...stats.entries()]
      .sort((a, b) => cmp(a[0], b[0]))
      .map(([k, v]) => `- ${k} : ${v}`),
    `- fusions de graphies (même INSEE, deux écritures) : ${mergedSpellings}`,
    `- contrôle : ${kept.length + mergedSpellings} + ${former.length} + ` +
      `${unmatched.length} = ${targets.length} cibles`,
    "\n## Candidats et classification (ajustable dans outils/build.ts)\n",
  ];
  for (const y of [2022, 2017]) {
    lines.push(
      `\n### ${y}\n`,
      "| Candidat | Parrainages | Classe |",
      "|---|---|---|",
    );
    const entries = [...(totals.get(y) as Map<string, number>).entries()];
    // stable sort, like Python: at equal count, appearance order
    entries.sort((x, z) => z[1] - x[1]);
    for (const [cand, n] of entries) {
      const cl = TIER_A[y].has(cand)
        ? "A (signal fort)"
        : TIER_B[y].has(cand)
          ? "B (signal réel)"
          : "— (non compté)";
      lines.push(`| ${cand} | ${n} | ${cl} |`);
    }
  }
  lines.push(
    "\n## Base complète (04_base_complete.csv)\n",
    `- ${baseRows.length} maires (tous les maires de France)`,
    ...rankOrder.map((k) => `  - ${RANKS[k]} : ${rankCounts.get(k)}`),
  );
  writeFileSync(join(OUT, "rapport.md"), lines.join("\n") + "\n", "utf8");

  console.log(
    `targets=${targets.length} still_mayors=${kept.length} ` +
      `(P1=${p1}, both_years=${both}, email=${nEmail}) ` +
      `former=${former.length} unmatched=${unmatched.length}`,
  );
  for (const [k, v] of [...stats.entries()].sort((a, b) => cmp(a[0], b[0]))) {
    console.log(`  ${k}: ${v}`);
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) main();
