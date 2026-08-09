// One version everywhere, or the release lies.
//
// The image tag and the OCI chart both derive from the git tag, so a stale
// Chart.yaml does not corrupt what CI publishes — it corrupts what someone
// installing from a clone gets, and what `helm show chart` reports about a
// deployed release. Divergence here is silent by construction: nothing at
// runtime compares these files.

import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";

import { ROOT } from "./config.ts";
import { versions, writeVersion } from "./version.ts";

const CHART = join(ROOT, "chart", "paraphe", "Chart.yaml");
const original = versions()["package.json"];

afterEach(() => {
  writeVersion(original);
});

describe("the version", () => {
  it("is the same in every file that carries one", () => {
    const found = versions();
    expect(Object.keys(found).length).toBeGreaterThan(3);
    expect(new Set(Object.values(found)),
      `versions disagree: ${JSON.stringify(found)}`).toEqual(new Set([original]));
  });

  it("moves everywhere at once, or nowhere", () => {
    writeVersion("9.9.9");
    expect(new Set(Object.values(versions()))).toEqual(new Set(["9.9.9"]));
  });

  it("leaves the chart's comments in place", () => {
    const before = readFileSync(CHART, "utf8");
    const comments = (s: string) => s.split("\n").filter((l) => l.startsWith("#"));
    writeVersion("9.9.9");
    const after = readFileSync(CHART, "utf8");
    expect(comments(after)).toEqual(comments(before));
    // and only the two version lines moved
    const differing = before.split("\n")
      .map((l, i) => [l, after.split("\n")[i]])
      .filter(([a, b]) => a !== b);
    expect(differing.length).toBe(2);
  });

  it("refuses anything that is not x.y.z", () => {
    for (const bad of ["v1.0.0", "1.0", "latest", "1.0.0.0", ""]) {
      expect(() => writeVersion(bad), `« ${bad} » was accepted`)
        .toThrow(/x\.y\.z/);
    }
  });
});
