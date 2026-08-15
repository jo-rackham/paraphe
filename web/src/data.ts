// Loading of the lists published next to the application.
//
// Two files: the priority list (139 KB gzipped, loaded by default) and the
// full base (2.1 MB gzipped, on demand). Hence the chunked reader: on a
// slow connection, 2 MB without visual feedback feels like the tool
// crashed.
//
// These files come from the public repository; this is the application's
// only network request, and it transmits nothing — no parameter, no
// cookie, no identifier.

import { parseCsv } from "./db.ts";
import type { Mayor } from "./types.ts";

export type ListKey = "light" | "complete";

export const LISTS: Record<
  ListKey,
  { file: string; name: string; detail: string }
> = {
  light: {
    file: "01_maires_cibles_prioritaires.csv",
    name: "liste prioritaire",
    detail: "les maires qui ont déjà parrainé une candidature peu médiatisée",
  },
  complete: {
    file: "04_base_complete.csv",
    name: "base complète",
    detail: "tous les maires de France, classés par signal",
  },
};

/** URL of a list, relative to the publication base (Vite `base`). */
const url = (key: ListKey) =>
  `${import.meta.env.BASE_URL}donnees/${LISTS[key].file}`;

/**
 * Downloads and parses a list.
 * `onProgress({received, total})` is called during the transfer; `total`
 * is 0 when the server sends no Content-Length (common behind on-the-fly
 * compression).
 */
export interface Progress {
  received: number;
  total: number;
}

export async function loadList(
  key: ListKey,
  onProgress: (p: Progress) => void = () => {},
): Promise<Mayor[]> {
  const response = await fetch(url(key), { cache: "no-store" });
  if (!response.ok) {
    throw new Error(
      `liste « ${LISTS[key].name} » indisponible (HTTP ${response.status}). ` +
        "Sur une installation locale, lancez `task web-donnees`.",
    );
  }
  const total = Number(response.headers.get("content-length")) || 0;
  const reader = response.body?.getReader();

  // no stream (old browser, test environment): direct read
  if (!reader) {
    const text = await response.text();
    onProgress({ received: text.length, total: text.length });
    return parseCsv(text);
  }

  const chunks = [];
  let received = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    chunks.push(value);
    received += value.length;
    onProgress({ received, total });
  }
  const bytes = new Uint8Array(received);
  let pos = 0;
  for (const c of chunks) {
    bytes.set(c, pos);
    pos += c.length;
  }
  return parseCsv(new TextDecoder("utf-8").decode(bytes));
}

export function formatBytes(n: number): string {
  if (!n) return "";
  return n > 1e6 ? `${(n / 1e6).toFixed(1)} Mo` : `${Math.round(n / 1e3)} Ko`;
}
