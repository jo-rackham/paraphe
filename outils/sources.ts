// Reading and validating the three open sources: the Conseil
// constitutionnel endorsement files, the RNE (mayors in office) and the
// DILA directory of town halls. Everything read by position is checked at
// the index the code actually reads, and every loader carries a yield
// floor: a source that yields nothing is a source read wrong.

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { parseRecords, parseRows } from "../noyau/csv.ts";
import { collapse, norm, sexFromTitle } from "../noyau/texte.ts";
import { ROOT } from "./config.ts";

const RAW = join(ROOT, "data", "raw");

export const YEARS = [2022, 2017];
const ENDORSEMENT_FILES: Record<number, string> = {
  2022: join(RAW, "parrainages2022.csv"),
  2017: join(RAW, "parrainages2017.csv"),
};

export interface Endorsement {
  year: number;
  civ: string;
  lastName: string;
  firstName: string;
  office: string;
  commune: string;
  dept: string;
  candidate: string;
}

export interface Official {
  dept: string;
  insee: string;
  commune: string;
  lastName: string;
  firstName: string;
  sex: string;
}

export interface Contact {
  cardName: string;
  email: string;
  phone: string;
  street: string;
  zip: string;
  city: string;
  website: string;
  contactForm: string;
  hours: string;
}

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
        year,
        civ: r[0].trim(),
        lastName: collapse(r[1]),
        firstName: collapse(r[2]),
        office: collapse(r[3]),
        commune: collapse(r[4]),
        dept: collapse(r[5]),
        candidate: collapse(r[6]),
      });
    }
  }
  checkCivilities(rows);
  // ~14,000 endorsements per year, all offices taken together
  if (rows.length < 20000) {
    throw new Error(
      `only ${rows.length} endorsement(s) read across ${YEARS.length} years, ` +
        "while ~27,700 are expected. Truncated source or changed format.",
    );
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
    "civility outside the domain in the endorsement files: " +
      `${list.join(", ")}. The sex code is the discriminant that tells two ` +
      "namesakes apart; accepting it would silently disable it. Add the form " +
      "to sexFromTitle() after checking.",
  );
}

export function loadRne(): {
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
      dept,
      insee: r[4],
      commune: r[5],
      lastName: collapse(r[6]),
      firstName: collapse(r[7]),
      sex: collapse(r[8]).toUpperCase(),
    };
    const d = norm(dept);
    let communes = byCommune.get(d);
    if (!communes) {
      communes = new Map();
      byCommune.set(d, communes);
    }
    communes.set(norm(official.commune), official);
    const people = byPerson.get(d);
    if (people) people.push(official);
    else byPerson.set(d, [official]);
  }
  // A source that yields nothing is a source read wrong: without this
  // floor, the crossing carries on and writes 34,826 rows of garbage.
  if (byPerson.size < 90) {
    throw new Error(
      `rne_maires.csv yielded only ${byPerson.size} department(s), while ~104 ` +
        "are expected. Truncated source or changed format.",
    );
  }
  return { byCommune, byPerson };
}

interface OpeningRange {
  nom_jour_debut?: string;
  nom_jour_fin?: string;
  [k: string]: string | undefined;
}

/** DILA plage_ouverture JSON -> 'Lun-Ven 09:00-12:00/14:00-17:00 ; Sam …'. */
export function compactHours(raw: string): string {
  const ranges = jsonOrDefault<OpeningRange[]>(raw, []);
  const parts: string[] = [];
  for (const p of ranges) {
    const d1 = p.nom_jour_debut ?? "";
    const d2 = p.nom_jour_fin ?? "";
    const days =
      !d2 || d1 === d2 ? d1.slice(0, 3) : `${d1.slice(0, 3)}-${d2.slice(0, 3)}`;
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

interface Value {
  valeur?: string;
}
interface Address {
  type_adresse?: string;
  complement1?: string;
  numero_voie?: string;
  code_postal?: string;
  nom_commune?: string;
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
  [0, ["Civilité"]],
  [1, ["Nom"]],
  [2, ["Prénom"]],
  [3, ["Mandat"]],
  [4, ["Circonscription"]],
  [5, ["Département"]],
  [6, ["Candidat", "Candidat-e parrainé-e"]],
];

export function checkHeader(
  file: string,
  header: string[],
  expected: [number, string[]][],
): void {
  const wrong = expected
    .filter(
      ([i, names]) => !names.some((n) => norm(header[i] ?? "") === norm(n)),
    )
    .map(
      ([i, names]) =>
        `${i}: attendu ${names.map((n) => JSON.stringify(n)).join(" ou ")}, ` +
        `trouvé ${JSON.stringify(header[i] ?? "")}`,
    );
  if (wrong.length) {
    throw new Error(
      `${file}: the columns have moved — ${wrong.join(" ; ")}. The file is ` +
        "read by position: one column inserted upstream shifts the identity " +
        "of every mayor.",
    );
  }
}

const DIRECTORY_COLUMNS = [
  "pivot",
  "code_insee_commune",
  "nom",
  "adresse_courriel",
  "telephone",
  "site_internet",
  "adresse",
  "formulaire_contact",
  "plage_ouverture",
];

export function loadDirectory(): Map<string, Contact> {
  const contacts = new Map<string, Contact>();
  const raw = readStrict(join(RAW, "annuaire_mairies.csv"));
  // A column renamed upstream would drop EVERY card without an error: the
  // only trace would be "email=0" at the end of a console line, and Pages
  // would publish a list without a single contact.
  const header = new Set(parseRows(raw)[0] ?? []);
  const missing = DIRECTORY_COLUMNS.filter((c) => !header.has(c));
  if (missing.length) {
    throw new Error(
      `columns missing from annuaire_mairies.csv: ${missing.join(", ")} — ` +
        "the directory format changed, the crossing would produce cards " +
        "without contact details.",
    );
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
    const pivots = jsonOrDefault<{ type_service_local?: string }[]>(
      r.pivot,
      null as never,
    );
    if (pivots === null) continue;
    const types = new Set(pivots.map((p) => p.type_service_local));
    if (!types.has("mairie") && !types.has("mairie_com")) continue; // annexes, mobile town halls
    const insee = (r.code_insee_commune ?? "").trim();
    if (!insee) continue;
    const existing = contacts.get(insee);
    if (existing && beforeOrEqual(rankCard(existing.cardName), rankCard(r.nom)))
      continue;

    const phones = jsonOrDefault<Value[]>(r.telephone, []);
    const sites = jsonOrDefault<Value[]>(r.site_internet, []);
    const addresses = jsonOrDefault<Address[]>(r.adresse, []);
    const addr =
      addresses.find((a) => a.type_adresse === "Adresse") ?? addresses[0] ?? {};
    contacts.set(insee, {
      cardName: r.nom,
      email: (r.adresse_courriel ?? "").trim(),
      phone: phones[0]?.valeur ?? "",
      street: [addr.complement1 ?? "", addr.numero_voie ?? ""]
        .filter(Boolean)
        .join(" ")
        .trim(),
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
      `annuaire_mairies.csv produced only ${contacts.size} contact card(s), ` +
        "while ~34,800 are expected. Truncated source or changed format — " +
        "without contacts the produced list is unusable.",
    );
  }
  return contacts;
}
