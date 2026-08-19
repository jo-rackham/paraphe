// The difflib port must stay FAITHFUL to Python: the 0.93 threshold on
// commune names is tuned against its implementation. The reference values
// come from `difflib.SequenceMatcher(None, a, b).ratio()`: if they stop
// matching, different mayors enter the list and nothing else signals it.

import { describe, expect, it } from "vitest";
import {
  closestMatch,
  norm,
  ratio,
  sexFromTitle,
  stripControls,
  tight,
  titleCase,
} from "./texte.ts";

const REFERENCES: [string, string, number][] = [
  ["SAINT MARTIN", "SAINT MARTIN", 1.0],
  // 0.875: at the original 0.87 cutoff, Goncourt caught Voncourt
  ["GONCOURT", "VONCOURT", 0.875],
  ["ESNES EN ARGONNE", "GESNES EN ARGONNE", 0.9696969696969697],
  ["HENRY", "HENRI", 0.8],
  ["MAGALI", "MAGALLI", 0.9230769230769231],
  ["JACKY", "JACQUY", 0.7272727272727273],
  // 0.889: invisible to spelling, the RNE sex code is what decides
  ["CHRISTIAN", "CHRISTINE", 0.8888888888888888],
  ["MARIE CECILE", "MARIE EVE", 0.7619047619047619],
  ["", "", 1.0],
  ["A", "", 0.0],
  ["OETING", "OETING", 1.0],
  ["SAINTE FICTION 12", "SAINTE FICTION 2", 0.9696969696969697],
];

describe("ratio(), difflib port", () => {
  it.each(REFERENCES)("%s ~ %s = %f", (a, b, expected) => {
    expect(ratio(a, b)).toBeCloseTo(expected, 12);
  });

  it("refuses a string long enough to trigger Python's autojunk", () => {
    // beyond that, the two implementations would silently diverge
    expect(() => ratio("x", "y".repeat(200))).toThrow(/autojunk/);
  });
});

describe("closestMatch(), get_close_matches port", () => {
  const choices = ["ABBEVILLE", "ABBEVILLERS", "ABBEVILLE SAINT LUCIEN"];
  it.each([
    ["ABBEVILLE", 0.93, "ABBEVILLE"],
    ["ABBEVILLERS", 0.93, "ABBEVILLERS"],
    ["ABBEVILL", 0.93, "ABBEVILLE"],
    ["ABBEVILLE", 0.5, "ABBEVILLE"],
  ])("%s at cutoff %f -> %s", (word, cutoff, expected) => {
    expect(closestMatch(word, choices, cutoff)).toBe(expected);
  });

  it("returns nothing below the cutoff", () => {
    expect(closestMatch("MARSEILLE", choices, 0.93)).toBeNull();
  });
});

describe("normalisation", () => {
  it("folds accents, punctuation and saint abbreviations", () => {
    expect(norm("Rieux-en-Val")).toBe("RIEUX EN VAL");
    expect(norm("Étalante")).toBe("ETALANTE");
    expect(norm("St-Œting")).toBe("SAINT OETING");
    expect(norm("Ste Marie")).toBe("SAINTE MARIE");
  });

  it("only returns a sex for a civility within the domain", () => {
    expect(sexFromTitle("Mme")).toBe("F");
    expect(sexFromTitle("M.")).toBe("M");
    expect(sexFromTitle("Dr")).toBeNull();
  });

  it("title-cases like Python", () => {
    expect(titleCase("THOUY")).toBe("Thouy");
    expect(titleCase("D'ARTAGNAN")).toBe("D'Artagnan");
    expect(titleCase("LE-GALL")).toBe("Le-Gall");
  });
});

describe("stripControls()", () => {
  // The table only covered 11 of the 27 CP1252 positions: "L'Haÿ-les-Roses"
  // comes out "L'Ha-les-Roses". Œ sits one code point further, and Ÿ is
  // present in the national register of elected officials.
  it.each([
    ["\u009f", "Ÿ"],
    ["\u008c", "Œ"],
    ["\u009c", "œ"],
    ["\u0080", "€"],
    ["\u0092", "’"],
    ["\u009e", "ž"],
  ])("decodes CP1252 byte %j as %s", (raw, expected) => {
    expect(stripControls(`x${raw}y`)).toBe(`x${expected}y`);
  });

  // 0x9F is the UPPERCASE Ÿ, and that is exactly how the RNE writes commune
  // names ("L'HAŸ-LES-ROSES", "AŸ-CHAMPAGNE").
  it("keeps communes whose name carries a badly decoded Ÿ", () => {
    expect(stripControls("L'HA\u009f-LES-ROSES")).toBe("L'HAŸ-LES-ROSES");
    expect(stripControls("A\u009f-CHAMPAGNE")).toBe("AŸ-CHAMPAGNE");
  });

  // Removing a control without putting anything in its place glued words
  // together, and five directory cards already carry line breaks.
  it("does not glue words separated by a control character", () => {
    expect(stripControls("Hôtel de Ville\n1 place de la Mairie")).toBe(
      "Hôtel de Ville 1 place de la Mairie",
    );
    expect(stripControls("Lun-Ven\t9h-12h")).toBe("Lun-Ven 9h-12h");
  });

  it("collapses multiple spaces, non-breaking included", () => {
    expect(stripControls("4  route\u00a0 d'Aigre")).toBe("4 route d'Aigre");
  });
});

// « Cram-Chaban » and « Cramchaban » are one commune, and `norm` cannot say
// so: it turns a hyphen into a SPACE. That mattered far past the spelling —
// reached by the fuzzy tier instead, the match is `approx`, and `approx`
// disables the counter proof that concludes « successeur en place ».
describe("the tight form of a name", () => {
  it.each([
    ["Cram-Chaban", "Cramchaban"],
    ["L'Isle-en-Rigault", "Lisle-en-Rigault"],
    ["Puka Puka", "Pukapuka"],
    ["Saint-Denis", "St Denis"],
    ["L'Haÿ-les-Roses", "LHay les Roses"],
  ])("reads %s and %s as the same commune", (a, b) => {
    expect(tight(a)).toBe(tight(b));
  });

  // It is an EXACT tier and not a similarity: anything other than a
  // separator still separates. Otherwise it would be the fuzzy match it
  // exists to avoid — and at 0.87 « Goncourt » already catches « Voncourt ».
  it.each([
    ["Goncourt", "Voncourt"],
    ["Esnes-en-Argonne", "Gesnes-en-Argonne"],
    ["Neuville", "Neuvillette"],
  ])("keeps %s and %s apart", (a, b) => {
    expect(tight(a)).not.toBe(tight(b));
  });

  // Derived FROM `norm`, so it inherits the ST → SAINT expansion — which
  // only fires on word boundaries, and would be lost if the separators went
  // first: « ST-DENIS » would tighten to « STDENIS » and never reach
  // « SAINTDENIS ».
  it("keeps the expansions norm makes on word boundaries", () => {
    expect(tight("St-Denis")).toBe("SAINTDENIS");
    expect(tight("Ste-Marie")).toBe("SAINTEMARIE");
    expect(tight("Œting")).toBe("OETING");
  });
});
