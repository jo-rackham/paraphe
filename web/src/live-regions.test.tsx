// The live-region doctrine (CLAUDE.md): a region PRE-EXISTS, only its text
// changes, and it holds no interactive control. These tests pin the part
// assistive technology actually depends on: the region must already be in
// the DOM during the loading state — a region mounted together with its
// text, which is what a `!ready` early-return produces, is announced by
// some screen readers and silently dropped by others.
//
// Each test here was written RED against the early-return shells, and is
// the non-regression for the round-2 adversarial finding that proved the
// download announcer entered the DOM already populated on first launch.

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CAMPAIGN_KEYS } from "../../noyau/messages.ts";
import App from "./App.tsx";
import * as API from "./api.ts";
import Browser from "./Browser.tsx";
import { resetViewMemory } from "./common.tsx";
import * as DB from "./db.ts";
import Team from "./Team.tsx";
import type { ServerConfig } from "./types.ts";

vi.mock("./api.ts", { spy: true });
vi.mock("./db.ts", { spy: true });

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

const CONFIG: ServerConfig = {
  mode: "team",
  campaign: Object.fromEntries(CAMPAIGN_KEYS.map((k) => [k, `valeur ${k}`])),
  batch_size: 10,
  unfilled: [],
  source_url: "",
  logo: null,
  statuses: [{ key: "to_contact", label: "À contacter", colour: "#eee" }],
  ranks: [{ key: "has_endorsed", label: "A parrainé" }],
};

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  resetViewMemory();
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

// no restoreAllMocks here: under `spy: true` it strips the spy wrappers,
// and every vi.mocked(...) of the NEXT test throws. The ...Once overrides
// used below clean themselves up.
afterEach(async () => {
  await act(async () => {
    root.unmount();
  });
  container.remove();
});

const flush = () =>
  act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });

/** A promise that never settles: freezes the component in its loading state. */
const pending = () => new Promise<never>(() => {});

describe("live regions pre-exist their first message", () => {
  it("Browser: the alert region and the download announcer are in the DOM while loading", async () => {
    vi.mocked(DB.loadMayors).mockReturnValueOnce(pending());
    await act(async () => {
      root.render(<Browser />);
    });
    await flush();

    // still loading — the shell must already carry the live regions
    expect(container.textContent).toContain("Chargement…");
    expect(container.querySelector('[role="alert"]')).not.toBeNull();
    expect(
      container.querySelectorAll('span[role="status"]').length,
    ).toBeGreaterThan(0);
  });

  it("Team: the alert region is in the DOM while loading", async () => {
    vi.mocked(API.me).mockReturnValueOnce(pending());
    await act(async () => {
      root.render(<Team config={CONFIG} />);
    });
    await flush();

    expect(container.textContent).toContain("Chargement…");
    expect(container.querySelector('[role="alert"]')).not.toBeNull();
  });

  it("App: the outage message lands as a text change in a pre-existing region", async () => {
    let resolve!: (m: Awaited<ReturnType<typeof API.detectMode>>) => void;
    vi.mocked(API.detectMode).mockReturnValueOnce(
      new Promise((r) => {
        resolve = r;
      }),
    );
    await act(async () => {
      root.render(<App />);
    });
    await flush();

    // loading: the region exists, empty
    const region = container.querySelector('[role="alert"]');
    expect(region).not.toBeNull();
    expect(region?.textContent).toBe("");

    await act(async () => {
      resolve({ kind: "outage", message: "panne simulée" });
    });
    await flush();

    // outage: the SAME node carries the message
    expect(container.querySelector('[role="alert"]')?.textContent).toContain(
      "panne simulée",
    );
    expect(container.textContent).toContain("Serveur injoignable");
  });

  it("Browser: the result counter is ONE node, not a visible line plus a mirror", async () => {
    // a stored mayor makes the list — and its counter — render
    await DB.replaceMayors([
      {
        insee_code: "90001",
        commune: "Bourg-Réel",
        department: "90",
        last_name: "MARTIN",
        first_name: "Camille",
        title: "Mme",
        rank: "has_endorsed",
        score: "3",
        recent_candidate: "Camille Réel",
        recent_year: "2022",
      },
    ]);
    await act(async () => {
      root.render(<Browser />);
    });
    await flush();
    // past the announcement debounce: a lagging mirror is exactly the
    // two-node state this test refuses
    await act(async () => {
      await new Promise((r) => setTimeout(r, 700));
    });

    const matches = (container.textContent ?? "").match(/affiché\(s\) sur/g);
    expect(matches).not.toBeNull();
    // read twice at the virtual cursor — and the two disagreed while the
    // mirror lagged behind the debounce — when this was two nodes
    expect(matches?.length).toBe(1);
  });
});
