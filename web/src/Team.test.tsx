// Team mode's draft store, tested through the RENDERED component.
//
// The store keeps unsent card work — a rewritten email, a note being
// typed during a call — across the tab clicks that unmount the card. Its
// three guards are about NOT keeping it: on sign-out, on a different
// account signing in, and on the leave-page dialog. Unwiring all three
// left the whole suite green, which is why this file exists.
//
// The API is mocked here on purpose: these are decisions the interface
// makes on its own, and the e2e journeys already prove the wiring against
// a real server.

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import Team from "./Team.tsx";
import * as API from "./api.ts";
import { CAMPAIGN_KEYS } from "../../noyau/messages.ts";
import type { Me, ServerConfig } from "./types.ts";

vi.mock("./api.ts", { spy: true });

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

const CAMPAIGN = Object.fromEntries(
  CAMPAIGN_KEYS.map((k) => [k, `valeur de ${k}`]));

const CONFIG: ServerConfig = {
  mode: "team", campaign: CAMPAIGN, batch_size: 10, unfilled: [],
  source_url: "", no_account: false,
  statuses: [{ key: "to_contact", label: "À contacter", colour: "#eee" }],
  ranks: [{ key: "has_endorsed", label: "A parrainé" }],
};

const MAYOR = {
  insee_code: "90001", commune: "Bourg-Réel", department: "90",
  last_name: "MARTIN", first_name: "Camille", title: "Mme",
  rank: "has_endorsed", score: "3", recent_candidate: "Camille Réel",
  recent_year: "2022", democratic_theme_endorsement: "",
  email: "mairie@exemple.fr", status: "to_contact",
};

const who = (email: string, name: string): Me => ({
  account: {
    email, name, role: "volunteer", team_id: null, active: true,
    personal_note: "", team_name: null,
  },
  departments: ["90"], may_manage: false,
});

const ALICE = who("alice@exemple.fr", "Alice Bénévole");
// same display name on purpose: the only barrier left when the email is
// not compared is `signer`, a free field — and this project's thesis is
// that names repeat
const BRUNO = who("bruno@exemple.fr", "Alice Bénévole");

let container: HTMLDivElement;
let root: Root;

const flush = () => act(async () => { await new Promise((r) => setTimeout(r, 0)); });
const text = () => container.textContent ?? "";

async function until(pred: () => boolean, what: string) {
  for (let i = 0; i < 50; i++) {
    if (pred()) return;
    await flush();
  }
  throw new Error(`never happened: ${what}`);
}

function button(label: string): HTMLButtonElement {
  const b = [...container.querySelectorAll("button")]
    .find((el) => el.textContent?.includes(label));
  if (!b) throw new Error(`no button « ${label} » on screen`);
  return b;
}

const click = (label: string) => act(async () => {
  button(label).dispatchEvent(new MouseEvent("click", { bubbles: true }));
});

function type(field: HTMLTextAreaElement, value: string) {
  const set = Object.getOwnPropertyDescriptor(
    window.HTMLTextAreaElement.prototype, "value")!.set!;
  return act(async () => {
    set.call(field, value);
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

const emailBody = () =>
  container.querySelector<HTMLTextAreaElement>("textarea[rows='16']")!;

const noteField = () => [...container.querySelectorAll("label")]
  .find((l) => l.textContent?.startsWith("Note"))!.querySelector("textarea")!;

/** Signs in through the form, the way the screen does. */
async function signIn(person: Me) {
  vi.mocked(API.signIn).mockResolvedValueOnce(person);
  const [email, password] = container.querySelectorAll("input");
  const set = Object.getOwnPropertyDescriptor(
    window.HTMLInputElement.prototype, "value")!.set!;
  await act(async () => {
    set.call(email, person.account.email);
    email.dispatchEvent(new Event("input", { bubbles: true }));
    set.call(password, "mot-de-passe");
    password.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await click("Se connecter");
  await until(() => text().includes("Mon tableau"), "the app opens");
}

/** Opens the one card, from the dashboard. */
async function openCard() {
  await click("Mon tableau");
  await until(() => text().includes("Bourg-Réel"), "the dashboard lists the card");
  await act(async () => {
    container.querySelector<HTMLButtonElement>("table button.lien")!
      .dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
  await until(() => !!emailBody(), "the card opens");
}

beforeEach(() => {
  vi.mocked(API.detectMode).mockResolvedValue({ kind: "team", config: CONFIG });
  vi.mocked(API.dashboard).mockResolvedValue({
    stats: {}, total: 1, departments_with_promise: [], departments_covered: 0,
    mine: [MAYOR], team: [], departments: ["90"],
    by_rank: { has_endorsed: 1 }, batch_size: 10,
  });
  vi.mocked(API.card).mockResolvedValue({ mayor: MAYOR, notes: [] });
  vi.mocked(API.signOut).mockResolvedValue(undefined);
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => { root.unmount(); });
  container.remove();
  // reset, not restore: `spy: true` mocks are removed by restoreAllMocks,
  // and the second test would then call the real API
  vi.resetAllMocks();
});

describe("unsent card work in team mode", () => {
  it("does not follow the computer to the next person", async () => {
    // Alice arrives by COOKIE — the scenario the guard exists for: a
    // shared computer, a session left open overnight. That path never
    // called onSignedIn, so the account was never recorded and the store
    // was never cleared.
    vi.mocked(API.me).mockResolvedValueOnce(ALICE);
    await act(async () => { root.render(<Team config={CONFIG} />); });
    await until(() => text().includes("Mon tableau"), "Alice's session is restored");
    await openCard();
    await type(emailBody(), "Texte d'Alice pour Mme MARTIN.");
    await type(noteField(), "sa fille est malade, ne pas insister avant lundi");

    // the session expires — no sign-out, which is exactly how a shared
    // computer changes hands
    await act(async () => {
      window.dispatchEvent(new Event(API.SESSION_LOST));
    });
    await until(() => text().includes("Se connecter"), "the session is gone");
    await signIn(BRUNO);
    await openCard();
    expect(emailBody().value,
      "Bruno must not inherit Alice's rewrite").not.toContain("Texte d'Alice");
    expect(noteField().value,
      "nor her private note about a named mayor's family").toBe("");
  });

  // Signing out is deliberate: the volunteer is leaving the machine. A
  // lost session is not — hence the two are treated differently.
  it("is discarded by an explicit sign-out, even for the same person",
    async () => {
      vi.mocked(API.me).mockResolvedValueOnce(ALICE);
      await act(async () => { root.render(<Team config={CONFIG} />); });
      await until(() => text().includes("Mon tableau"), "the session is restored");
      await openCard();
      await type(emailBody(), "Texte d'Alice pour Mme MARTIN.");

      await click("déconnexion");
      await until(() => text().includes("Se connecter"), "Alice is signed out");
      await signIn(ALICE);
      await openCard();
      expect(emailBody().value,
        "she left the computer: what she had not sent goes with her")
        .not.toContain("Texte d'Alice");
    });

  it("keeps it for the SAME person signing back in after a lost session",
    async () => {
      vi.mocked(API.me).mockResolvedValueOnce(ALICE);
      await act(async () => { root.render(<Team config={CONFIG} />); });
      await until(() => text().includes("Mon tableau"), "Alice's session is restored");
      await openCard();
      await type(emailBody(), "Texte d'Alice pour Mme MARTIN.");

      // the session expires under her, and she signs straight back in
      await act(async () => {
        window.dispatchEvent(new Event(API.SESSION_LOST));
      });
      await until(() => text().includes("Se connecter"), "the session is gone");
      await signIn(ALICE);
      await openCard();
      expect(emailBody().value,
        "her own work, lost to nothing but a cookie expiring")
        .toContain("Texte d'Alice");
    });

  it("arms the leave-page dialog, and only while the work still stands",
    async () => {
      vi.mocked(API.me).mockResolvedValueOnce(ALICE);
      await act(async () => { root.render(<Team config={CONFIG} />); });
      await until(() => text().includes("Mon tableau"), "the session is restored");
      const quiet = new Event("beforeunload", { cancelable: true });
      window.dispatchEvent(quiet);
      expect(quiet.defaultPrevented, "nothing typed yet").toBe(false);

      await openCard();
      await type(emailBody(), "Phrase écrite à la main.");
      const armed = new Event("beforeunload", { cancelable: true });
      window.dispatchEvent(armed);
      expect(armed.defaultPrevented,
        "a rewritten email addressed to a named mayor").toBe(true);
    });

  // The predicate is written twice, once per mode: a divergence in one of
  // them goes unnoticed unless both are held.
  it("arms it for a call note alone, with the email untouched", async () => {
    vi.mocked(API.me).mockResolvedValueOnce(ALICE);
    await act(async () => { root.render(<Team config={CONFIG} />); });
    await until(() => text().includes("Mon tableau"), "the session is restored");
    await openCard();
    await type(noteField(), "il rappelle jeudi");
    const armed = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(armed);
    expect(armed.defaultPrevented,
      "what was said on the phone exists nowhere else").toBe(true);
  });
});
