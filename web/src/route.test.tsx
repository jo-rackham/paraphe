// The address bar names the screen — and « précédent » is the assertion.
//
// Before this, a volunteer's back gesture left the application entirely: on a
// phone, in the middle of a card, with a rewritten email and a half-typed
// call note behind it. These pin the three things that buys, and the two
// places it must NOT reach.

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as API from "./api.ts";
import Instance from "./Instance.tsx";
import { href, navigate, segmentsOf } from "./route.tsx";
import Team from "./Team.tsx";
import { instanceConfig, teamConfig, who } from "./testing/fixtures.ts";

vi.mock("./api.ts", { spy: true });

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.resetAllMocks();
});

const text = () => host.textContent ?? "";

/** What the browser does on « précédent »: pop, then tell the page. */
async function back() {
  await act(async () => {
    window.history.back();
    // jsdom traverses on a queued task and fires popstate itself; waiting
    // for it is what makes this « précédent » and not a simulation of one
    await new Promise((r) => setTimeout(r, 20));
  });
}

describe("the address bar", () => {
  it("reads and writes relative to the deployment's base", () => {
    // The application can live under a sub-path (PARAPHE_BASE_PATH), and a
    // router that ignored it would route on the deployment's own prefix.
    const base = import.meta.env.BASE_URL || "/";
    expect(segmentsOf(`${base}maires/01001`)).toEqual(["maires", "01001"]);
    expect(segmentsOf(base)).toEqual([]);
    expect(href(["maires", "01001"])).toBe(`${base}maires/01001`);
    // href encodes, so segmentsOf decodes: a pipeline that writes one way
    // and reads another matches no view and falls back with nothing said
    expect(segmentsOf(href(["une vue", "01001"]))).toEqual([
      "une vue",
      "01001",
    ]);
  });

  it("adds one history entry per move, and replace adds none", () => {
    const before = window.history.length;
    act(() => navigate(["maires"]));
    expect(window.location.pathname).toBe(href(["maires"]));
    expect(window.history.length).toBe(before + 1);
    // REPLACE is what a redirect does — a spent sign-in link, a session that
    // died, a card that was refused. « précédent » must not walk back onto
    // any of those.
    act(() => navigate(["guide"], { replace: true }));
    expect(window.location.pathname).toBe(href(["guide"]));
    expect(window.history.length).toBe(before + 1);
  });

  it("scrubs the fragment even when the view does not change", () => {
    // The commonest move there is: tapping the tab you are already on,
    // signing out while at home. The early exit read « same path, nothing to
    // do » and skipped the write — and the write is what strips the token's
    // fragment. So the second lock was off in precisely the case nobody
    // thinks to test.
    const before = window.history.length;
    window.history.replaceState(null, "", `${href([])}#jeton=abc`);
    act(() => navigate([]));
    expect(
      window.location.hash,
      "going to the view already on screen left the token in the address bar",
    ).toBe("");
    // scrubbed in place: the view did not change, so neither did the history
    expect(window.history.length).toBe(before);

    window.history.replaceState(null, "", `${href(["maires"])}?org=candidat`);
    act(() => navigate(["maires"]));
    expect(window.location.search).toBe("");
  });

  it("carries no fragment into a new entry, so no token can ride one", () => {
    // The fragment is the sign-in link's: `main.tsx` empties it before the
    // first render. This is the second lock on the same door — whatever is
    // in there when a view changes does NOT reach the new history entry, so
    // a token cannot end up in the back button, in a bookmark, or in a URL
    // pasted into a support thread.
    window.history.replaceState(null, "", `${href([])}#jeton=abc`);
    act(() => navigate(["maires"]));
    expect(window.location.hash).toBe("");
    expect(window.location.href).not.toContain("jeton");
  });
});

describe("team mode", () => {
  beforeEach(() => {
    vi.mocked(API.me).mockResolvedValue(who("moi@exemple.fr", "Moi"));
    vi.mocked(API.consumeLinkToken).mockReturnValue(null);
    vi.mocked(API.card).mockResolvedValue({
      mayor: {
        insee_code: "01001",
        commune: "Saint-Marcel",
        department: "Ain",
        last_name: "MARTIN",
        first_name: "Camille",
        title: "Mme",
      },
      notes: [],
      messages: { email: "", letter: "", phone: "" },
    } as never);
  });

  it("opens the card the address names, on a cold load", async () => {
    // A link somebody shared, opened by a volunteer who was nowhere near the
    // list. This is what the removal of the team wall made worth having: no
    // card of a campaign is refused to a team of it, so the link works for
    // whoever receives it.
    window.history.replaceState(null, "", href(["maires", "01001"]));
    await act(async () => {
      root.render(<Team config={teamConfig()} />);
    });
    await act(async () => {});
    expect(vi.mocked(API.card).mock.calls[0]?.[0]).toBe("01001");
    expect(text()).toContain("Saint-Marcel");
  });

  it("goes back from a card to the list, not out of the application", async () => {
    window.history.replaceState(null, "", href(["maires"]));
    await act(async () => {
      root.render(<Team config={teamConfig()} />);
    });
    await act(async () => {});
    act(() => navigate(["maires", "01001"]));
    await act(async () => {});
    expect(text()).toContain("Saint-Marcel");

    await back();
    expect(window.location.pathname).toBe(href(["maires"]));
    expect(
      text(),
      "« précédent » from a card left the card on screen: the history moved " +
        "and the application did not follow",
    ).not.toContain("Saint-Marcel");
  });

  it("never shows one card under another's address", async () => {
    // Card to card — which is what « précédent » and « suivant » do between
    // two of them, and what a shared link clicked from a card does. The
    // previous card stayed on screen while the next was in flight, so every
    // control on it, « Enregistrer » included, was wired to the WRONG INSEE:
    // a status against the wrong mayor, in a base the whole campaign reads.
    let releaseB: ((c: unknown) => void) | null = null;
    vi.mocked(API.card).mockImplementation((insee: string) => {
      if (insee === "01001") {
        return Promise.resolve({
          mayor: { insee_code: "01001", commune: "Saint-Marcel" },
          notes: [],
          messages: { email: "", letter: "", phone: "" },
        } as never);
      }
      return new Promise((r) => {
        releaseB = r as (c: unknown) => void;
      });
    });

    window.history.replaceState(null, "", href(["maires", "01001"]));
    await act(async () => {
      root.render(<Team config={teamConfig()} />);
    });
    await act(async () => {});
    expect(text()).toContain("Saint-Marcel");

    // straight to the second card; its answer has not arrived
    act(() => navigate(["maires", "02002"]));
    await act(async () => {});
    expect(
      text(),
      "the first card is still on screen under the second one's address: a " +
        "status written here lands on the mayor the visitor is no longer " +
        "looking at",
    ).not.toContain("Saint-Marcel");

    // and it does arrive
    await act(async () => {
      releaseB?.({
        mayor: { insee_code: "02002", commune: "Voncourt" },
        notes: [],
        messages: { email: "", letter: "", phone: "" },
      });
    });
    expect(text()).toContain("Voncourt");
  });

  it("fetches a card once, whatever re-renders", async () => {
    // The loader is keyed on the INSEE and holds it in a ref: a status write
    // replaces the card in place, and that must not read as a new card to
    // fetch — nor must React's double effect in development.
    window.history.replaceState(null, "", href(["maires", "01001"]));
    await act(async () => {
      root.render(<Team config={teamConfig()} />);
    });
    await act(async () => {});
    await act(async () => {});
    expect(vi.mocked(API.card).mock.calls.length).toBe(1);
  });
});

describe("the apex", () => {
  it("opens the hosting form the address names", async () => {
    // a plain Error, not APIError: the module is spy-mocked, so its class
    // cannot be constructed here — and this path only needs a rejection
    vi.mocked(API.me).mockRejectedValue(new Error("non connecté"));
    vi.mocked(API.consumeLinkToken).mockReturnValue(null);
    vi.mocked(API.publicCampaigns).mockResolvedValue({
      campaigns: [],
      base_domain: "paraphe.test",
    } as never);
    window.history.replaceState(null, "", href(["demande"]));
    await act(async () => {
      root.render(<Instance config={instanceConfig()} />);
    });
    await act(async () => {});
    expect(text()).toContain("Héberger une campagne");
  });
});
