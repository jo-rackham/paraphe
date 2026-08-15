// Synthetic dataset, for CI and the tests.
//
// The real lists are not versioned (they regenerate with `task all` from
// the open sources): without this file, neither the image nor the tests
// could start. No real elected official here — communes and names are
// invented.

import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

import { writeCsv } from "../noyau/csv.ts";
import { RANKS } from "../noyau/messages.ts";

const COLS_04 = [
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

const COLS_01 = [
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

const SCORES: Record<string, number> = {
  has_endorsed: 3,
  commune_has_endorsed: 0,
  no_signal: 0,
};

const zeros = (n: number, width: number) => String(n).padStart(width, "0");

// The shapes the real base carries and a two-department fixture does not:
// overseas (whose INSEE is three digits and whose department label is empty
// at the RNE), non-ASCII communes, a commune starting with a vowel — the
// elision — and one carrying an article.
const DEPARTMENTS = [
  "Aveyron",
  "Cantal",
  "Moselle",
  "Nord",
  "Bas-Rhin",
  "Martinique",
  "Polynésie française",
  "Vosges",
  "Somme",
  "Pas-De-Calais",
];

const COMMUNE_SHAPES = [
  "Sainte-Fiction",
  "Havange-Fictive",
  "Ambléon-Fictif",
  "Le Fictif",
  "La Fiction",
  "Les Fictions",
  "L'Haÿ-Fictive",
  "Œting-Fictif",
  "Fiction-sur-Mer",
  "Hiva-Fictive",
];

function row(i: number, rank: string): Record<string, string> {
  const woman = i % 3 === 0;
  const hasHistory = rank === "has_endorsed" || rank === "commune_has_endorsed";
  const department = DEPARTMENTS[i % DEPARTMENTS.length];
  const overseas =
    department === "Martinique" || department === "Polynésie française";
  return {
    rank,
    rank_label: RANKS[rank],
    // A single distinct score per rank cannot exercise an ordering: the
    // real file spreads 1 to 5, and reversing the sort changed nothing in
    // any journey.
    score: String(SCORES[rank] + (rank === "has_endorsed" ? i % 5 : 0)),
    priority: rank === "has_endorsed" ? "P1" : "",
    democratic_theme_endorsement:
      rank === "has_endorsed" && i % 5 === 0 ? "oui" : "",
    title: woman ? "Mme" : "M.",
    first_name: woman ? `Camille${i}` : `Dominique${i}`,
    last_name: `MARTIN${i}`,
    commune: `${COMMUNE_SHAPES[i % COMMUNE_SHAPES.length]}-${i}`,
    department,
    // Five characters, like a real one: "97" plus THREE digits, so the
    // overseas rows are numbered among themselves — numbering them by the
    // global index overflowed past 999 and produced duplicates, which the
    // API's own import guard caught.
    // 5 characters, and BOUNDED: zeros() pads without truncating, so past
    // a few thousand rows the overseas code grew to six and the mainland
    // one collided with the 97xxx range. Only the API's import guard would
    // have caught the second.
    insee_code: overseas
      ? `97${zeros(
          (Math.floor(i / DEPARTMENTS.length) * 2 +
            (department === "Martinique" ? 0 : 1)) %
            1000,
          3,
        )}`
      : `9${zeros(i % 7000, 4)}`,
    endorsement_history: hasHistory
      ? "2017: EXEMPLE Alex (A) | 2022: EXEMPLE Alex (A)"
      : "",
    predecessor: rank === "commune_has_endorsed" ? `Claude ANCIEN${i}` : "",
    predecessor_mayor: rank === "commune_has_endorsed" ? "oui" : "",
    recent_candidate: rank === "has_endorsed" ? "Alex Exemple" : "",
    recent_year: rank === "has_endorsed" ? "2022" : "",
    // The real directory leaves 370 emails empty, concatenates 318 of them,
    // and leaves 412 opening hours and 76 addresses blank. A fixture where
    // every field is filled never crosses the fallbacks the card relies on.
    email:
      i % 23 === 0
        ? ""
        : i % 17 === 0
          ? `mairie${i}@exemple-fictif.fr;secretariat${i}@exemple-fictif.fr`
          : `mairie${i}@exemple-fictif.fr`,
    phone: i % 19 === 0 ? "" : "01 23 45 67 89",
    town_hall_hours: i % 13 === 0 ? "" : "lundi 9h-12h",
    postal_address: i % 29 === 0 ? "" : `${i} place de la Fiction`,
    postal_code: `1${zeros(i, 4)}`,
    city: `Sainte-Fiction-${i}`,
    website: "",
    contact_form: "",
  };
}

export function main(dest: string): void {
  mkdirSync(dest, { recursive: true });
  const rows: Record<string, string>[] = [];
  let i = 0;
  // The volume is not decorative: the API refuses to import a CSV of fewer
  // than 1,000 rows (a truncated file would empty the database). A shorter
  // fixture builds an image that does not start, with a green CI.
  for (const [rank, count] of [
    ["has_endorsed", 260],
    ["commune_has_endorsed", 180],
    ["no_signal", 620],
  ] as [string, number][]) {
    for (let n = 0; n < count; n++) rows.push(row(++i, rank));
  }
  writeFileSync(
    join(dest, "04_base_complete.csv"),
    writeCsv(COLS_04, rows),
    "utf8",
  );

  // file 01 (mass mailing) only contains the endorsers
  const endorsers = rows
    .filter((m) => m.rank === "has_endorsed")
    .map((m) => ({
      ...Object.fromEntries(COLS_01.map((c) => [c, m[c] ?? ""])),
      small_candidate_endorsements: m.endorsement_history,
      commune_2026: m.commune,
      matching_confidence: "exact",
    }));
  writeFileSync(
    join(dest, "01_maires_cibles_prioritaires.csv"),
    writeCsv(COLS_01, endorsers),
    "utf8",
  );

  console.log(`${rows.length} synthetic rows written to ${dest}`);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  main(process.argv[2] ?? "out/");
}
