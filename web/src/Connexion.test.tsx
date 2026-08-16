// The sign-in screen's second path: a link by email.
//
// Four decisions live in the interface alone, and each of them is silent
// when it breaks — the screen still works, it just stops keeping a promise
// the server side cannot keep for it.
//
// The API is mocked: the e2e journey drives the real one.

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App.tsx";
import * as API from "./api.ts";
import { FormulaireConnexion } from "./Connexion.tsx";
import Team from "./Team.tsx";
import { teamConfig, who } from "./testing/fixtures.ts";

// The module is spied, so its exported CLASS is a spy too and cannot be
// `new`ed. This is the real one.
const { APIError: API_ERROR } =
  await vi.importActual<typeof import("./api.ts")>("./api.ts");

vi.mock("./api.ts", { spy: true });

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

const CONFIG = teamConfig({ magic_link: true });

let container: HTMLDivElement;
let root: Root;

const flush = () =>
  act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
const text = () => container.textContent ?? "";

function button(label: string): HTMLButtonElement | undefined {
  return [...container.querySelectorAll("button")].find((el) =>
    el.textContent?.includes(label),
  );
}

const click = (label: string) =>
  act(async () => {
    button(label)?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });

function typeInto(field: HTMLInputElement, value: string) {
  const set = Object.getOwnPropertyDescriptor(
    window.HTMLInputElement.prototype,
    "value",
  )!.set!;
  return act(async () => {
    set.call(field, value);
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

const emailField = () => container.querySelectorAll("input")[0];

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => {
    root.unmount();
  });
  container.remove();
  // reset, not restore: `spy: true` mocks are removed by restoreAllMocks,
  // and the next test would then call the real API
  vi.resetAllMocks();
  window.history.replaceState({}, "", "/");
});

const render = (magicLink: boolean) =>
  act(() => {
    root.render(
      <FormulaireConnexion magicLink={magicLink} onSignedIn={() => {}} />,
    );
  });

describe("signing in by email", () => {
  // A button that always refuses is worse than no button: the instance
  // answers 503 when no relay is configured, and the screen must not offer
  // the path at all.
  it("offers the link only where the server can send one", async () => {
    await render(false);
    expect(button("Recevoir un lien")).toBeUndefined();
    await render(true);
    expect(button("Recevoir un lien")).toBeDefined();
  });

  // The server's sentence, verbatim. It is the SAME whether or not an
  // account bears the address, and any rewording per case here — "envoyé !"
  // against "adresse inconnue" — turns this screen into a roster of the
  // campaign's volunteers.
  it("shows the server's answer word for word", async () => {
    const answer =
      "Si un compte existe à cette adresse, un lien de connexion vient " +
      "d'y être envoyé. Il est valable 15 minutes.";
    vi.mocked(API.requestLink).mockResolvedValue({ message: answer });
    await render(true);
    await typeInto(emailField(), "marie@exemple.fr");
    await click("Recevoir un lien");
    await flush();

    expect(API.requestLink).toHaveBeenCalledWith("marie@exemple.fr");
    expect(text()).toContain(answer);
  });

  // The link button is not a submit — a submit would validate the password
  // field and refuse to send a link to precisely the person who has none.
  it("asks for a link without a password", async () => {
    vi.mocked(API.requestLink).mockResolvedValue({ message: "envoyé" });
    await render(true);
    expect(button("Recevoir un lien")?.type).toBe("button");
    await typeInto(emailField(), "marie@exemple.fr");
    await click("Recevoir un lien");
    await flush();
    expect(API.requestLink).toHaveBeenCalled();
  });

  // Nothing is sent for an empty address, and the refusal is said on the
  // screen: the browser validates nothing on a non-submit button.
  //
  // And the SCREEN's own message goes, even on this path: pressing the
  // button is the new attempt whether or not it reaches the server. Cleared
  // only after the empty check, the screen's alert stayed up beside this
  // one — two live regions with contradictory text, in the very case a
  // reader is most likely to reach (a spent link, then the button pressed
  // before typing anything).
  it("says so instead of sending an empty address, and clears the screen", async () => {
    let cleared = false;
    await act(() => {
      root.render(
        <FormulaireConnexion
          magicLink
          onSignedIn={() => {}}
          onAttempt={() => {
            cleared = true;
          }}
        />,
      );
    });
    await click("Recevoir un lien");
    await flush();
    expect(API.requestLink).not.toHaveBeenCalled();
    expect(text()).toContain("Indiquez votre adresse email");
    expect(cleared).toBe(true);

    // and the form still works afterwards: the guard is ACQUIRED by the
    // check that opens the handler, so a branch returning without releasing
    // it would leave every later click refused — a screen answering nothing
    // to somebody who has just been told to type their address.
    vi.mocked(API.requestLink).mockResolvedValue({ message: "envoyé" });
    await typeInto(emailField(), "marie@exemple.fr");
    await click("Recevoir un lien");
    await flush();
    expect(API.requestLink).toHaveBeenCalledWith("marie@exemple.fr");
  });

  // `disabled` on the button holding the focus drops that focus to <body>,
  // in every browser. Both buttons of this form use aria-disabled and a
  // re-entry guard instead.
  //
  // And only the button that was PRESSED changes its label: one flag for
  // both made the submit button announce « Connexion… » the instant
  // somebody asked for a link, which reads as the form having misunderstood
  // which button they pressed.
  it("never takes the focus away from a busy button, nor mislabels the other", async () => {
    let release: (v: { message: string }) => void = () => {};
    vi.mocked(API.requestLink).mockReturnValue(
      new Promise((r) => {
        release = r;
      }),
    );
    await render(true);
    await typeInto(emailField(), "marie@exemple.fr");
    await click("Recevoir un lien");

    const link = container.querySelector<HTMLButtonElement>("button.lien")!;
    const submit = container.querySelector<HTMLButtonElement>(
      "button[type='submit']",
    )!;
    expect(link.disabled).toBe(false);
    expect(link.getAttribute("aria-disabled")).toBe("true");
    expect(link.textContent).toContain("Envoi…");
    // the submit is held too — one request at a time — but it does not
    // claim to be signing anybody in
    expect(submit.disabled).toBe(false);
    expect(submit.getAttribute("aria-disabled")).toBe("true");
    expect(submit.textContent).toBe("Se connecter");

    // and the guard holds: a second click while busy sends nothing more
    await act(async () => {
      link.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(API.requestLink).toHaveBeenCalledTimes(1);
    await act(async () => {
      release({ message: "envoyé" });
    });
  });

  // A DOUBLE-CLICK — both clicks in the same tick, which is what a
  // double-click is.
  //
  // The guard cannot be state: the two handlers come from the same render
  // and both read the flag as it was before either of them ran. Two
  // requests went out, which spends two of the three links an address is
  // allowed in a quarter of an hour — and the first one arrives DEAD,
  // because minting the second deleted it. The recipient clicks the older
  // mail and is told their link is no longer valid.
  it("sends one request for a double-click, not two", async () => {
    vi.mocked(API.requestLink).mockResolvedValue({ message: "envoyé" });
    await render(true);
    await typeInto(emailField(), "marie@exemple.fr");

    const link = container.querySelector<HTMLButtonElement>("button.lien")!;
    await act(async () => {
      link.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      link.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flush();
    expect(API.requestLink).toHaveBeenCalledTimes(1);
  });

  // The same, on the password path: two sign-ins in one tick are two
  // password derivations, and the ceiling is ten per quarter of an hour.
  it("signs in once for a double-submitted form", async () => {
    vi.mocked(API.signIn).mockResolvedValue(who("marie@exemple.fr", "Marie"));
    await render(true);
    await typeInto(emailField(), "marie@exemple.fr");
    const form = container.querySelector("form")!;
    await act(async () => {
      form.dispatchEvent(
        new Event("submit", { bubbles: true, cancelable: true }),
      );
      form.dispatchEvent(
        new Event("submit", { bubbles: true, cancelable: true }),
      );
    });
    await flush();
    expect(API.signIn).toHaveBeenCalledTimes(1);
  });

  // « un lien vient de partir » under a field that now shows another
  // address describes something that did not happen to the address being
  // read.
  it("drops the answer when the address changes under it", async () => {
    vi.mocked(API.requestLink).mockResolvedValue({
      message: "un lien est parti",
    });
    await render(true);
    await typeInto(emailField(), "marie@exemple.fr");
    await click("Recevoir un lien");
    await flush();
    expect(text()).toContain("un lien est parti");

    await typeInto(emailField(), "bruno@exemple.fr");
    expect(text()).not.toContain("un lien est parti");
  });

  // A REFUSAL stays, though. Clearing everything on a keystroke took « trop
  // de tentatives pour cette adresse » off the screen the moment its reader
  // started correcting what they took for a typo — and that ceiling is per
  // address, which is exactly what they were about to change.
  it("keeps a refusal while the address is being corrected", async () => {
    vi.mocked(API.requestLink).mockRejectedValue(
      new Error("Trop de tentatives. Réessayez dans 12 minutes."),
    );
    await render(true);
    await typeInto(emailField(), "marie@exemple.fr");
    await click("Recevoir un lien");
    await flush();
    expect(text()).toContain("Trop de tentatives");

    await typeInto(emailField(), "marie@exemple.f");
    expect(text()).toContain("Trop de tentatives");
  });
});

describe("landing on a sign-in link", () => {
  it("erases the token from the address bar before anything else", () => {
    window.history.replaceState({}, "", "/connexion#jeton=abc123");
    expect(API.takeLinkToken()).toBe("abc123");
    // A token opens ONE session: left in the URL, a reload would replay it
    // and tell somebody who has just used their link correctly that it is
    // no longer valid.
    expect(window.location.hash).toBe("");
    expect(API.takeLinkToken()).toBeNull();
  });

  // An empty value is not a token, but the KEY is still a credential-shaped
  // string sitting in the address bar and in the history entry.
  it("erases the marker even when it carries nothing", () => {
    window.history.replaceState({}, "", "/connexion#jeton=");
    expect(API.takeLinkToken()).toBeNull();
    expect(window.location.hash).toBe("");
  });

  // Nothing else uses the fragment today. Eating it silently is how the
  // first thing that does would be lost.
  it("puts back whatever else the fragment carried", () => {
    window.history.replaceState(
      {},
      "",
      "/connexion#jeton=abc123&onglet=equipe",
    );
    expect(API.takeLinkToken()).toBe("abc123");
    expect(window.location.hash).toBe("#onglet=equipe");
  });

  // A call that finds nothing must also LEAVE nothing: read once and kept,
  // the token of a first call is what a later screen would be handed.
  it("forgets the previous token when the next look finds none", () => {
    window.history.replaceState({}, "", "/connexion#jeton=abc123");
    API.takeLinkToken();
    expect(API.takeLinkToken()).toBeNull();
    expect(API.consumeLinkToken()).toBeNull();
  });

  // A history that refuses is what a sandboxed embed does. Letting it throw
  // stops main.tsx before createRoot — a blank page, with the link still
  // LIVE in the address bar that renders it.
  it("hands the token over even when the address bar refuses to be rewritten", () => {
    window.history.replaceState({}, "", "/connexion#jeton=abc123");
    const real = window.history.replaceState;
    window.history.replaceState = () => {
      throw new DOMException("blocked", "SecurityError");
    };
    try {
      expect(API.takeLinkToken()).toBe("abc123");
    } finally {
      window.history.replaceState = real;
    }
  });

  it("opens the session the link carries", async () => {
    window.history.replaceState({}, "", "/connexion#jeton=abc123");
    API.takeLinkToken(); // what main.tsx does, at boot
    const marie = who("marie@exemple.fr", "Marie Bénévole");
    vi.mocked(API.redeemLink).mockResolvedValue(marie);
    vi.mocked(API.detectMode).mockResolvedValue({
      kind: "team",
      config: CONFIG,
    });

    await act(() => {
      root.render(<Team config={CONFIG} />);
    });
    await flush();
    expect(API.redeemLink).toHaveBeenCalledWith("abc123");
    // and /api/me is not asked: the link IS the answer to "who are you"
    expect(API.me).not.toHaveBeenCalled();
    expect(text()).toContain("Marie Bénévole");
  });

  // A token that met the outage screen is DROPPED, not kept waiting.
  //
  // Alice opens her link, the campaign is down, she walks away and leaves
  // the tab on the table. Bruno sits down, presses « Réessayer », the
  // campaign answers this time — and the screen that finally mounts opened
  // ALICE's session in his browser. Her link is still in her inbox and
  // still valid; she clicks it again.
  it("is dropped by an outage rather than waiting for the next visitor", async () => {
    window.history.replaceState({}, "", "/connexion#jeton=alice");
    API.takeLinkToken(); // what main.tsx does, at boot
    vi.mocked(API.redeemLink).mockResolvedValue(
      who("alice@exemple.fr", "Alice Bénévole"),
    );
    vi.mocked(API.me).mockRejectedValue(new Error("Session absente."));
    vi.mocked(API.detectMode).mockResolvedValue({
      kind: "outage",
      message: "Le serveur de la campagne est injoignable.",
    });

    await act(() => {
      root.render(<App />);
    });
    await flush();
    expect(text()).toContain("Serveur injoignable");

    // Bruno presses « Réessayer », and this time the campaign answers
    vi.mocked(API.detectMode).mockResolvedValue({
      kind: "team",
      config: CONFIG,
    });
    await click("Réessayer");
    await flush();
    await flush();
    expect(text()).toContain("Connexion");
    expect(API.redeemLink).not.toHaveBeenCalled();
    expect(text()).not.toContain("Alice");
  });

  // The link's own refusal outranks whatever the cookie's turn says next.
  //
  // The cookie is tried after a spent link, and its failure is not always a
  // 401: a 502, a proxy hiccup, a dropped connection. That error overwrote
  // « ce lien n'est plus valable » with a network message, and its reader
  // went back to their inbox to click the same dead link again.
  it("says the link is spent even when the cookie's turn fails too", async () => {
    window.history.replaceState({}, "", "/connexion#jeton=perime");
    API.takeLinkToken();
    vi.mocked(API.redeemLink).mockRejectedValue(
      new API_ERROR(401, "Ce lien n'est plus valable."),
    );
    vi.mocked(API.me).mockRejectedValue(new Error("Erreur HTTP 502."));
    vi.mocked(API.detectMode).mockResolvedValue({
      kind: "team",
      config: CONFIG,
    });

    await act(() => {
      root.render(<Team config={CONFIG} />);
    });
    await flush();
    expect(text()).toContain("Ce lien n'est plus valable.");
  });

  // Handed over EXACTLY ONCE, which is also what stops React's development
  // double-mount from redeeming a token twice and showing a signed-in
  // volunteer "this link is no longer valid".
  it("is handed over once and once only", () => {
    window.history.replaceState({}, "", "/connexion#jeton=abc123");
    API.takeLinkToken();
    expect(API.consumeLinkToken()).toBe("abc123");
    expect(API.consumeLinkToken()).toBeNull();
  });

  // The token comes out of the URL at BOOT, not inside the screen that uses
  // it. Taken there, every path that never reaches that screen — the server
  // down, the account-less version, a mode detection answering something
  // else — left the credential in the address bar and in the history entry,
  // for as long as the outage lasted.
  it("is out of the address bar even when the server never answers", async () => {
    window.history.replaceState({}, "", "/connexion#jeton=abc123");
    // what main.tsx does, before anything is rendered
    API.takeLinkToken();
    expect(window.location.hash).toBe("");

    vi.mocked(API.detectMode).mockResolvedValue({
      kind: "outage",
      message: "Le serveur de la campagne est injoignable.",
    });
    await act(() => {
      root.render(<App />);
    });
    await flush();
    expect(text()).toContain("Serveur injoignable");
    expect(window.location.hash).toBe("");
  });

  // A spent link says so — and does not throw away the session the cookie
  // may still hold. Someone whose link expired while they were already
  // signed in on this browser must not be shown the door.
  it("says a spent link is spent, and still restores a live session", async () => {
    const marie = who("marie@exemple.fr", "Marie Bénévole");
    vi.mocked(API.redeemLink).mockRejectedValue(
      new Error("Ce lien n'est plus valable."),
    );
    vi.mocked(API.me).mockResolvedValue(marie);
    vi.mocked(API.detectMode).mockResolvedValue({
      kind: "team",
      config: CONFIG,
    });

    window.history.replaceState({}, "", "/connexion#jeton=perime");
    API.takeLinkToken();
    await act(() => {
      root.render(<Team config={CONFIG} />);
    });
    await flush();
    expect(text()).toContain("Ce lien n'est plus valable.");
    // the cookie got its chance, and the session it holds is open
    expect(API.me).toHaveBeenCalled();
    expect(text()).toContain("Marie Bénévole");
  });
});
