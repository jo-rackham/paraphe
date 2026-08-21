import { useCallback, useSyncExternalStore } from "react";

/**
 * The address bar as the source of truth for WHICH SCREEN is open.
 *
 * Written by hand, on the History API, and that is a decision rather than an
 * omission: `web/` has three dependencies, this application has five views
 * per mode and one card, and a routing library brings twenty transitive
 * packages for nested routes, loaders and data APIs that nothing here uses.
 * The same reasoning that hand-wrote the S3 client and ported `difflib`.
 *
 * THE PATH, NEVER THE FRAGMENT. `#` belongs to the sign-in link: a token
 * arrives in it, `main.tsx` takes it out before the first render, and a
 * router writing there would put a history entry — and a « précédent » — in
 * the middle of that. The path costs nothing extra: the server already
 * serves `index.html` for every extension-less path, because the application
 * has always been a single page.
 *
 * Everything is relative to Vite's base (`PARAPHE_BASE_PATH`, `/paraphe/` by
 * default), so a deployment under a sub-path routes like any other.
 */

// A store, and `useSyncExternalStore` to read it: `popstate` fires outside
// React, and a plain useState+useEffect pair tears under concurrent
// rendering — two components can read two different locations in one paint.
const listeners = new Set<() => void>();

function subscribe(onChange: () => void): () => void {
  listeners.add(onChange);
  window.addEventListener("popstate", onChange);
  return () => {
    listeners.delete(onChange);
    window.removeEventListener("popstate", onChange);
  };
}

// A STRING, not the parsed segments: `useSyncExternalStore` compares
// snapshots by identity, and a fresh array every call is a fresh identity
// every call — an infinite re-render, and React says so at runtime.
const snapshot = () => window.location.pathname;

// The server renders no HTML, so there is no server snapshot to reconcile;
// tests that render without a DOM would still call this.
const serverSnapshot = () => base();

function base(): string {
  return import.meta.env.BASE_URL || "/";
}

/** The path after the base, as segments. `/paraphe/maires/01001` → ["maires","01001"]. */
export function segmentsOf(pathname: string): string[] {
  const b = base();
  const rest = pathname.startsWith(b) ? pathname.slice(b.length) : pathname;
  // DECODED, because `href` encodes. Today every segment is a known view or
  // an INSEE code, all plain ASCII, so the asymmetry shows nowhere — and
  // that is exactly how it survives until the first view whose name carries
  // a space or an accent, which then matches no known view and falls back to
  // the home with nothing said. The same family as comparing a scope
  // unnormalised: a pipeline that writes one way and reads another.
  return rest
    .split("/")
    .filter((s) => s !== "")
    .map((s) => {
      try {
        return decodeURIComponent(s);
      } catch {
        // a hand-typed `%` is not an escape and not a crash either: the
        // segment is whatever it literally says, and matches no view
        return s;
      }
    });
}

/** Where a set of segments lives, base included. */
export function href(segments: string[]): string {
  return base() + segments.map(encodeURIComponent).join("/");
}

/**
 * Go there. `replace` overwrites the current entry instead of adding one —
 * what a redirect does, and what a REDEEMED sign-in link must do: pressing
 * « précédent » onto a spent token is a dead end the visitor did not choose.
 */
export function navigate(segments: string[], opts?: { replace?: boolean }) {
  const to = href(segments);
  const from = window.location.pathname;
  // A FRAGMENT OR A QUERY IS NOT NOTHING TO DO. The early exit used to read
  // « same path, nothing to do » and skipped the write — but the write is
  // what strips them, and that stripping is the second lock on the sign-in
  // token. Going to the view you are already on is the commonest move there
  // is (tapping the tab you are on, signing out at home), and every one of
  // them left a `#jeton=…` or a `?org=…` standing. So: same path and a
  // clean URL is the only true no-op; same path and a dirty one is scrubbed
  // in place, with no entry added and no listener woken, because the view
  // did not change.
  const dirty = window.location.search !== "" || window.location.hash !== "";
  if (to === from && !dirty) return;
  if (opts?.replace || to === from) {
    window.history.replaceState(null, "", to);
  } else {
    window.history.pushState(null, "", to);
  }
  // pushState fires no event: the listeners are ours to call.
  if (to !== from) {
    for (const fn of [...listeners]) fn();
  }
}

/** The current segments, re-read on every history move. */
export function useRoute(): string[] {
  const pathname = useSyncExternalStore(subscribe, snapshot, serverSnapshot);
  return segmentsOf(pathname);
}

/**
 * A view name and the card under it, for the modes that have both.
 *
 * `fallback` is what an empty path means, and what an UNKNOWN one means too:
 * a screen this build does not have — an old link, a typo, a mode that does
 * not carry that view — lands on the mode's home rather than on a blank
 * page. `known` is the mode's own list, so the same URL can be a real view
 * in one mode and nonsense in another, which is exactly what three modes on
 * one bundle means.
 */
export function useView(
  known: readonly string[],
  home: string,
): {
  view: string;
  card: string | null;
  go: (view: string) => void;
  hrefOf: (view: string) => string;
} {
  const segments = useRoute();
  const first = segments[0] ?? "";
  const view = known.includes(first) ? first : home;
  // `go` lives HERE, beside the fallback it mirrors: ONE address per screen
  // means the home is the base and not a segment beside it, and the two
  // halves of that rule — what an empty path reads as, and what going home
  // writes — were a copy each in the three modes. Two URLs rendering one
  // view make a « précédent » that appears to do nothing, and nothing would
  // have gone red the day one copy moved.
  const go = useCallback(
    (to: string) => navigate(to === home ? [] : [to]),
    [home],
  );
  // `hrefOf` is the SAME rule read instead of written — the address a nav
  // link carries, so a modified click (new tab) and a plain click (view
  // change) open the same screen. A second copy of the home rule in the
  // nav is how the two would drift.
  const hrefOf = useCallback(
    (to: string) => href(to === home ? [] : [to]),
    [home],
  );
  return { view, card: segments[1] ?? null, go, hrefOf };
}
