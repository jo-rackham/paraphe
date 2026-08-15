// Crosses the 2017/2022 presidential endorsements (Conseil constitutionnel),
// the national register of elected officials (RNE, mayors in office) and the
// government services directory (town hall contacts) to produce the list of
// mayors who endorsed "small" candidates and are still in office, with their
// contact details.
//
// Outputs in out/ (file names stay French — the campaign team opens them):
//   01_maires_cibles_prioritaires.csv  mayors still in office (the target)
//   02_anciens_parrains.csv            endorsers not found as mayors in 2026
//   03_non_apparies.csv                rows to check by hand
//   04_base_complete.csv               every mayor, ordered by signal
//   rapport.md                         methodology + stats (French: team doc)
//
// Run with `node outils/build.ts` — no compilation, no dependency.

import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { parseRecords, parseRows, writeCsv } from "../noyau/csv.ts";
import { RANKS } from "../noyau/messages.ts";
import {
  closestMatch, collapse, communeLabel, norm, ratio, sexFromTitle,
  stripControls, titleCase,
} from "../noyau/texte.ts";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const RAW = join(ROOT, "data", "raw");
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
    "ASSELINEAU François", "KAZIB Anasse", "THOUY Hélène", "KOENIG Gaspard",
    "KUZMANOVIC Georges", "MIGUET Nicolas", "EGGER Clara", "CHICHE Arnaud",
    "MARTINEZ Antoine", "FORTANÉ Jean-Marc", "SMATI Rafik", "ROCCA Martin",
    "CAU Marie", "WAECHTER Antoine", "MEURICE Guillaume", "BÉKAERT Corinne",
  ]),
  2017: new Set([
    "JARDIN Alexandre", "MARCHANDISE Charlotte", "TEMARU Oscar",
    "TAUZIN Didier", "GORGES Jean-Pierre", "TROADEC Christian",
    "LARROUTUROU Pierre", "FAUDOT Bastien", "MIGUET Nicolas",
    "MUMBACH Paul", "WAECHTER Antoine", "TONIUTTI Emmanuel",
    "GUYOT Stéphane", "REGIS Olivier", "NIKONOFF Jacques",
  ]),
};

const TIER_B: Record<number, Set<string>> = {
  2022: new Set(["POUTOU Philippe", "ARTHAUD Nathalie", "LASSALLE Jean",
    "TAUBIRA Christiane"]),
  2017: new Set(["ARTHAUD Nathalie", "POUTOU Philippe", "CHEMINADE Jacques",
    "ASSELINEAU François", "LASSALLE Jean"]),
};

// Candidates whose campaign explicitly bore on democratic functioning
// (citizen primary, citizens' initiative referendum, subsidiarity, popular
// sovereignty, decentralisation). The tag qualifies a PUBLIC ACT — having
// endorsed that candidacy — not a conviction: we do not presume the
// official's sincerity, we record what they signed.
const DEMOCRATIC_THEME = [
  "MARCHANDISE Charlotte",   // LaPrimaire.org, citizen primary
  "JARDIN Alexandre",        // citizen movement, hands-on democracy
  "NIKONOFF Jacques",        // popular sovereignty, constituent assembly
  "FAUDOT Bastien",          // VIth Republic
  "EGGER Clara",             // citizens' initiative referendum
  "KOENIG Gaspard",          // subsidiarity, decentralisation
  "TROADEC Christian",       // decentralisation, local democracy
];

const YEARS = [2022, 2017];
const ENDORSEMENT_FILES: Record<number, string> = {
  2022: join(RAW, "parrainages2022.csv"),
  2017: join(RAW, "parrainages2017.csv"),
};

export interface Endorsement {
  year: number; civ: string; lastName: string; firstName: string; office: string;
  commune: string; dept: string; candidate: string;
}

interface Official {
  dept: string; insee: string; commune: string; lastName: string;
  firstName: string; sex: string;
}

interface Contact {
  cardName: string; email: string; phone: string; street: string; zip: string;
  city: string; website: string; contactForm: string; hours: string;
}

interface Person {
  civ: string; lastName: string; firstName: string; commune: string;
  dept: string; office: string; small: string[]; others: string[];
  years: Set<number>; score: number; status?: string; communeInsee?: string;
  // normalised forms of the aggregation key: recomputing them from a
  // concatenated key would be wrong, department and commune contain spaces
  deptN: string; communeN: string;
}

export interface Target extends Person { rne: Official; contact: Contact | undefined; conf: string }

// STRICT decoding: `readFileSync(…, "utf8")` replaces an invalid byte with
// U+FFFD without a word, and the corrupted commune name goes into the
// outputs — relabelling the match with a wrong explanation along the way.
// The sources already carry CP1252 leftovers: the raw byte is not a
// hypothesis.
export const readStrict = (path: string): string => {
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(readFileSync(path));
  } catch (e) {
    throw new Error(`${path} is not valid UTF-8: ${(e as Error).message}`);
  }
};

/** Possibly missing or malformed JSON -> explicit fallback value. */
function jsonOrDefault<T>(raw: string | undefined, fallback: T): T {
  if (!raw) return fallback;
  try {
    return JSON.parse(raw) as T;
  } catch {
    // the directory embeds JSON inside a CSV: a malformed card is skipped,
    // it must not stop the crossing of the 34,826 others
    return fallback;
  }
}

export function loadEndorsements(): Endorsement[] {
  const rows: Endorsement[] = [];
  for (const year of YEARS) {
    const lines = parseRows(readStrict(ENDORSEMENT_FILES[year]));
    checkHeader(`parrainages${year}.csv`, lines[0] ?? [], ENDORSEMENT_COLUMNS);
    for (const r of lines.slice(1)) {
      if (r.length < 8) continue;
      rows.push({
        year, civ: r[0].trim(), lastName: collapse(r[1]), firstName: collapse(r[2]),
        office: collapse(r[3]), commune: collapse(r[4]), dept: collapse(r[5]),
        candidate: collapse(r[6]),
      });
    }
  }
  checkCivilities(rows);
  // ~14,000 endorsements per year, all offices taken together
  if (rows.length < 20000) {
    throw new Error(
      `only ${rows.length} endorsement(s) read across ${YEARS.length} years, `
      + "while ~27,700 are expected. Truncated source or changed format.");
  }
  return rows;
}

/**
 * The sex code is THE discriminant that decides Christian/Christine, and
 * `compareIdentity` silently disables it when the civility leaves the known
 * domain. The two files already spell it differently ("M" in 2017, "M." in
 * 2022): a "Mr" in 2027 would be enough to mail "you presented X" to the
 * namesake of the other sex. We refuse to proceed rather than disarm.
 */
export function checkCivilities(rows: { civ: string }[]): void {
  const unknown = new Map<string, number>();
  for (const p of rows) {
    if (sexFromTitle(p.civ) === null) {
      unknown.set(p.civ, (unknown.get(p.civ) ?? 0) + 1);
    }
  }
  if (!unknown.size) return;
  const list = [...unknown].map(([v, n]) => `${JSON.stringify(v)} (${n})`);
  throw new Error(
    "civility outside the domain in the endorsement files: "
    + `${list.join(", ")}. The sex code is the discriminant that tells two `
    + "namesakes apart; accepting it would silently disable it. Add the form "
    + "to sexFromTitle() after checking.");
}

function loadRne(): {
  byCommune: Map<string, Map<string, Official>>;
  byPerson: Map<string, Official[]>;
} {
  const byCommune = new Map<string, Map<string, Official>>();
  const byPerson = new Map<string, Official[]>();
  const lines = parseRows(readStrict(join(RAW, "rne_maires.csv")));
  checkHeader("rne_maires.csv", lines[0] ?? [], RNE_COLUMNS);
  for (const r of lines.slice(1)) {
    // Martinique, Guyane, Polynésie, Nouvelle-Calédonie and SPM have an
    // EMPTY department label: their name is in the "special-status
    // collectivity" column
    const dept = collapse(r[1]) || collapse(r[3]);
    const official: Official = {
      dept, insee: r[4], commune: r[5], lastName: collapse(r[6]),
      firstName: collapse(r[7]), sex: collapse(r[8]).toUpperCase(),
    };
    const d = norm(dept);
    let communes = byCommune.get(d);
    if (!communes) { communes = new Map(); byCommune.set(d, communes); }
    communes.set(norm(official.commune), official);
    const people = byPerson.get(d);
    if (people) people.push(official);
    else byPerson.set(d, [official]);
  }
  // A source that yields nothing is a source read wrong: without this
  // floor, the crossing carries on and writes 34,826 rows of garbage.
  if (byPerson.size < 90) {
    throw new Error(
      `rne_maires.csv yielded only ${byPerson.size} department(s), while ~104 `
      + "are expected. Truncated source or changed format.");
  }
  return { byCommune, byPerson };
}

interface OpeningRange {
  nom_jour_debut?: string; nom_jour_fin?: string;
  [k: string]: string | undefined;
}

/** DILA plage_ouverture JSON -> 'Lun-Ven 09:00-12:00/14:00-17:00 ; Sam …'. */
export function compactHours(raw: string): string {
  const ranges = jsonOrDefault<OpeningRange[]>(raw, []);
  const parts: string[] = [];
  for (const p of ranges) {
    const d1 = p.nom_jour_debut ?? "";
    const d2 = p.nom_jour_fin ?? "";
    const days = !d2 || d1 === d2 ? d1.slice(0, 3) : `${d1.slice(0, 3)}-${d2.slice(0, 3)}`;
    const hours: string[] = [];
    for (const i of [1, 2]) {
      const from = (p[`valeur_heure_debut_${i}`] ?? "").slice(0, 5);
      const to = (p[`valeur_heure_fin_${i}`] ?? "").slice(0, 5);
      if (from && to) hours.push(`${from}-${to}`);
    }
    if (days && hours.length) parts.push(`${days} ${hours.join("/")}`);
  }
  return parts.join(" ; ");
}

interface Value { valeur?: string }
interface Address {
  type_adresse?: string; complement1?: string; numero_voie?: string;
  code_postal?: string; nom_commune?: string;
}

// The RNE and the endorsement files are read BY POSITION: r[4] is the
// commune, r[8] the sex code. That is the only way — the RNE header
// carries no stable machine name — but it means a column inserted upstream
// shifts everything by one, silently. Both datasets have already gained
// columns once (the special-status collectivity pair). Checked at the
// index the code actually reads, not merely present.
export const RNE_COLUMNS: [number, string[]][] = [
  [1, ["Libellé du département"]],
  [3, ["Libellé de la collectivité à statut particulier"]],
  [4, ["Code de la commune"]],
  [5, ["Libellé de la commune"]],
  [6, ["Nom de l'élu"]],
  [7, ["Prénom de l'élu"]],
  [8, ["Code sexe"]],
];

// The two years do not spell their last column the same way ("Candidat"
// in 2022, "Candidat-e parrainé-e" in 2017): several spellings are
// accepted, an unknown one is not.
export const ENDORSEMENT_COLUMNS: [number, string[]][] = [
  [0, ["Civilité"]], [1, ["Nom"]], [2, ["Prénom"]], [3, ["Mandat"]],
  [4, ["Circonscription"]], [5, ["Département"]],
  [6, ["Candidat", "Candidat-e parrainé-e"]],
];

export function checkHeader(file: string, header: string[], expected: [number, string[]][]): void {
  const wrong = expected
    .filter(([i, names]) => !names.some((n) => norm(header[i] ?? "") === norm(n)))
    .map(([i, names]) => `${i}: attendu ${names.map((n) => JSON.stringify(n)).join(" ou ")}, `
      + `trouvé ${JSON.stringify(header[i] ?? "")}`);
  if (wrong.length) {
    throw new Error(
      `${file}: the columns have moved — ${wrong.join(" ; ")}. The file is `
      + "read by position: one column inserted upstream shifts the identity "
      + "of every mayor.");
  }
}

const DIRECTORY_COLUMNS = ["pivot", "code_insee_commune", "nom",
  "adresse_courriel", "telephone", "site_internet", "adresse",
  "formulaire_contact", "plage_ouverture"];

function loadDirectory(): Map<string, Contact> {
  const contacts = new Map<string, Contact>();
  const raw = readStrict(join(RAW, "annuaire_mairies.csv"));
  // A column renamed upstream would drop EVERY card without an error: the
  // only trace would be "email=0" at the end of a console line, and Pages
  // would publish a list without a single contact.
  const header = new Set(parseRows(raw)[0] ?? []);
  const missing = DIRECTORY_COLUMNS.filter((c) => !header.has(c));
  if (missing.length) {
    throw new Error(
      `columns missing from annuaire_mairies.csv: ${missing.join(", ")} — `
      + "the directory format changed, the crossing would produce cards "
      + "without contact details.");
  }
  // one card per commune: the main town hall first — "Mairie déléguée -
  // Pruillé" is shorter than "Mairie - Longuenée-en-Anjou" but it is not
  // the right one
  const rankCard = (name: string): [number, number] => {
    const n = name.toLowerCase();
    return [n.includes("délégu") || n.includes("annexe") ? 1 : 0, name.length];
  };
  const beforeOrEqual = (a: [number, number], b: [number, number]): boolean =>
    a[0] < b[0] || (a[0] === b[0] && a[1] <= b[1]);

  for (const r of parseRecords(raw)) {
    const pivots = jsonOrDefault<{ type_service_local?: string }[]>(r.pivot, null as never);
    if (pivots === null) continue;
    const types = new Set(pivots.map((p) => p.type_service_local));
    if (!types.has("mairie") && !types.has("mairie_com")) continue; // annexes, mobile town halls
    const insee = (r.code_insee_commune ?? "").trim();
    if (!insee) continue;
    const existing = contacts.get(insee);
    if (existing && beforeOrEqual(rankCard(existing.cardName), rankCard(r.nom))) continue;

    const phones = jsonOrDefault<Value[]>(r.telephone, []);
    const sites = jsonOrDefault<Value[]>(r.site_internet, []);
    const addresses = jsonOrDefault<Address[]>(r.adresse, []);
    const addr = addresses.find((a) => a.type_adresse === "Adresse") ?? addresses[0] ?? {};
    contacts.set(insee, {
      cardName: r.nom,
      email: (r.adresse_courriel ?? "").trim(),
      phone: phones[0]?.valeur ?? "",
      street: [addr.complement1 ?? "", addr.numero_voie ?? ""].filter(Boolean).join(" ").trim(),
      zip: addr.code_postal ?? "",
      city: addr.nom_commune ?? "",
      website: sites[0]?.valeur ?? "",
      contactForm: (r.formulaire_contact ?? "").trim(),
      hours: compactHours(r.plage_ouverture ?? ""),
    });
  }
  // yield floor: ~34,800 communes have a card. A silent collapse deserves
  // a halt.
  if (contacts.size < 30000) {
    throw new Error(
      `annuaire_mairies.csv produced only ${contacts.size} contact card(s), `
      + "while ~34,800 are expected. Truncated source or changed format — "
      + "without contacts the produced list is unusable.");
  }
  return contacts;
}

/**
 * 'ok' | 'unsure' | 'different'.
 *
 * Compares the WHOLE first-name string: reduced to the first token,
 * Marie-Cécile and Marie-Ève would become the same person. Truncation is
 * accepted (Jean-Louis ⊇ Louis, Don Philippe ⊇ Philippe), contradiction is
 * not.
 */
export function compareFirstNames(a: string, b: string): string {
  const ta = norm(a).split(" ").filter(Boolean);
  const tb = norm(b).split(" ").filter(Boolean);
  if (!ta.length || !tb.length) return "unsure";
  const sa = new Set(ta);
  const sb = new Set(tb);
  const subset = (x: Set<string>, y: Set<string>) => [...x].every((v) => y.has(v));
  if (subset(sa, sb) || subset(sb, sa)) return "ok";
  // only common positions are compared: "Jean-Louis" and "Louis" do not
  // have the same number of tokens
  let worst = Infinity;
  for (let i = 0; i < Math.min(ta.length, tb.length); i++) {
    worst = Math.min(worst, ratio(ta[i], tb[i]));
  }
  if (worst >= 0.80) return "ok";        // Henry/Henri, Magali/Magalli
  if (worst >= 0.60) return "unsure";    // Jacky/Jacquy: plausible, to check
  return "different";
}

/**
 * Is the endorser the current mayor? 'ok' | 'unsure' | 'different'.
 *
 * The RNE sex code decides spousal or filial successions that spelling
 * alone conflates (Christian → Christine: ratio 0.89).
 */
/**
 * The identity of an endorser, as the aggregation groups them. The first
 * name is PART of it: without it two namesakes of the same commune — a
 * predecessor and their successor, two spouses — merge into one person,
 * and the current mayor inherits the other's endorsements — five false
 * « merci pour votre parrainage » on the real data.
 *
 * Grouped on the FIRST TOKEN, because one person is written two ways
 * across the two years — « Jean-Claude » in 2017 and « Jean-Claude
 * Raymond » in 2022, both real, both the mayor of Villautou. Keying on
 * the whole first name splits them and loses a real endorsement.
 *
 * Which leaves Jean-Louis and Jean-Marc DUPONT sharing a token: that is
 * what `discriminator` is for — the caller passes it when the full names
 * CONTRADICT, and the two records stay apart. Truncation groups,
 * contradiction separates, exactly as compareFirstNames decides it
 * everywhere else.
 */
export function personKey(
  p: { dept: string; commune: string; lastName: string; firstName: string },
  discriminator = "",
): string {
  return JSON.stringify([norm(p.dept), norm(p.commune), norm(p.lastName),
    norm(p.firstName).split(" ").filter(Boolean)[0] ?? "", discriminator]);
}

/** How well a match is established; 0 is the best. */
export function confidenceRank(conf: string): number {
  return ["exact", "commune approchée", "retrouvé par nom"].indexOf(conf) + 1
    || Number.MAX_SAFE_INTEGER;
}

/**
 * One row per INSEE. A commune's name spelt differently between 2017 and
 * 2022 (compound, accents) yields two rows for one person — merge. At
 * equal INSEE with DIFFERENT first names it is a matching bug, and it
 * throws: a duplicated INSEE in the output is a mayor thanked for a
 * stranger's endorsement.
 */
export function dedupeByInsee(
  rows: Target[],
): { kept: Target[]; mergedSpellings: number } {
  const byInsee = new Map<string, Target>();
  let mergedSpellings = 0;
  for (const r of rows) {
    const k = r.rne.insee;
    const target = byInsee.get(k);
    if (!target) { byInsee.set(k, r); continue; }
    if (compareFirstNames(target.firstName, r.firstName) === "different") {
      throw new Error(
        `two different people matched onto INSEE ${k}: `
        + `${target.firstName} ${target.lastName} / ${r.firstName} ${r.lastName}`);
    }
    mergedSpellings++;
    // The survivor keeps the BEST-established match, not the first one
    // read: two records of one person can be matched differently — the
    // commune spelt as signed in one year and found by fuzzy fallback in
    // the other — and taking whichever came first made the commune shown
    // and the confidence label depend on the order the source files are
    // listed in. What is shown is now the same whatever that order.
    if (confidenceRank(r.conf) < confidenceRank(target.conf)) {
      target.conf = r.conf;
      target.commune = r.commune;
      target.communeN = r.communeN;
    }
    target.small = [...new Set([...target.small, ...r.small])].sort(cmp);
    target.others = [...new Set([...target.others, ...r.others])].sort(cmp);
    for (const y of r.years) target.years.add(y);
    target.score = 2 * target.small.filter((t) => t.includes("(A)")).length
      + target.small.filter((t) => t.includes("(B)")).length
      + (target.years.size >= 2 ? 1 : 0);
  }
  const kept = [...byInsee.values()].sort((a, b) =>
    b.score - a.score || cmp(a.dept, b.dept) || cmp(a.commune, b.commune));
  return { kept, mergedSpellings };
}

/**
 * The key this endorsement is filed under, among those already seen.
 *
 * A shared first token is not a shared identity: Jean-Louis and Jean-Marc
 * DUPONT of one commune would be one person, and whichever of them is
 * still mayor would be thanked for the other's endorsement. When the full
 * names CONTRADICT, the record gets a key of its own — while « Jean-Claude »
 * and « Jean-Claude Raymond », one man written two ways across the two
 * years, keep sharing theirs.
 */
export function keyAmong(
  p: { dept: string; commune: string; lastName: string; firstName: string },
  seen: Map<string, { firstName: string }>,
): string {
  const key = personKey(p);
  const sharing = seen.get(key);
  if (sharing && compareFirstNames(sharing.firstName, p.firstName) === "different") {
    return personKey(p, norm(p.firstName));
  }
  return key;
}

export function compareIdentity(rec: { lastName: string; firstName: string; civ: string },
  row: Official): string {
  const lastP = norm(rec.lastName);
  const lastR = norm(row.lastName);
  if (!(lastP === lastR || lastR.split(" ").includes(lastP)
        || lastP.split(" ").includes(lastR))) {
    return "different";
  }
  const verdict = compareFirstNames(rec.firstName, row.firstName);
  const sexP = sexFromTitle(rec.civ);
  if (sexP && row.sex && sexP !== row.sex) {
    // last AND first names strictly identical: the Conseil constitutionnel
    // file's civility is what is wrong ("M. Sophie PRADEL"), not two
    // different people. Assert nothing: the doubt goes to 03 "to check",
    // never to 02 "successor in place".
    if (lastP === lastR && norm(rec.firstName) === norm(row.firstName)) return "unsure";
    return verdict === "ok" ? "unsure" : "different";
  }
  return verdict;
}

/**
 * Department code of an INSEE code. Overseas takes three digits (97120 is
 * Martinique, not department 97), mainland two — 2A/2B included, Corsican
 * INSEE codes carrying the letter.
 */
function departmentOfInsee(insee: string): string {
  return /^9[78]/.test(insee) ? insee.slice(0, 3) : insee.slice(0, 2);
}

/** 'THOUY Hélène' -> 'Hélène Thouy' (all-uppercase tokens = last name). */
function candidateProseName(candidate: string): string {
  const lastNames: string[] = [];
  const firstNames: string[] = [];
  for (const tok of candidate.split(" ").filter(Boolean)) {
    (tok === tok.toUpperCase() && tok !== tok.toLowerCase() ? lastNames : firstNames).push(tok);
  }
  return [...firstNames, ...lastNames.map(titleCase)].join(" ");
}

/** Code-point string comparison, like Python. */
const cmp = (a: string, b: string): number => (a < b ? -1 : a > b ? 1 : 0);

/** Python's `max(iterable, key=…)`: returns the FIRST maximal element. */
function maxBy<T>(items: T[], key: (x: T) => string): T {
  let best = items[0];
  let bestKey = key(items[0]);
  for (const x of items.slice(1)) {
    const k = key(x);
    if (k > bestKey) { best = x; bestKey = k; }
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
  const totals = new Map<number, Map<string, number>>(YEARS.map((y) => [y, new Map()]));
  for (const p of endorsements) {
    const t = totals.get(p.year) as Map<string, number>;
    t.set(p.candidate, (t.get(p.candidate) ?? 0) + 1);
  }
  // the configuration keys do exist in the data
  for (const y of [2017, 2022]) {
    for (const c of [...TIER_A[y], ...TIER_B[y]]) {
      if (!(totals.get(y) as Map<string, number>).has(c)) {
        throw new Error(`unknown candidate in the ${y} configuration: ${JSON.stringify(c)}`);
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
        civ: p.civ, lastName: p.lastName, firstName: p.firstName,
        commune: p.commune, dept: p.dept, office: p.office, small: [],
        others: [], years: new Set(), score: 0, deptN, communeN,
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
      // high cutoff: at 0.87, "Goncourt" catches "Voncourt" and
      // "Esnes-en-Argonne" catches "Gesnes-en-Argonne"
      const close = closestMatch(communeN, deptCommunes.keys(), 0.93);
      if (close !== null) { row = deptCommunes.get(close); approx = true; }
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
      // A commune absent from the RNE is not thereby a merged commune: it
      // may simply have no mayor row this month — 912 communes have a town
      // hall in the directory and no RNE row. Taking the fallback there
      // hands the endorsement to a departmental namesake 130 km away, and
      // the letter thanks them for it. If the signed commune still owns an
      // INSEE code of its own, it is neither a rename nor a merger.
      // successor in place: the commune IS in the RNE, under someone else
      const counterProof = verdict === "different" && !approx;
      const hits = counterProof ? [] : (byPerson.get(deptN) ?? [])
        .filter((r) => compareIdentity(rec, r) === "ok");
      // A commune absent from the RNE is not thereby a merged commune: it
      // may simply have no mayor row this month — 912 communes have a town
      // hall in the directory and none in the RNE. The signed commune's own
      // INSEE settles it. Renamed or merged, it points at the commune the
      // namesake leads (Vertus and Blancs-Coteaux are both 51612); a
      // DIFFERENT code means two distinct communes, and the fallback would
      // thank someone 130 km away for an endorsement that is not theirs.
      const signedInsee = row === undefined
        ? (communesByLabel.get(communeN) ?? []).find(
          (insee) => departmentOfInsee(insee) === deptCode.get(deptN))
        : undefined;
      const otherCommune = hits.length === 1 && signedInsee !== undefined
        && signedInsee !== hits[0].insee;
      if (hits.length === 1 && !otherCommune) {
        rneRow = hits[0];
        conf = "retrouvé par nom (commune renommée/fusionnée)";
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
        rec.status = "à vérifier : "
          + (otherCommune ? "commune sans maire au RNE, homonyme dans une autre commune"
            : row === undefined ? "commune introuvable"
              : "identité proche mais non certaine")
          + (row === undefined ? "" : ` (RNE : ${row.firstName} ${row.lastName}, ${row.commune})`);
        unmatched.push(rec);
        continue;
      }
    }
    stillMayor.push({ ...rec, rne: rneRow, contact: contacts.get(rneRow.insee), conf });
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
    [...rec.small, ...rec.others].some((t) => DEMOCRATIC_THEME.some((c) => t.includes(c)))
      ? "oui" : "";
  const common = (rec: Person): Record<string, string> => ({
    priority: priority(rec),
    score: String(rec.score),
    democratic_theme_endorsement: democraticTheme(rec),
    title: rec.civ, first_name: rec.firstName, last_name: rec.lastName,
    commune: rec.commune, department: rec.dept,
    small_candidate_endorsements: [...rec.small].sort(cmp).join(" | "),
    other_endorsements: [...rec.others].sort(cmp).join(" | "),
  });

  const cols1 = ["priority", "score", "democratic_theme_endorsement",
    "title", "first_name", "last_name", "commune", "department", "insee_code",
    "small_candidate_endorsements", "other_endorsements", "recent_candidate",
    "recent_year", "email", "phone", "town_hall_hours", "postal_address",
    "postal_code", "city", "website", "contact_form", "commune_2026",
    "matching_confidence"];

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
      communeLabel(rne.commune, c?.city ?? ""), r.commune);
    const official: Person = {
      ...r, lastName: rne.lastName, firstName: rne.firstName, dept: rne.dept,
      civ, commune,
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
      recent_candidate: candidateProseName(tagCandidate(recent)),
      recent_year: tagYear(recent),
      commune_2026: rne.commune,
      matching_confidence: r.conf,
    };
  });
  writeFileSync(join(OUT, "01_maires_cibles_prioritaires.csv"),
    writeCsv(cols1, rows1), "utf8");

  const notInScope = new Set(["insee_code", "email", "phone", "town_hall_hours",
    "postal_address", "postal_code", "city", "website", "contact_form",
    "commune_2026", "matching_confidence", "recent_candidate", "recent_year"]);
  const cols2 = [...cols1.filter((c) => !notInScope.has(c)), "status"];
  for (const [name, rows] of [["02_anciens_parrains.csv", former],
    ["03_non_apparies.csv", unmatched]] as [string, Person[]][]) {
    const sorted = [...rows].sort((a, b) => b.score - a.score || cmp(a.dept, b.dept));
    writeFileSync(join(OUT, name),
      writeCsv(cols2, sorted.map((r) => ({ ...common(r), status: r.status ?? "" }))),
      "utf8");
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

  const cols4 = ["rank", "rank_label", "score", "priority",
    "democratic_theme_endorsement", "title", "first_name", "last_name",
    "commune", "department", "insee_code", "endorsement_history",
    "predecessor", "predecessor_mayor", "recent_candidate", "recent_year",
    "email", "phone", "town_hall_hours", "postal_address", "postal_code",
    "city", "website", "contact_form"];
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
        predecessorMayor = past.office === "Maire" && past.years.has(2022)
          && !past.years.has(2017) ? "oui" : "";
      } else {
        rankKey = "no_signal";
        hist = "";
      }
      rankCounts.set(rankKey, (rankCounts.get(rankKey) ?? 0) + 1);
      const recent = target ? maxBy(target.small, tagYear) : null;
      baseRows.push({
        rank: rankKey, rank_label: RANKS[rankKey],
        score: String(target ? target.score : 0),
        priority: target ? priority(target) : "",
        democratic_theme_endorsement: target ? democraticTheme(target) : "",
        title: row.sex === "F" ? "Mme" : "M.",
        first_name: row.firstName, last_name: row.lastName,
        commune: communeLabel(row.commune, c?.city ?? ""),
        department: row.dept, insee_code: insee,
        endorsement_history: hist,
        predecessor, predecessor_mayor: predecessorMayor,
        recent_candidate: recent ? candidateProseName(tagCandidate(recent)) : "",
        recent_year: recent ? tagYear(recent) : "",
        email: c?.email ?? "", phone: c?.phone ?? "",
        town_hall_hours: c?.hours ?? "",
        postal_address: stripControls(c?.street ?? ""),
        postal_code: c?.zip ?? "", city: stripControls(c?.city ?? ""),
        website: c?.website ?? "", contact_form: c?.contactForm ?? "",
      });
    }
  }
  baseRows.sort((a, b) =>
    rankOrder.indexOf(a.rank) - rankOrder.indexOf(b.rank)
    || Number(b.score) - Number(a.score)
    || cmp(a.department, b.department) || cmp(a.commune, b.commune));
  writeFileSync(join(OUT, "04_base_complete.csv"), writeCsv(cols4, baseRows), "utf8");

  // --- report (French: it is a document for the campaign team) -------------
  const nEmail = kept.filter((r) => r.contact?.email).length;
  const nAddr = kept.filter((r) => r.contact?.zip).length;
  const p1 = kept.filter((r) => r.small.some((t) => t.includes("(A)"))).length;
  const both = kept.filter((r) => r.years.size >= 2).length;
  const total = (y: number) =>
    [...(totals.get(y) as Map<string, number>).values()].reduce((s, n) => s + n, 0);
  const today = new Date();
  const dd = String(today.getDate()).padStart(2, "0");
  const mm = String(today.getMonth() + 1).padStart(2, "0");

  const lines = [
    "# Maires parrains de petits candidats — rapport de build\n",
    `- Parrainages chargés : ${endorsements.length} (2022: ${total(2022)}, 2017: ${total(2017)})`,
    `- Maires parrains d'un petit candidat (2017 et/ou 2022) : ${targets.length}`,
    `- **Toujours maires en 2026 : ${kept.length}** (P1 signal fort : ${p1} ; les deux années : ${both})`,
    `  - avec email mairie : ${nEmail} (${Math.floor(100 * nEmail / Math.max(kept.length, 1))} %) ; avec adresse postale : ${nAddr}`,
    `- Plus maires en 2026 : ${former.length} ; à vérifier à la main : `
    + `${unmatched.length}`,
    `\n## Matching RNE (${dd}/${mm}/${today.getFullYear()})\n`,
    ...[...stats.entries()].sort((a, b) => cmp(a[0], b[0])).map(([k, v]) => `- ${k} : ${v}`),
    `- fusions de graphies (même INSEE, deux écritures) : ${mergedSpellings}`,
    `- contrôle : ${kept.length + mergedSpellings} + ${former.length} + `
    + `${unmatched.length} = ${targets.length} cibles`,
    "\n## Candidats et classification (ajustable dans outils/build.ts)\n",
  ];
  for (const y of [2022, 2017]) {
    lines.push(`\n### ${y}\n`, "| Candidat | Parrainages | Classe |", "|---|---|---|");
    const entries = [...(totals.get(y) as Map<string, number>).entries()];
    // stable sort, like Python: at equal count, appearance order
    entries.sort((x, z) => z[1] - x[1]);
    for (const [cand, n] of entries) {
      const cl = TIER_A[y].has(cand) ? "A (signal fort)"
        : TIER_B[y].has(cand) ? "B (signal réel)" : "— (non compté)";
      lines.push(`| ${cand} | ${n} | ${cl} |`);
    }
  }
  lines.push("\n## Base complète (04_base_complete.csv)\n",
    `- ${baseRows.length} maires (tous les maires de France)`,
    ...rankOrder.map((k) => `  - ${RANKS[k]} : ${rankCounts.get(k)}`));
  writeFileSync(join(OUT, "rapport.md"), lines.join("\n") + "\n", "utf8");

  console.log(`targets=${targets.length} still_mayors=${kept.length} `
    + `(P1=${p1}, both_years=${both}, email=${nEmail}) `
    + `former=${former.length} unmatched=${unmatched.length}`);
  for (const [k, v] of [...stats.entries()].sort((a, b) => cmp(a[0], b[0]))) {
    console.log(`  ${k}: ${v}`);
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) main();
