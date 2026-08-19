// CSV reading and writing in the format the rest of the project produces
// and expects: ";" separator, UTF-8 with BOM, CRLF line endings.
//
// None of these three choices is cosmetic. The BOM and the semicolon make
// Excel and LibreOffice open the file correctly in French; the CRLF is
// that of Python's csv module (excel dialect), which keeps the outputs
// byte-comparable with a reference run — the proof CLAUDE.md requires
// before touching the crossing.

export const BOM = "﻿";

/** Splits a CSV into rows of fields. Doubled quotes per RFC 4180. */
export function parseRows(text: string, separator = ";"): string[][] {
  const source = text.startsWith(BOM) ? text.slice(1) : text;
  const rows: string[][] = [];
  let fields: string[] = [];
  let current = "";
  let inQuotes = false;
  let started = false; // row under way: tells "" apart from end of file

  for (let i = 0; i < source.length; i++) {
    const c = source[i];
    if (inQuotes) {
      if (c === '"') {
        if (source[i + 1] === '"') {
          current += '"';
          i++;
        } else inQuotes = false;
      } else current += c;
      continue;
    }
    if (c === '"' && current === "") {
      inQuotes = true;
      started = true;
      continue;
    }
    if (c === separator) {
      fields.push(current);
      current = "";
      started = true;
      continue;
    }
    if (c === "\r" || c === "\n") {
      if (c === "\r" && source[i + 1] === "\n") i++;
      if (started || current !== "" || fields.length) {
        fields.push(current);
        rows.push(fields);
      }
      fields = [];
      current = "";
      started = false;
      continue;
    }
    current += c;
    started = true;
  }
  if (started || current !== "" || fields.length) {
    fields.push(current);
    rows.push(fields);
  }
  return rows;
}

export type Row = Record<string, string>;

/** Rows indexed by the header, like `csv.DictReader`. */
export function parseRecords(text: string, separator = ";"): Row[] {
  const rows = parseRows(text, separator);
  if (!rows.length) return [];
  const header = rows[0];
  return rows.slice(1).map((fields) => {
    const o: Row = {};
    header.forEach((name, i) => {
      o[name] = fields[i] ?? "";
    });
    return o;
  });
}

/**
 * NO APOSTROPHE IS PREFIXED HERE, and that is a decision rather than an
 * oversight — it is the first thing anyone reaching for « CSV injection »
 * will want to add.
 *
 * Ten cells of the generated files start with « + »: the Polynesian and New
 * Caledonian town halls, whose directory numbers read « +689 40 92 92 19 ».
 * Excel parses a leading `+` as a formula and shows `#NAME?`, so two mayors of
 * file 01 and eight of file 04 display wrong in a spreadsheet.
 *
 * The standard remedy makes it WORSE here. These files are not a report, they
 * are an interchange format with three machine readers — the Go import that
 * fills `mayors` at startup, the account-less version that loads them off
 * `web/public/donnees/`, and the mass mailing. An apostrophe written here
 * reaches all three: the database, and every card a volunteer opens, would
 * carry « '+689 40 92 92 19 ». A visible `#NAME?` in one program is a cheaper
 * failure than a wrong telephone number shown as if it were right — and the
 * one job file 01 exists for, the mail merge, reads the CSV and is unaffected.
 *
 * Quoting is not an answer either: Excel evaluates a quoted field the same
 * way, and this end cannot verify that claim by running Excel, which is
 * reason enough not to ship a fix resting on it.
 *
 * What would change the decision: a value starting with `=` or `@` appearing
 * in the sources. Nothing does today (measured over both files) — those come
 * from a public government directory, and a hostile one there would be worth
 * refusing at the door rather than escaping at every writer.
 */
function escapeField(value: unknown, separator: string): string {
  const s = value === null || value === undefined ? "" : String(value);
  // Python's QUOTE_MINIMAL: separator, quote or line ending
  if (
    s.includes(separator) ||
    s.includes('"') ||
    s.includes("\r") ||
    s.includes("\n")
  ) {
    return `"${s.replaceAll('"', '""')}"`;
  }
  return s;
}

/**
 * Serialises records to CSV. `columns` fixes the order: deriving it from
 * the first record would make the output depend on key insertion order.
 */
export function writeCsv(
  columns: string[],
  rows: Record<string, unknown>[],
  separator = ";",
): string {
  const out = [columns.map((c) => escapeField(c, separator)).join(separator)];
  for (const r of rows) {
    out.push(columns.map((c) => escapeField(r[c], separator)).join(separator));
  }
  return BOM + out.join("\r\n") + "\r\n";
}
