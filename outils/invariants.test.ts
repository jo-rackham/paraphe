// The guards this crossing rests on, pinned by construction.
//
// Each of these is held by mutation: removing the guard leaves all 237
// tests green. They protect the two claims the project cannot get wrong —
// that a mayor is only ever thanked for an endorsement they made, and
// that the files open correctly in the spreadsheet volunteers use.

import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

import { BOM, writeCsv } from "../noyau/csv.ts";
import {
  confidenceRank,
  dedupeByInsee,
  keyAmong,
  personKey,
  type Target,
} from "./matching.ts";
import { readStrict } from "./sources.ts";

describe("the identity of an endorser", () => {
  const commune = { dept: "Vosges", commune: "Robécourt", lastName: "MARTIN" };
  const seenAs = (firstName: string) =>
    new Map([[personKey({ ...commune, firstName }), { firstName }]]);

  it("keeps two namesakes of one commune apart", () => {
    // predecessor and successor, or two spouses: merged, the one still in
    // office inherits the other's endorsement and is thanked for it. They
    // share a first TOKEN, which is why the plain key is not enough.
    const jeanLouis = { ...commune, firstName: "Jean-Louis" };
    const jeanMarc = { ...commune, firstName: "Jean-Marc" };
    expect(keyAmong(jeanMarc, seenAs("Jean-Louis"))).not.toBe(
      keyAmong(jeanLouis, new Map()),
    );
    expect(personKey({ ...commune, firstName: "Mélina" })).not.toBe(
      personKey({ ...commune, firstName: "Gilles" }),
    );
  });

  it("keeps ONE person written two ways together", () => {
    // « Jean-Claude » in 2017, « Jean-Claude Raymond » in 2022: the mayor
    // of Villautou, both times. Split, he loses a real endorsement and
    // the record that he signed in both years.
    const short = { ...commune, firstName: "Jean-Claude" };
    const long = { ...commune, firstName: "Jean-Claude Raymond" };
    expect(keyAmong(long, seenAs("Jean-Claude"))).toBe(
      keyAmong(short, new Map()),
    );
  });

  it("still recognises one person across spelling and case", () => {
    expect(personKey({ ...commune, firstName: "JEAN-LOUIS" })).toBe(
      personKey({ ...commune, firstName: "Jean-Louis" }),
    );
    expect(
      personKey({ ...commune, commune: "ROBECOURT", firstName: "Jean" }),
    ).toBe(personKey({ ...commune, firstName: "Jean" }));
  });
});

describe("one row per INSEE", () => {
  const target = (insee: string, firstName: string): Target =>
    ({
      civ: "M.",
      lastName: "MARTIN",
      firstName,
      commune: "Robécourt",
      dept: "Vosges",
      office: "Maire",
      small: [],
      others: [],
      years: new Set([2022]),
      score: 0,
      deptN: "vosges",
      communeN: "robecourt",
      rne: { insee } as Target["rne"],
      contact: undefined,
    }) as unknown as Target;

  it("refuses two DIFFERENT people at the same code", () => {
    // an INSEE names a commune: two people under one is a matching bug,
    // and shipping it thanks a mayor for a stranger's endorsement
    expect(() =>
      dedupeByInsee([target("88362", "Régine"), target("88362", "Paul")]),
    ).toThrow(/two different people/);
  });

  it("merges one person written two ways", () => {
    const { kept, mergedSpellings } = dedupeByInsee([
      target("88362", "Jean-Louis"),
      target("88362", "Louis"),
    ]);
    expect(kept).toHaveLength(1);
    expect(mergedSpellings).toBe(1);
  });

  it("reports the best-established match, whichever came first", () => {
    // One person can be matched twice, differently: the commune spelt as
    // signed in one year, found by the fuzzy fallback in the other. Taking
    // whichever record is read first would make the confidence shown to a
    // volunteer depend on the order the source files are listed in.
    const weak = { ...target("88362", "Jean-Louis"), conf: "retrouvé par nom" };
    const strong = { ...target("88362", "Jean-Louis"), conf: "exact" };
    for (const order of [
      [weak, strong],
      [strong, weak],
    ]) {
      expect(dedupeByInsee(order).kept[0].conf).toBe("exact");
    }
    expect(confidenceRank("exact")).toBeLessThan(
      confidenceRank("retrouvé par nom"),
    );
    // an unknown label is the least established, not the most
    expect(confidenceRank("exact")).toBeLessThan(confidenceRank("inconnu"));
  });
});

describe("what the files are made of", () => {
  it("ends its lines the way Python's csv.writer does", () => {
    // the byte-for-byte comparison against the Python version rests on
    // this, and so does every spreadsheet reading the file as one row
    const csv = writeCsv(["a", "b"], [{ a: "1", b: "2" }]);
    expect(csv).toContain("\r\n");
    expect(csv).not.toMatch(/[^\r]\n/);
  });

  it("starts with the BOM that keeps accents readable in Excel", () => {
    // without it Excel opens the file as Windows-1252 and every accented
    // commune is mangled — on the machine of the volunteer, silently
    expect(writeCsv(["a"], [{ a: "é" }]).startsWith(BOM)).toBe(true);
  });

  it("refuses a source that is not valid UTF-8", () => {
    // the open sources carry CP1252 leftovers: decoded leniently, a
    // commune name arrives corrupted and labels a match with a wrong
    // explanation
    const path = join(mkdtempSync(join(tmpdir(), "paraphe-")), "source.csv");
    writeFileSync(path, Buffer.from([0x61, 0xe9, 0x62])); // é in CP1252
    expect(() => readStrict(path)).toThrow(/not valid UTF-8/);
  });
});
