// IndexedDB only reshapes a database when the version RISES. Rename a
// store at constant version and every browser that already opened the
// application keeps the old schema: each read throws NotFoundError, and
// the export that would have rescued the work throws with it. Nothing
// recovers on its own, and clearing the site data is the only way out —
// which no volunteer will find.
//
// Its own file: the database is rebuilt from scratch here, and doing that
// under the connections db.test.ts leaves open would block the upgrade.

import { describe, expect, it } from "vitest";
import * as DB from "./db.ts";
import { CURRENT_STORES, VERSION } from "./db.ts";

/**
 * A database at the CURRENT version, holding the stores this build
 * expects. Seeding at version 1 proved only that 1 → 2 upgrades — a
 * transition that cannot regress — while the defect being guarded
 * against is a store renamed WITHOUT raising the version, which leaves
 * every returning volunteer at the current version, reading stores that no
 * longer exist.
 */
function atCurrentVersion(stores: [string, string][]): Promise<void> {
  return reset().then(
    () =>
      new Promise<void>((resolve, reject) => {
        const req = indexedDB.open("paraphe", VERSION);
        req.onupgradeneeded = () => {
          for (const [name, keyPath] of stores) {
            req.result.createObjectStore(name, { keyPath });
          }
        };
        req.onsuccess = () => {
          req.result.close();
          resolve();
        };
        req.onerror = () => reject(req.error);
      }),
  );
}

/** Each fixture starts from nothing: vitest isolates files, not tests. */
function reset(): Promise<void> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.deleteDatabase("paraphe");
    req.onsuccess = () => resolve();
    req.onerror = () => reject(req.error);
    req.onblocked = () => resolve();
  });
}

/** The shape the stores had before the English rename. */
function stale(): Promise<void> {
  return reset().then(
    () =>
      new Promise<void>((resolve, reject) => {
        const req = indexedDB.open("paraphe", 1);
        req.onupgradeneeded = () => {
          req.result.createObjectStore("maires", { keyPath: "code_insee" });
          req.result.createObjectStore("suivi", { keyPath: "code_insee" });
          req.result.createObjectStore("reglages", { keyPath: "cle" });
        };
        req.onsuccess = () => {
          req.result.close();
          resolve();
        };
        req.onerror = () => reject(req.error);
      }),
  );
}

// The one case the version-1 fixture cannot see: a browser ALREADY at the
// current version. Rename a store without raising VERSION and this is
// every returning volunteer — onupgradeneeded never runs, and every read
// throws for good.
describe("a browser already at the current version", () => {
  // Written OUT, not imported: seeding the fixture from the code under
  // test made a consistent rename rename the fixture with it, and the
  // whole suite stayed green while every returning volunteer lost their
  // work. Renaming a store now fails here until the same commit bumps
  // VERSION, which is the invariant CURRENT_STORES only asked for.
  it("pins the schema to its version", () => {
    expect([VERSION, CURRENT_STORES]).toEqual([
      2,
      [
        ["mayors", "insee_code"],
        ["tracking", "insee_code"],
        ["settings", "key"],
      ],
    ]);
  });

  it("still reads the stores this build expects", async () => {
    await atCurrentVersion(CURRENT_STORES);
    await expect(DB.loadMayors()).resolves.toEqual([]);
    await expect(DB.loadTracking()).resolves.toEqual({});
    await expect(DB.readSetting("campagne", null)).resolves.toBeNull();
    await expect(DB.exportAll()).resolves.toMatchObject({
      format: "paraphe/1",
    });
  });
});

describe("a database left in an earlier shape", () => {
  it("opens, rather than throwing on every read forever", async () => {
    await stale();

    // every entry point browser mode hits at first load
    await expect(DB.loadMayors()).resolves.toEqual([]);
    await expect(DB.loadTracking()).resolves.toEqual({});
    await expect(DB.readSetting("campagne", null)).resolves.toBeNull();
    await expect(DB.exportAll()).resolves.toMatchObject({
      format: "paraphe/1",
    });

    // and it is writable, not merely readable
    await DB.replaceMayors([{ insee_code: "01022", commune: "Artemare" }]);
    await DB.saveTracking("01022", "promised", "après reprise");
    expect(await DB.loadMayors()).toHaveLength(1);
    expect((await DB.loadTracking())["01022"].status).toBe("promised");
  });
});

// Browser mode has no server to catch a renamed column. Parsed silently,
// every English key resolves to undefined, rank() takes its absent-column
// branch — which means "they endorsed" — and only the required-field guard
// stands between that and a mass false claim, on the one mode where nobody
// is watching.
describe("the published CSV", () => {
  const header = "insee_code;first_name;last_name;commune;department";

  it("loads when it carries the columns the code reads", () => {
    expect(
      DB.parseCsv(`${header}\n01001;Camille;MARTIN;Artemare;Ain`),
    ).toHaveLength(1);
  });

  it("accepts the priority list, which carries no rank column", () => {
    // it contains endorsers only — that is what rank()'s absent-column
    // fallback exists for, and requiring the column would refuse the very
    // list the application loads by default
    expect(
      DB.parseCsv(`${header}\n01001;Camille;MARTIN;Artemare;Ain`),
    ).toHaveLength(1);
  });

  it("is refused, by name, when a column has been renamed", () => {
    const french = "code_insee;prenom;nom;commune;departement";
    expect(() =>
      DB.parseCsv(`${french}\n01001;Camille;MARTIN;Artemare;Ain`),
    ).toThrow(/insee_code, first_name/);
  });
});

// Rejecting instead of hanging changes nothing on screen unless the caller
// catches: the startup effect had no catch, `ready` stayed false, and the
// page rendered "Chargement…" for good — with the export that would rescue
// the work behind that same screen.
describe("a database the browser refuses to open", () => {
  it("reports rather than hanging", async () => {
    const original = indexedDB.open;
    // what a private window does, and what a blocked upgrade now does
    (indexedDB as unknown as { open: unknown }).open = () => {
      const req: Record<string, unknown> = {};
      queueMicrotask(() => {
        (req.onerror as (() => void) | undefined)?.();
      });
      Object.defineProperty(req, "error", {
        value: new Error("SecurityError"),
      });
      return req;
    };
    try {
      await expect(DB.loadMayors()).rejects.toBeDefined();
    } finally {
      (indexedDB as unknown as { open: unknown }).open = original;
    }
  });
});
