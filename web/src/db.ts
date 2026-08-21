// Local tracking — IndexedDB, never the network.
//
// The whole value of this version fits in one sentence: no data leaves the
// computer. So no fetch to a server, no analytics, no remote font. JSON
// export/import replaces synchronisation: the user decides what they
// share, and with whom.

import { parseRecords } from "../../noyau/csv.ts";
import type { Mayor, Tracking } from "./types.ts";

const BASE = "paraphe";
// Bump this on ANY change to the stores below. IndexedDB only runs
// `onupgradeneeded` when the version rises: rename a store without raising
// it and every browser that already opened the application keeps its old
// schema, every read throws NotFoundError, and even the export that would
// have rescued the work fails. Nothing recovers on its own.
export const VERSION = 2;

type Store = "mayors" | "tracking" | "settings";
const STORES: { name: Store; keyPath: string }[] = [
  { name: "mayors", keyPath: "insee_code" },
  { name: "tracking", keyPath: "insee_code" },
  { name: "settings", keyPath: "key" },
];

interface Setting {
  key: string;
  value: unknown;
}

export interface Backup {
  format: string;
  exported_at: string;
  mayors: Mayor[];
  tracking: Tracking[];
  settings: Setting[];
}

function open(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(BASE, VERSION);
    req.onupgradeneeded = () => {
      const db = req.result;
      const expected = new Set<string>(STORES.map((s) => s.name));
      // A store nobody reads any more holds a schema nobody can read: drop
      // it rather than leave the database half in one shape, half in the
      // other. No content is carried over — the browser version has never
      // been published, so no user data exists in an earlier shape.
      for (const name of [...db.objectStoreNames]) {
        if (!expected.has(name)) db.deleteObjectStore(name);
      }
      for (const { name, keyPath } of STORES) {
        if (!db.objectStoreNames.contains(name)) {
          db.createObjectStore(name, { keyPath });
        }
      }
    };
    // A blocked upgrade fires NO event: without this the promise never
    // settles, the application stays on "Chargement…" for good, and the
    // export that would rescue the work is behind that same screen.
    req.onblocked = () =>
      reject(
        new Error(
          "Une autre fenêtre de paraphe est ouverte sur une version précédente. " +
            "Fermez les autres onglets de ce site, puis rechargez cette page.",
        ),
      );
    req.onsuccess = () => {
      // and this connection steps aside when ANOTHER tab needs to upgrade,
      // rather than blocking it in turn
      req.result.onversionchange = () => req.result.close();
      resolve(req.result);
    };
    req.onerror = () => reject(req.error);
  });
}

function tx<T>(
  db: IDBDatabase,
  store: Store,
  mode: IDBTransactionMode,
  action: (s: IDBObjectStore) => IDBRequest<T> | undefined,
): Promise<T | undefined> {
  return new Promise((resolve, reject) => {
    const t = db.transaction(store, mode);
    const r = action(t.objectStore(store));
    t.oncomplete = () => resolve(r ? r.result : undefined);
    t.onerror = () => reject(t.error);
    t.onabort = () => reject(t.error);
  });
}

const settle = (t: IDBTransaction): Promise<void> =>
  new Promise((resolve, reject) => {
    t.oncomplete = () => resolve();
    t.onerror = () => reject(t.error);
  });

/**
 * The stores as this build expects them, for the tests to seed a database
 * that is ALREADY current. Frozen on purpose: renaming a store here
 * without raising VERSION is the defect, and the change has to be made in
 * both places, in the same commit.
 */
export const CURRENT_STORES: [string, string][] = [
  ["mayors", "insee_code"],
  ["tracking", "insee_code"],
  ["settings", "key"],
];

export async function loadMayors(): Promise<Mayor[]> {
  const db = await open();
  return (await tx<Mayor[]>(db, "mayors", "readonly", (s) => s.getAll())) ?? [];
}

export async function replaceMayors(rows: Mayor[]): Promise<number> {
  const db = await open();
  // ONE transaction for the clearing and the writes: in two transactions,
  // a `put` that throws leaves the store emptied and the work lost, on an
  // error message suggesting nothing happened.
  const t = db.transaction("mayors", "readwrite");
  const s = t.objectStore("mayors");
  s.clear();
  for (const r of rows) s.put(r);
  await settle(t);
  // what is STORED, not what is handed over: two rows sharing an INSEE
  // code are one record, and announcing the file's line count told a
  // volunteer they had mayors the list does not contain
  return new Set(rows.map((r) => r.insee_code)).size;
}

/**
 * One mayor's record, straight from the store.
 *
 * For the moment after a REFUSAL: this browser holds a version the screen has
 * not seen — another tab wrote — and the screen has to show it rather than
 * ask for a gesture. `loadTracking` would read every mayor a volunteer has
 * ever worked to answer about one.
 */
export async function readTracking(insee: string): Promise<Tracking | null> {
  const db = await open();
  return (
    (await tx<Tracking>(db, "tracking", "readonly", (s) => s.get(insee))) ??
    null
  );
}

export async function loadTracking(): Promise<Record<string, Tracking>> {
  const db = await open();
  const rows =
    (await tx<Tracking[]>(db, "tracking", "readonly", (s) => s.getAll())) ?? [];
  return Object.fromEntries(rows.map((r) => [r.insee_code, r]));
}

// LOCAL time: the timestamp shows in a call log ("rappeler avant 11 h"),
// and UTC shifted everything by two hours in summer.
function timestamp(): string {
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, "0");
  return (
    `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}` +
    ` à ${p(d.getHours())}:${p(d.getMinutes())}`
  );
}

/**
 * Read, decide and write a mayor's record in ONE readwrite transaction.
 *
 * Every write here is a read-modify-write of the same array, and read in one
 * transaction and written in another, the read is worth nothing: two callers
 * see the same array, both write, both are told it worked, and one of the two
 * pieces of work is gone. Two tabs on one card is all it takes, and this store
 * is the ONLY thing browser mode owns — there is no server holding a second
 * copy. IndexedDB queues readwrite transactions over a store, so inside one
 * the read sees every write committed before it.
 *
 * `decide` returns the record to store, or null to store nothing — which is
 * how a refusal is expressed without aborting a transaction (an abort fires
 * `onabort`, not `onerror`, and a promise waiting on the other two never
 * settles). The requests are issued from `onsuccess`, synchronously, or the
 * transaction commits out from under the second one.
 *
 * `decide` THROWING is a different thing and is not a refusal: IndexedDB
 * aborts the transaction and drops the message, so the caller gets an
 * `AbortError` and the screen shows that word. It takes a stored record this
 * application cannot write — `notes` that is not an array — and such a record
 * breaks the card's own render before reaching here, so nothing guards
 * against it. `validTracking` is what refuses one at the door, on import.
 */
function reviseTracking(
  db: IDBDatabase,
  insee: string,
  decide: (current: Tracking | undefined) => Tracking | null,
): Promise<Tracking | null> {
  return new Promise((resolve, reject) => {
    const t = db.transaction("tracking", "readwrite");
    const s = t.objectStore("tracking");
    const got = s.get(insee);
    let entry: Tracking | null = null;
    got.onsuccess = () => {
      entry = decide(got.result as Tracking | undefined);
      if (entry) s.put(entry);
    };
    t.oncomplete = () => resolve(entry);
    t.onerror = () => reject(t.error);
    t.onabort = () => reject(t.error);
  });
}

export async function saveTracking(
  insee: string,
  status: string,
  note: string,
): Promise<Tracking> {
  const db = await open();
  const entry = await reviseTracking(db, insee, (current) => ({
    insee_code: insee,
    status,
    updated_at: timestamp(),
    notes: [{ ts: timestamp(), status, note }, ...(current?.notes ?? [])],
  }));
  // `decide` never returns null here: recording an outcome refuses nothing.
  return entry as Tracking;
}

/**
 * A note held here has NO identifier, and does not gain one: a note already
 * written would have none, and the two paths would have to be told apart for
 * ever. It is named by its POSITION plus the content the screen was showing —
 * the `seen` of the team version, written on this side of the wire.
 *
 * `expected` is compared rather than trusted, because the array is re-read:
 * two tabs on the same card, or a card left open while an import lands, and
 * position 0 is no longer the line the volunteer pressed. Refusing beats
 * rewriting a note nobody meant to touch.
 *
 * Assumed limit: two notes of the same minute, the same status and the same
 * text are indistinguishable here, so the comparison lets a swap between them
 * through. They are the same line to every reader — editing either is the
 * same act — and the alternative is an identifier a note already written
 * would not have.
 */
type LocalNote = Tracking["notes"][number];
/**
 * What identifies a note beside its position: the three fields a write sets
 * and nothing touches afterwards. Stated as a SUBSET rather than as the whole
 * record, because the caller holds a card's `Note` — the shape the team
 * version's API answers with — and the fields the two do not share are
 * precisely the ones this comparison has no business reading.
 */
type NoteSeen = Pick<LocalNote, "ts" | "status" | "note">;

/**
 * Revises one line of a record, inside the ONE transaction that read it: the
 * check below is an optimistic-concurrency check, and checked in a different
 * transaction from the write it guards it guards nothing.
 *
 * `revise` is handed the record and the confirmed array, and returns what to
 * store. A position that no longer holds `expected` stores nothing and the
 * refusal is raised out here, where a promise can carry it.
 */
async function reviseNote(
  insee: string,
  index: number,
  expected: NoteSeen,
  revise: (current: Tracking, notes: LocalNote[]) => Tracking,
): Promise<Tracking> {
  const db = await open();
  const entry = await reviseTracking(db, insee, (current) => {
    const notes = current?.notes ?? [];
    const at = notes[index];
    if (
      !current ||
      !at ||
      at.ts !== expected.ts ||
      at.status !== expected.status ||
      at.note !== expected.note
    ) {
      return null;
    }
    return revise(current, notes);
  });
  if (!entry) {
    // What the sentence asks for has to be a gesture that DOES something.
    // « rouvrez la fiche » sent the volunteer to the list and back, where
    // nothing had changed — this mode reads its store once, at load — and the
    // second attempt was refused the same way; only a full reload worked, and
    // nothing said so. The history refreshes itself on the refusal now, and
    // the words that are left name the gesture that shows it: the volunteer's
    // own editor is open OVER that line, holding the text they typed, which a
    // refusal must not throw away.
    throw new Error(
      "Cette note a changé depuis son affichage — une autre fenêtre l'a " +
        "modifiée. Annulez pour voir son texte.",
    );
  }
  return entry;
}

/**
 * Correcting the WORDS, and nothing else — the same rule as the server's. The
 * status the note recorded and when the contact happened are not a spelling,
 * so the card's own status and date are left exactly where they were.
 */
export const editNote = (
  insee: string,
  index: number,
  expected: NoteSeen,
  text: string,
): Promise<Tracking> =>
  reviseNote(insee, index, expected, (current, notes) => ({
    ...current,
    notes: notes.map((n, i) =>
      i === index ? { ...n, note: text, edited_at: timestamp() } : n,
    ),
  }));

/**
 * Removing one, and putting the card back to what the history then says.
 *
 * THE HISTORY IS THE REGISTER AND `status` IS ITS HEAD, the server's rule
 * written on this side: a card whose last note has just gone would otherwise
 * keep announcing « signé » with nothing on record saying so, and emptying
 * the history left a status nobody ever wrote.
 */
export const deleteNote = (
  insee: string,
  index: number,
  expected: NoteSeen,
): Promise<Tracking> =>
  reviseNote(insee, index, expected, (current, held) => {
    const notes = held.filter((_, i) => i !== index);
    return {
      ...current,
      status: notes[0]?.status ?? "to_contact",
      // the line that now decides dates the card; with none left, the card
      // moved NOW
      updated_at: notes[0]?.ts ?? timestamp(),
      notes,
    };
  });

export async function readSetting<T>(key: string, fallback: T): Promise<T> {
  const db = await open();
  const r = await tx<Setting>(db, "settings", "readonly", (s) => s.get(key));
  return r ? (r.value as T) : fallback;
}

export async function writeSetting(key: string, value: unknown): Promise<void> {
  const db = await open();
  await tx(db, "settings", "readwrite", (s) => s.put({ key, value }));
}

/** Full backup: the loaded list, the tracking, the configuration. */
export async function exportAll(): Promise<Backup> {
  const db = await open();
  const [mayors, tracking, settings] = await Promise.all([
    tx<Mayor[]>(db, "mayors", "readonly", (s) => s.getAll()),
    tx<Tracking[]>(db, "tracking", "readonly", (s) => s.getAll()),
    tx<Setting[]>(db, "settings", "readonly", (s) => s.getAll()),
  ]);
  return {
    format: "paraphe/1",
    exported_at: new Date().toISOString(),
    mayors: mayors ?? [],
    tracking: tracking ?? [],
    settings: settings ?? [],
  };
}

export interface ImportReport {
  mayors: number;
  tracking: number;
  skipped: number;
  settings: number;
  /** Settings the file carried and the merge deliberately did not take. */
  keptSettings?: number;
}

/**
 * A logo this mode may hold: inline bytes, or nothing. Browser mode stores
 * only data URIs — a remote address here would be fetched on every render.
 */
export const usableLogo = (v: unknown): v is string =>
  v === "" || (typeof v === "string" && v.startsWith("data:image/"));

/** A usable tracking entry: without this, the card renders a white screen. */
function validTracking(e: unknown): e is Tracking {
  const t = e as Tracking;
  return (
    !!t &&
    typeof t.insee_code === "string" &&
    t.insee_code !== "" &&
    typeof t.status === "string" &&
    Array.isArray(t.notes)
  );
}

/**
 * Restores a backup. `merge` keeps the existing tracking and only adds
 * what is missing: that is what is wanted when two volunteers exchange
 * their files, rather than crushing one's work.
 */
export async function importAll(
  data: Backup,
  { merge = false } = {},
): Promise<ImportReport> {
  if (data?.format !== "paraphe/1") {
    throw new Error("Fichier non reconnu : ce n'est pas un export paraphe.");
  }
  const db = await open();
  const report: ImportReport = {
    mayors: 0,
    tracking: 0,
    skipped: 0,
    settings: 0,
  };

  // Everything is validated BEFORE entering the transaction: a malformed
  // entry making a `put` throw mid-way left the store emptied and the work
  // lost, with a message suggesting nothing had moved.
  const mayors = (data.mayors ?? []).filter(
    (m) => typeof m?.insee_code === "string" && m.insee_code !== "",
  );
  const trackings = (data.tracking ?? []).filter(validTracking);
  report.skipped +=
    (data.mayors ?? []).length -
    mayors.length +
    (data.tracking ?? []).length -
    trackings.length;

  if (mayors.length) {
    const t = db.transaction("mayors", "readwrite");
    const s = t.objectStore("mayors");
    if (!merge) s.clear();
    for (const m of mayors) s.put(m);
    await settle(t);
    report.mayors = mayors.length;
  }

  const existing = merge ? await loadTracking() : {};
  const t = db.transaction("tracking", "readwrite");
  const s = t.objectStore("tracking");
  if (!merge) s.clear();
  for (const e of trackings) {
    const already = existing[e.insee_code];
    if (already && (already.updated_at ?? "") >= (e.updated_at ?? "")) {
      report.skipped++;
      continue;
    }
    s.put(e);
    report.tracking++;
  }
  await settle(t);

  // Merging protects the settings you HOLD, key by key. "Fusionner"
  // promises to keep your work — silently replacing the candidate, the
  // contacts and the signature made the next emails go out in the name of
  // the colleague whose file had just been opened.
  //
  // Per key, not all-or-nothing: the first visit downloads a list on its
  // own and records which one, so the store is never empty afterwards. An
  // all-or-nothing guard therefore never fired on the application's own
  // end-of-campaign procedure — erase, reload, restore — and the campaign
  // came back at its template values with the report saying nothing.
  const current = await tx<Setting[]>(db, "settings", "readonly", (s) =>
    s.getAll(),
  );
  const held = new Set((current ?? []).map((s) => s.key));
  for (const r of data.settings ?? []) {
    // The logo is the one setting that becomes an <img src>, so it is the
    // one a backup file can turn into a beacon: an imported
    // `https://ailleurs.example/pixel.gif?qui=…` is fetched on every page
    // load, for ever, and the GUIDE tells volunteers to exchange these
    // files. A data: URI is the only shape this mode ever writes, and the
    // only one that keeps « aucune donnée ne quitte ce navigateur » true —
    // published on GitHub Pages there is no Content-Security-Policy to
    // catch it.
    if (r.key === "logo" && !usableLogo(r.value)) {
      report.skipped++;
      continue;
    }
    if (!merge || !held.has(r.key)) {
      await writeSetting(r.key, r.value);
      report.settings++;
    } else {
      report.keptSettings = (report.keptSettings ?? 0) + 1;
    }
  }
  return report;
}

export async function eraseAll(): Promise<void> {
  const db = await open();
  for (const m of ["mayors", "tracking", "settings"] as Store[]) {
    await tx(db, m, "readwrite", (s) => s.clear());
  }
}

/**
 * Reads a ";" CSV (the one `task build` produces).
 *
 * The columns are checked, as the API checks them on import. Browser mode
 * has no server to catch a renamed column: every English key would resolve
 * to undefined, rank() would take its absent-column branch — which means
 * "they endorsed" — and only the required-field guard would stand between
 * that and a mass false claim.
 */
// `rank` is NOT among them: the priority list has no rank column — it
// contains endorsers and nothing else, which is exactly what rank()'s
// absent-column fallback is for.
const REQUIRED_COLUMNS = [
  "insee_code",
  "first_name",
  "last_name",
  "commune",
  "department",
];

export function parseCsv(text: string): Mayor[] {
  const rows = parseRecords(text);
  const missing = REQUIRED_COLUMNS.filter((c) => !(c in (rows[0] ?? {})));
  if (missing.length) {
    throw new Error(
      `colonnes absentes : ${missing.join(", ")} — ce fichier ne vient pas ` +
        "de `task build`, ou son format a changé.",
    );
  }
  return rows;
}
