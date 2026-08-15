// Local tracking is all browser mode owns: it is therefore the only thing
// it can lose. These tests pin the two ways it lost it — a non-atomic
// import, and a merge that crushed what it promised to keep.

import { beforeEach, describe, expect, it } from "vitest";
import type { Backup } from "./db.ts";
import * as DB from "./db.ts";

const wipe = async () => {
  await DB.eraseAll();
};

const backup = (extra: Partial<Backup> = {}): Backup => ({
  format: "paraphe/1",
  exported_at: "2026-08-13T00:00:00Z",
  mayors: [],
  tracking: [],
  settings: [],
  ...extra,
});

beforeEach(wipe);

describe("the import", () => {
  it("refuses a file that is not a paraphe export", async () => {
    await expect(DB.importAll({ format: "autre" } as Backup)).rejects.toThrow(
      /pas un export paraphe/,
    );
  });

  // The clear() and the put()s lived in two transactions: a record without
  // a key made the second one throw, left the store emptied, and the
  // screen showed "Import impossible" over tracking already destroyed.
  it("keeps existing work when merging a damaged file", async () => {
    await DB.saveTracking("01022", "promised", "trois semaines de travail");
    const report = await DB.importAll(
      backup({
        tracking: [{ status: "email_sent" } as never],
      }),
      { merge: true },
    );
    const tracking = await DB.loadTracking();
    expect(tracking["01022"]?.notes[0].note).toBe("trois semaines de travail");
    expect(report.skipped).toBe(1);
    expect(report.tracking).toBe(0);
  });

  // Overwriting is what the user asked for; what must never happen is
  // throwing AFTER clearing — a "nothing was done" message while
  // everything has just disappeared.
  it("does not fail in the middle of an overwrite", async () => {
    await DB.saveTracking("01022", "promised", "");
    const report = await DB.importAll(
      backup({
        tracking: [
          {
            insee_code: "01033",
            status: "refused",
            updated_at: "2026-08-13 à 10:00",
            notes: [],
          },
          { status: "email_sent" } as never,
        ],
      }),
      { merge: false },
    );
    expect(report.tracking).toBe(1);
    expect(report.skipped).toBe(1);
    expect(Object.keys(await DB.loadTracking())).toEqual(["01033"]);
  });

  // "Fusionner" promises to keep your work. Yet it overwrote the
  // candidate, the contacts and the signature: the next emails went out in
  // the name of the colleague whose file had just been opened.
  it("does not touch the configuration when merging", async () => {
    await DB.writeSetting("campagne", { candidat: "Camille Réel" });
    await DB.writeSetting("argument", "mon argumentaire");
    const report = await DB.importAll(
      backup({
        settings: [
          { key: "campagne", value: { candidat: "Autre Candidat" } },
          { key: "argument", value: "celui du collègue" },
        ],
      }),
      { merge: true },
    );

    expect(await DB.readSetting("campagne", null)).toEqual({
      candidat: "Camille Réel",
    });
    expect(await DB.readSetting("argument", "")).toBe("mon argumentaire");
    expect(report.settings).toBe(0);
  });

  // Per key, not all-or-nothing. The first visit downloads a list on its
  // own and records which one, so the settings store is never empty
  // afterwards: a guard on "the store is empty" stopped protecting anything
  // the moment the app has been opened once, and the campaign comes back at
  // its template values.
  it("takes the settings it does not hold, and keeps those it does", async () => {
    await DB.writeSetting("liste", "light"); // written by the first visit
    const report = await DB.importAll(
      backup({
        settings: [
          { key: "liste", value: "complete" },
          { key: "campagne", value: { candidat: "Ariane Fictive" } },
        ],
      }),
      { merge: true },
    );

    expect(await DB.readSetting("campagne", null)).toEqual({
      candidat: "Ariane Fictive",
    });
    expect(await DB.readSetting("liste", "")).toBe("light");
    expect(report.settings).toBe(1);
    expect(report.keptSettings).toBe(1);
  });

  it("takes the configuration when overwriting, and says so", async () => {
    await DB.writeSetting("argument", "le mien");
    const report = await DB.importAll(
      backup({
        settings: [{ key: "argument", value: "celui du fichier" }],
      }),
      { merge: false },
    );
    expect(await DB.readSetting("argument", "")).toBe("celui du fichier");
    expect(report.settings).toBe(1);
  });

  it("keeps the most recent version on merge", async () => {
    await DB.saveTracking("01022", "promised", "à moi, plus récent");
    const report = await DB.importAll(
      backup({
        tracking: [
          {
            insee_code: "01022",
            status: "refused",
            updated_at: "2020-01-01 à 00:00",
            notes: [
              { ts: "2020-01-01 à 00:00", status: "refused", note: "vieux" },
            ],
          },
        ],
      }),
      { merge: true },
    );
    expect((await DB.loadTracking())["01022"].status).toBe("promised");
    expect(report.skipped).toBe(1);
  });
});

describe("local tracking", () => {
  it("timestamps in local time, not UTC", async () => {
    // two hours off in summer on a call log ("rappeler avant 11 h") is a
    // missed appointment
    const e = await DB.saveTracking("01022", "to_call_back", "");
    const expected = String(new Date().getHours()).padStart(2, "0");
    expect(e.notes[0].ts).toContain(` à ${expected}:`);
  });

  it("stacks notes, most recent first", async () => {
    await DB.saveTracking("01022", "email_sent", "premier");
    const e = await DB.saveTracking("01022", "to_call_back", "second");
    expect(e.notes.map((n) => n.note)).toEqual(["second", "premier"]);
  });
});

describe("list replacement", () => {
  it("does not lose the tracking, which is keyed by INSEE code", async () => {
    await DB.replaceMayors([{ insee_code: "01022", commune: "Artemare" }]);
    await DB.saveTracking("01022", "promised", "gardé");
    await DB.replaceMayors([
      { insee_code: "01022", commune: "Artemare" },
      { insee_code: "01033", commune: "Belley" },
    ]);
    expect((await DB.loadTracking())["01022"].status).toBe("promised");
    expect(await DB.loadMayors()).toHaveLength(2);
  });
});
