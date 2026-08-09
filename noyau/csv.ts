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
        if (source[i + 1] === '"') { current += '"'; i++; } else inQuotes = false;
      } else current += c;
      continue;
    }
    if (c === '"' && current === "") { inQuotes = true; started = true; continue; }
    if (c === separator) { fields.push(current); current = ""; started = true; continue; }
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
    header.forEach((name, i) => { o[name] = fields[i] ?? ""; });
    return o;
  });
}

function escapeField(value: unknown, separator: string): string {
  const s = value === null || value === undefined ? "" : String(value);
  // Python's QUOTE_MINIMAL: separator, quote or line ending
  if (s.includes(separator) || s.includes('"') || s.includes("\r") || s.includes("\n")) {
    return `"${s.replaceAll('"', '""')}"`;
  }
  return s;
}

/**
 * Serialises records to CSV. `columns` fixes the order: deriving it from
 * the first record would make the output depend on key insertion order.
 */
export function writeCsv(
  columns: string[], rows: Record<string, unknown>[], separator = ";",
): string {
  const out = [columns.map((c) => escapeField(c, separator)).join(separator)];
  for (const r of rows) {
    out.push(columns.map((c) => escapeField(r[c], separator)).join(separator));
  }
  return BOM + out.join("\r\n") + "\r\n";
}
