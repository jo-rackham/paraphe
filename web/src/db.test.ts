// Local tracking is all browser mode owns: it is therefore the only thing
// it can lose. These tests pin the two ways it lost it — a non-atomic
// import, and a merge that crushed what it promised to keep.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Backup } from "./db.ts";
import * as DB from "./db.ts";
import type { Tracking } from "./types.ts";

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

// Correcting and removing, with no server and no row identifier: a note is
// named by its POSITION plus the content the screen was showing.
//
// TIME IS FROZEN AND MOVED BY HAND, and that is not decoration. `timestamp()`
// has MINUTE granularity by intent — it is read in a call log, « rappeler
// avant 11 h » — so a whole test file runs inside one of them, and every
// assertion comparing a note's `ts` with a fresh `timestamp()` compares a
// string with itself. Measured: `editNote` made to rewrite the card's date,
// and `deleteNote` made to date the card NOW instead of by the line that now
// decides — two mutations against this file's own doctrine, and all 19 tests
// stayed green. Only Date is faked: fake-indexeddb runs on real timers.
describe("revising a note", () => {
  const AT = (minute: number) => new Date(2026, 7, 20, 10, minute, 0);
  /** What `timestamp()` writes at that minute. */
  const STAMP = (minute: number) =>
    `2026-08-20 à 10:${String(minute).padStart(2, "0")}`;
  // the minute every revision happens at: LATER than all three notes, so a
  // date taken « now » cannot be mistaken for a date taken from a note
  const NOW = 9;

  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["Date"] });
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  /** Three notes, each of its OWN minute, then the clock moved to NOW. */
  const three = async () => {
    vi.setSystemTime(AT(0));
    await DB.saveTracking("01022", "email_sent", "courriel");
    vi.setSystemTime(AT(1));
    await DB.saveTracking("01022", "to_call_back", "appel");
    vi.setSystemTime(AT(2));
    const e = await DB.saveTracking("01022", "refused", "refus");
    vi.setSystemTime(AT(NOW));
    return e;
  };

  it("corrects the words and touches no other column", async () => {
    const e = await three();
    const [head] = e.notes;
    const after = await DB.editNote("01022", 0, head, "refus poli");
    expect(after.notes[0].note).toBe("refus poli");
    expect(after.notes[0].status).toBe(head.status);
    expect(after.notes[0].ts).toBe(STAMP(2));
    // the mark is taken NOW, and it is the only date the correction writes
    expect(after.notes[0].edited_at).toBe(STAMP(NOW));
    // the card's own status and date are not a spelling either
    expect(after.status).toBe(e.status);
    expect(after.updated_at).toBe(STAMP(2));
  });

  it("marks only the line that was corrected", async () => {
    const e = await three();
    const after = await DB.editNote("01022", 1, e.notes[1], "appel corrigé");
    expect(after.notes.filter((n) => n.edited_at)).toHaveLength(1);
    expect(after.notes[1].edited_at).toBe(STAMP(NOW));
  });

  // THE HISTORY IS THE REGISTER AND `status` IS ITS HEAD, the server's rule
  // written on this side: a card whose last note has gone would otherwise keep
  // announcing « refusé » with nothing on record saying so.
  it("rolls the card back to what the history then says", async () => {
    const e = await three();
    const after = await DB.deleteNote("01022", 0, e.notes[0]);
    expect(after.notes.map((n) => n.note)).toEqual(["appel", "courriel"]);
    expect(after.status).toBe("to_call_back");
    // dated by the LINE THAT NOW DECIDES, not by the moment of the removal
    expect(after.updated_at).toBe(STAMP(1));
  });

  it("leaves the card alone when the line was not the head", async () => {
    const e = await three();
    const after = await DB.deleteNote("01022", 1, e.notes[1]);
    expect(after.notes.map((n) => n.note)).toEqual(["refus", "courriel"]);
    expect(after.status).toBe("refused");
    expect(after.updated_at).toBe(STAMP(2));
  });

  // An empty history is a card nobody has contacted. Left at « refusé », it
  // is a status nobody ever wrote and the mayor is off everyone's list.
  it("gives back a card nobody has contacted when the last line goes", async () => {
    vi.setSystemTime(AT(0));
    const e = await DB.saveTracking("01022", "refused", "seul");
    vi.setSystemTime(AT(NOW));
    const after = await DB.deleteNote("01022", 0, e.notes[0]);
    expect(after.notes).toEqual([]);
    expect(after.status).toBe("to_contact");
    // no line left to date it: the card moved NOW, and says so
    expect(after.updated_at).toBe(STAMP(NOW));
  });

  // The `seen` of the team version, written on this side of the wire: the
  // array is re-read, so position 0 may no longer be the line the volunteer
  // pressed — a second tab, a card left open while an import landed. Refusing
  // beats rewriting a note nobody meant to touch.
  it("refuses a position that no longer holds what the screen showed", async () => {
    const e = await three();
    const stale = e.notes[0];
    await DB.saveTracking("01022", "promised", "entre-temps");
    await expect(DB.editNote("01022", 0, stale, "trop tard")).rejects.toThrow(
      /a changé depuis son affichage/,
    );
    await expect(DB.deleteNote("01022", 0, stale)).rejects.toThrow(
      /a changé depuis son affichage/,
    );
    expect((await DB.loadTracking())["01022"].notes).toHaveLength(4);
  });

  // THE CHECK AND THE WRITE ARE ONE TRANSACTION, or the check protects
  // nothing. Read in a readonly transaction and written in a second one, both
  // callers read the same array, both wrote, both answered « Note
  // modifiée. » — and one correction was gone. « Deux onglets sur la même
  // fiche » is exactly the case the comparison exists for, and it is the one
  // it let through. IndexedDB queues readwrite transactions over a store, so
  // in ONE transaction the second read sees the first write and refuses.
  it("lets no concurrent correction overwrite another", async () => {
    const e = await three();
    const head = e.notes[0];
    const both = await Promise.allSettled([
      DB.editNote("01022", 0, head, "premier"),
      DB.editNote("01022", 0, head, "second"),
    ]);
    const landed = both.filter((r) => r.status === "fulfilled");
    expect(landed, "both corrections were accepted").toHaveLength(1);
    // and the one told it worked is the one that is there
    const told = (landed[0] as PromiseFulfilledResult<Tracking>).value.notes[0]
      .note;
    const stored = (await DB.loadTracking())["01022"].notes[0].note;
    expect(stored, "the correction that was accepted is not the one kept").toBe(
      told,
    );
  });

  // The same window, one act over: a removal racing a fresh outcome being
  // recorded. `saveTracking` reads and writes in two transactions too, so the
  // note it prepends and the note the removal takes away are decided on the
  // same array — and whichever writes last decides alone.
  it("lets no concurrent write lose a note", async () => {
    const e = await three();
    await Promise.allSettled([
      DB.deleteNote("01022", 0, e.notes[0]),
      DB.saveTracking("01022", "promised", "pendant ce temps"),
    ]);
    const notes = (await DB.loadTracking())["01022"].notes.map((n) => n.note);
    expect(notes, "the fresh outcome was lost").toContain("pendant ce temps");
    expect(notes, "the removal was undone").not.toContain("refus");
  });

  it("refuses a position that is not there at all", async () => {
    const e = await three();
    await expect(DB.deleteNote("01022", 9, e.notes[0])).rejects.toThrow(
      /a changé depuis son affichage/,
    );
    await expect(DB.deleteNote("01033", 0, e.notes[0])).rejects.toThrow(
      /a changé depuis son affichage/,
    );
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
