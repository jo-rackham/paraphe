// The identity of an endorser, and how a 2017/2022 endorsement is matched
// onto the 2026 RNE. The rules the whole crossing rests on live here:
// identity is commune + surname + FIRST NAME, whole first names are
// compared (truncation groups, contradiction separates), and the RNE sex
// code is the discriminant spelling cannot provide.

import { norm, ratio, sexFromTitle } from "../noyau/texte.ts";
import type { Contact, Official } from "./sources.ts";

export interface Person {
  civ: string;
  lastName: string;
  firstName: string;
  commune: string;
  dept: string;
  office: string;
  small: string[];
  others: string[];
  years: Set<number>;
  score: number;
  status?: string;
  communeInsee?: string;
  // normalised forms of the aggregation key: recomputing them from a
  // concatenated key would be wrong, department and commune contain spaces
  deptN: string;
  communeN: string;
}

export interface Target extends Person {
  rne: Official;
  contact: Contact | undefined;
  conf: string;
}

/** Code-point string comparison, like Python. */
export const cmp = (a: string, b: string): number =>
  a < b ? -1 : a > b ? 1 : 0;

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
  const subset = (x: Set<string>, y: Set<string>) =>
    [...x].every((v) => y.has(v));
  if (subset(sa, sb) || subset(sb, sa)) return "ok";
  // only common positions are compared: "Jean-Louis" and "Louis" do not
  // have the same number of tokens
  let worst = Infinity;
  for (let i = 0; i < Math.min(ta.length, tb.length); i++) {
    worst = Math.min(worst, ratio(ta[i], tb[i]));
  }
  if (worst >= 0.8) return "ok"; // Henry/Henri, Magali/Magalli
  if (worst >= 0.6) return "unsure"; // Jacky/Jacquy: plausible, to check
  return "different";
}

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
  return JSON.stringify([
    norm(p.dept),
    norm(p.commune),
    norm(p.lastName),
    norm(p.firstName).split(" ").filter(Boolean)[0] ?? "",
    discriminator,
  ]);
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
  if (
    sharing &&
    compareFirstNames(sharing.firstName, p.firstName) === "different"
  ) {
    return personKey(p, norm(p.firstName));
  }
  return key;
}

/**
 * Is the endorser the current mayor? 'ok' | 'unsure' | 'different'.
 *
 * The RNE sex code decides spousal or filial successions that spelling
 * alone conflates (Christian → Christine: ratio 0.89).
 */
export function compareIdentity(
  rec: { lastName: string; firstName: string; civ: string },
  row: Official,
): string {
  const lastP = norm(rec.lastName);
  const lastR = norm(row.lastName);
  if (
    !(
      lastP === lastR ||
      lastR.split(" ").includes(lastP) ||
      lastP.split(" ").includes(lastR)
    )
  ) {
    return "different";
  }
  const verdict = compareFirstNames(rec.firstName, row.firstName);
  const sexP = sexFromTitle(rec.civ);
  if (sexP && row.sex && sexP !== row.sex) {
    // last AND first names strictly identical: the Conseil constitutionnel
    // file's civility is what is wrong ("M. Sophie PRADEL"), not two
    // different people. Assert nothing: the doubt goes to 03 "to check",
    // never to 02 "successor in place".
    if (lastP === lastR && norm(rec.firstName) === norm(row.firstName))
      return "unsure";
    return verdict === "ok" ? "unsure" : "different";
  }
  return verdict;
}

/** How well a match is established; 0 is the best. */
export function confidenceRank(conf: string): number {
  return (
    ["exact", "commune approchée", "retrouvé par nom"].indexOf(conf) + 1 ||
    Number.MAX_SAFE_INTEGER
  );
}

/**
 * One row per INSEE. A commune's name spelt differently between 2017 and
 * 2022 (compound, accents) yields two rows for one person — merge. At
 * equal INSEE with DIFFERENT first names it is a matching bug, and it
 * throws: a duplicated INSEE in the output is a mayor thanked for a
 * stranger's endorsement.
 */
export function dedupeByInsee(rows: Target[]): {
  kept: Target[];
  mergedSpellings: number;
} {
  const byInsee = new Map<string, Target>();
  let mergedSpellings = 0;
  for (const r of rows) {
    const k = r.rne.insee;
    const target = byInsee.get(k);
    if (!target) {
      byInsee.set(k, r);
      continue;
    }
    if (compareFirstNames(target.firstName, r.firstName) === "different") {
      throw new Error(
        `two different people matched onto INSEE ${k}: ` +
          `${target.firstName} ${target.lastName} / ${r.firstName} ${r.lastName}`,
      );
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
    target.score =
      2 * target.small.filter((t) => t.includes("(A)")).length +
      target.small.filter((t) => t.includes("(B)")).length +
      (target.years.size >= 2 ? 1 : 0);
  }
  const kept = [...byInsee.values()].sort(
    (a, b) =>
      b.score - a.score || cmp(a.dept, b.dept) || cmp(a.commune, b.commune),
  );
  return { kept, mergedSpellings };
}
