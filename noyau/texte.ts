// String normalisation and comparison — shared by the source crossing and
// the message engine.
//
// The port of `difflib.SequenceMatcher` below must stay FAITHFUL: the 0.93
// threshold on commune names was tuned against the Python implementation.
// Another distance (Levenshtein, Jaro) would give other matches, hence
// other mayors in the list — with nothing signalling it.

/** Uppercase, no accents or punctuation, normalised spaces. */
export function norm(s: string | null | undefined): string {
  let t = (s ?? "").normalize("NFD").replace(/\p{Mn}/gu, "");
  t = t.toUpperCase().replaceAll("Œ", "OE").replaceAll("Æ", "AE");
  t = t.replace(/[-'’/().]/g, " ").replace(/\s+/g, " ").trim();
  return t.replace(/\bSTE\b/g, "SAINTE").replace(/\bST\b/g, "SAINT");
}

export const collapse = (s: string | null | undefined): string =>
  (s ?? "").replace(/\s+/g, " ").trim();

// CP1252 bytes the directory failed to decode: erasing them would give
// "ting" for Œting, or "rue des Surs" for rue des Sœurs.
//
// All 27 positions, not a selection: the table only covered 11 of them, and
// "L'Haÿ-les-Roses" came out "L'Ha-les-Roses" — the same defect as Œting,
// one code point further. Ÿ is present in the RNE.
export const CP1252: Record<number, string> = {
  0x80: "€", 0x82: "‚", 0x83: "ƒ", 0x84: "„", 0x85: "…", 0x86: "†", 0x87: "‡",
  0x88: "ˆ", 0x89: "‰", 0x8a: "Š", 0x8b: "‹", 0x8c: "Œ", 0x8e: "Ž",
  0x91: "‘", 0x92: "’", 0x93: "“", 0x94: "”", 0x95: "•", 0x96: "–", 0x97: "—",
  0x98: "˜", 0x99: "™", 0x9a: "š", 0x9b: "›", 0x9c: "œ", 0x9e: "ž", 0x9f: "Ÿ",
};

/** Unicode category Cc: C0 and C1 controls. */
export const isControl = (p: number): boolean =>
  p < 0x20 || (p >= 0x7f && p <= 0x9f);

export function stripControls(s: string | null | undefined): string {
  let out = "";
  for (const c of s ?? "") {
    const p = c.codePointAt(0) as number;
    const replacement = CP1252[p];
    // a control becomes a SPACE, not nothing: removing it glued words
    // together — "Hôtel de Ville1 place General de GaulleBP 5" on an
    // envelope
    if (replacement !== undefined) out += replacement;
    else out += isControl(p) ? " " : c;
  }
  return out.replace(/\s{2,}/g, " ").trim();
}

/**
 * The RNE writes "Rieux-En-Val" and "Etalante"; the directory writes
 * "Rieux-en-Val" and "Étalante". When the names are identical up to accents
 * and case, the directory's label is the right one — it is what the mayor
 * reads on their mail.
 */
export function communeLabel(rneName: string, directoryName: string): string {
  const a = stripControls(directoryName);
  return a && norm(a) === norm(rneName) ? a : rneName;
}

/** 'Mme'/'M.'/'M' -> 'F'/'M'. null when the civility is out of domain. */
export function sexFromTitle(civ: string | null | undefined): string | null {
  const c = norm(civ).replace(/\.$/, "");
  if (c === "MME") return "F";
  if (c === "M") return "M";
  return null;
}

/**
 * Python's `str.title()`: first letter of each letter run capitalised, the
 * rest lowercased. "D'ARTAGNAN" -> "D'Artagnan".
 */
export function titleCase(s: string): string {
  let out = "";
  let previousIsLetter = false;
  for (const c of s) {
    const isLetter = /\p{L}/u.test(c);
    out += isLetter && !previousIsLetter ? c.toUpperCase() : c.toLowerCase();
    previousIsLetter = isLetter;
  }
  return out;
}

/** Python's `str.isupper()`: at least one letter, none lowercase. */
export const isUppercase = (s: string): boolean =>
  /\p{L}/u.test(s) && s === s.toUpperCase() && s !== s.toLowerCase();

// --- difflib -------------------------------------------------------------
//
// Port of `SequenceMatcher.ratio()` (Ratcliff-Obershelp as Python
// implements it). Python's "autojunk" heuristic only kicks in from 200
// elements in b; the commune and person names compared here are far
// shorter, so no character is ever treated as junk and the corresponding
// extension loops would have no effect.

const AUTOJUNK_THRESHOLD = 200;

function indexOfB(b: string[]): Map<string, number[]> {
  const b2j = new Map<string, number[]>();
  b.forEach((c, j) => {
    const l = b2j.get(c);
    if (l) l.push(j);
    else b2j.set(c, [j]);
  });
  return b2j;
}

function longestMatch(
  a: string[], b2j: Map<string, number[]>,
  alo: number, ahi: number, blo: number, bhi: number,
): [number, number, number] {
  let besti = alo;
  let bestj = blo;
  let bestsize = 0;
  let j2len = new Map<number, number>();
  for (let i = alo; i < ahi; i++) {
    const fresh = new Map<number, number>();
    for (const j of b2j.get(a[i]) ?? []) {
      if (j < blo) continue;
      if (j >= bhi) break;
      const k = (j2len.get(j - 1) ?? 0) + 1;
      fresh.set(j, k);
      if (k > bestsize) {
        besti = i - k + 1;
        bestj = j - k + 1;
        bestsize = k;
      }
    }
    j2len = fresh;
  }
  return [besti, bestj, bestsize];
}

/** Total number of matched characters, in the difflib sense. */
function matched(a: string[], b: string[], b2j: Map<string, number[]>): number {
  let total = 0;
  const stack: [number, number, number, number][] = [[0, a.length, 0, b.length]];
  while (stack.length) {
    const [alo, ahi, blo, bhi] = stack.pop() as [number, number, number, number];
    const [i, j, k] = longestMatch(a, b2j, alo, ahi, blo, bhi);
    if (!k) continue;
    total += k;
    if (alo < i && blo < j) stack.push([alo, i, blo, j]);
    if (i + k < ahi && j + k < bhi) stack.push([i + k, ahi, j + k, bhi]);
  }
  return total;
}

/** `difflib.SequenceMatcher(None, a, b).ratio()`. */
export function ratio(stringA: string, stringB: string): number {
  const a = [...stringA];
  const b = [...stringB];
  if (b.length >= AUTOJUNK_THRESHOLD) {
    throw new Error(
      `ratio() received a string of ${b.length} characters: beyond `
      + `${AUTOJUNK_THRESHOLD}, Python enables the "autojunk" heuristic that `
      + "this port does not reproduce, and the two implementations would "
      + "diverge.");
  }
  const total = a.length + b.length;
  if (!total) return 1;
  return (2 * matched(a, b, indexOfB(b))) / total;
}

/** Upper bound of ratio(), to discard fast: bag intersection. */
function quickRatio(a: string[], b: string[]): number {
  const total = a.length + b.length;
  if (!total) return 1;
  const counts = new Map<string, number>();
  for (const c of b) counts.set(c, (counts.get(c) ?? 0) + 1);
  let n = 0;
  for (const c of a) {
    const left = counts.get(c) ?? 0;
    counts.set(c, left - 1);
    if (left > 0) n++;
  }
  return (2 * n) / total;
}

/**
 * `difflib.get_close_matches(word, possibilities, n=1, cutoff)`.
 * Tie-breaking follows Python: `heapq.nlargest` compares the (score,
 * string) pair, so at equal score the greater string wins.
 */
export function closestMatch(
  word: string, possibilities: Iterable<string>, cutoff: number,
): string | null {
  const b = [...word];
  const b2j = indexOfB(b);
  let best: string | null = null;
  let bestScore = 0;
  for (const x of possibilities) {
    const a = [...x];
    const total = a.length + b.length;
    if (total && (2 * Math.min(a.length, b.length)) / total < cutoff) continue;
    if (quickRatio(a, b) < cutoff) continue;
    const r = total ? (2 * matched(a, b, b2j)) / total : 1;
    if (r < cutoff) continue;
    if (best === null || r > bestScore
        || (r === bestScore && x > best)) {
      best = x;
      bestScore = r;
    }
  }
  return best;
}
