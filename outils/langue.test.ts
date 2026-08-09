// The boundary between the English code and the French interface.
//
// Every identifier, column, JSON key and enum value is English; every string
// a volunteer or a mayor reads is French. A whole-word rename crosses that
// boundary without a sound: it rewrites the inside of the strings too, and
// the result is a sentence in neither language, shown to a volunteer, that
// no test asserts on. One reached the screen this way — "Essayez un
// no_signal vivier ou un no_signal département."

import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

import { ROOT } from "./config.ts";

/** Vocabulary of the data model: legitimate in code, never inside a sentence. */
const ENGLISH = [
  "mayor", "mayors", "volunteer", "team", "teams", "account", "accounts",
  "campaign", "department", "status", "rank", "batch", "assignment",
  "has_endorsed", "commune_has_endorsed", "no_signal", "to_contact",
  "email_sent", "letter_sent", "to_call_back", "promised",
  "promised_elsewhere", "do_not_contact", "insee_code", "first_name",
  "last_name", "updated_at", "team_id", "password_hash",
];

const SQL = /\b(SELECT|INSERT|UPDATE|DELETE|FROM|WHERE|JOIN|VALUES|SET|CREATE|ALTER|GRANT)\b/;
// French either by its accents or by its most ordinary words
const FRENCH =
  /[àâçéèêëîïôùûüœ]|\b(le|la|les|un|une|des|du|de|vous|votre|pour|dans|avec|est|sont|ne|pas|ce|cette)\b/i;

/**
 * Sentences that name an English value ON PURPOSE, because the value itself
 * is what the reader must type or recognise.
 */
const DELIBERATE = [
  "Décision inconnue", // names the two accepted JSON values
  "colonne insee_code absente", // names the CSV column to look for
];

const STRING = /"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'|`([^`]*)`/g;
/** `${…}` is code inside a template literal, not part of the sentence. */
const INTERPOLATION = /\$\{[^}]*\}/g;

function sources(dir: string, extensions: string[]): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(join(ROOT, dir), { withFileTypes: true })) {
    const rel = join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...sources(rel, extensions));
      continue;
    }
    // Test files are English by rule — assertion messages, fixtures
    // carrying data-model values — and no volunteer ever reads them. Left
    // in, a CSV fixture row and an assertion message both looked like a
    // rename that had crossed the boundary.
    if (/\.test\.[cm]?tsx?$|_test\.go$/.test(entry.name)) continue;
    if (extensions.some((e) => entry.name.endsWith(e))) out.push(rel);
  }
  return out;
}

/** A comment quoting the defect it warns about is not shipped to anyone. */
const isComment = (line: string) => /^\s*(\/\/|\/\*|\*)/.test(line);

/**
 * A JSX text node is a French sentence with no quotes around it, and the
 * scan of quoted literals missed 169 of them across the interface. Tags
 * and `{…}` expressions are stripped; what is left is what a volunteer
 * reads.
 */
function jsxText(line: string): string {
  // Quoted literals go first: they are scanned on their own below, and a
  // label table ("to_contact: [\"À contacter\"]") would otherwise read as a
  // French sentence containing a column name.
  return line.replace(STRING, " ").replace(/<[^>]*>/g, " ")
    // `${…}` first: a template literal spanning the line leaves its
    // expressions behind, and they are code
    .replace(/\$\{[^}]*/g, " ").replace(/\{[^}]*\}/g, " ");
}

/** A sentence, not an identifier: three words or it is code. */
const looksLikeProse = (text: string) =>
  text.trim().split(/\s+/).filter((w) => w.length > 1).length >= 3;

function contaminated(rel: string): string[] {
  const found: string[] = [];
  for (const line of readFileSync(join(ROOT, rel), "utf8").split("\n")) {
    if (isComment(line)) continue;
    if (rel.endsWith(".tsx")) {
      const text = jsxText(line);
      if (text.length >= 12 && FRENCH.test(text) && !SQL.test(text)
        && looksLikeProse(text) && !DELIBERATE.some((d) => text.includes(d))) {
        const word = ENGLISH.find((w) => new RegExp(`\\b${w}\\b`).test(text));
        if (word) found.push(`${text.trim().slice(0, 90)} [${word}]`);
      }
    }
    for (const m of line.matchAll(STRING)) {
      const text = (m[1] ?? m[2] ?? m[3] ?? "").replace(INTERPOLATION, "…");
      if (text.length < 12 || SQL.test(text) || !FRENCH.test(text)) continue;
      if (DELIBERATE.some((d) => text.includes(d))) continue;
      const word = ENGLISH.find((w) => new RegExp(`\\b${w}\\b`).test(text));
      if (word) found.push(`${text.trim().slice(0, 90)} [${word}]`);
    }
  }
  return found;
}

describe("the French shown and the English written", () => {
  it("keeps the data model out of the sentences", () => {
    const files = [
      ...sources("api", [".go"]),
      ...sources("web/src", [".ts", ".tsx"]),
      ...sources("noyau", [".ts"]),
      ...sources("outils", [".ts"]),
    ];
    const offenders: Record<string, string[]> = {};
    for (const rel of files) {
      const found = contaminated(rel);
      if (found.length) offenders[rel] = found;
    }
    expect(offenders, "an English value ended up inside a French sentence: "
      + "a rename crossed the boundary").toEqual({});
  });
});
