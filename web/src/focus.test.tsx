// Where focus LANDS when the control under it is destroyed or disabled.
//
// Clicking « fermer » unmounts the button; « Enregistrer » sets `disabled`
// on the very button being pressed; recovering from an outage swaps the
// whole shell. In every one of those, the browser silently drops focus to
// <body>: the next Tab restarts at the skip link and a keyboard user loses
// their place. The rules pinned here: a self-destroying control hands
// focus to the content (`#contenu`), a busy submit keeps focus by using
// aria-disabled instead of disabled, and leaving the outage screen lands
// on the next view's h1. Written RED against the round-2 state.

import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App.tsx";
import * as API from "./api.ts";
import Browser from "./Browser.tsx";
import { Alerte, RenderGuard, resetViewMemory } from "./common.tsx";
import * as DB from "./db.ts";
import Instance from "./Instance.tsx";
import Team from "./Team.tsx";
import { instanceConfig, teamConfig } from "./testing/fixtures.ts";
import type { Message } from "./types.ts";

vi.mock("./db.ts", { spy: true });

vi.mock("./api.ts", { spy: true });

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

const CONFIG = teamConfig();
const INSTANCE_CONFIG = instanceConfig({ base_domain: "paraphe.test" });

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  // a real page load resets the view memory for free; sequential tests in
  // one file share the module and would leak "a view was already shown"
  resetViewMemory();
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

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

const click = (el: Element) =>
  act(async () => {
    el.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });

/**
 * Ticks the « J'ai vérifié cette adresse » box of every pending card.
 *
 * Approving sends a session link to an address a stranger typed, so the
 * accept button stays inert until the moderator confirms having read it.
 * The tests take the same path a person does.
 */
async function confirmAddresses() {
  for (const l of container.querySelectorAll("label")) {
    if (!l.textContent?.includes("J'ai vérifié")) continue;
    const box = l.querySelector<HTMLInputElement>('input[type="checkbox"]');
    if (box && !box.checked) {
      await act(async () => {
        box.click();
      });
    }
  }
}

describe("focus survives the control's own destruction", () => {
  it("Alerte: « fermer » hands focus to the content, not to <body>", async () => {
    function Harness() {
      const [message, setMessage] = useState<Message | null>({
        tone: "ok",
        text: "campagne enregistrée",
      });
      return (
        <main id="contenu" tabIndex={-1}>
          <Alerte message={message} onClose={() => setMessage(null)} />
        </main>
      );
    }
    await act(async () => {
      root.render(<Harness />);
    });
    const fermer = [...container.querySelectorAll("button")].find(
      (b) => b.textContent === "fermer",
    );
    expect(fermer).toBeDefined();
    await act(async () => {
      fermer?.focus();
    });
    await click(fermer as Element);

    expect(document.activeElement?.id).toBe("contenu");
  });

  it("Connexion: a pending submit keeps its button focusable (aria-disabled, never disabled)", async () => {
    vi.mocked(API.me).mockRejectedValueOnce(
      Object.assign(new Error("non connecté"), { code: 401 }),
    );
    // never settles: the button stays in its busy state for the assertion
    vi.mocked(API.signIn).mockReturnValueOnce(new Promise<never>(() => {}));
    await act(async () => {
      root.render(<Team config={CONFIG} />);
    });
    await flush();

    const [email, password] = container.querySelectorAll("input");
    const set = Object.getOwnPropertyDescriptor(
      window.HTMLInputElement.prototype,
      "value",
    )?.set;
    await act(async () => {
      set?.call(email, "a@b.fr");
      email.dispatchEvent(new Event("input", { bubbles: true }));
      set?.call(password, "mdp");
      password.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const submit = [...container.querySelectorAll("button")].find((b) =>
      b.textContent?.includes("Se connecter"),
    ) as HTMLButtonElement;
    await act(async () => {
      submit.focus();
    });
    await act(async () => {
      submit.form?.requestSubmit(submit);
    });
    await flush();

    // busy: greyed for everyone, still focusable for the keyboard —
    // `disabled` would drop focus to <body> in a real browser
    expect(submit.textContent).toContain("Connexion…");
    expect(submit.disabled).toBe(false);
    expect(submit.getAttribute("aria-disabled")).toBe("true");
    expect(document.activeElement).toBe(submit);
  });

  it("outage recovery: leaving the outage shell focuses the next view's h1", async () => {
    vi.mocked(API.detectMode)
      .mockResolvedValueOnce({ kind: "outage", message: "panne simulée" })
      .mockResolvedValueOnce({ kind: "team", config: CONFIG });
    vi.mocked(API.me).mockRejectedValueOnce(
      Object.assign(new Error("non connecté"), { code: 401 }),
    );
    await act(async () => {
      root.render(<App />);
    });
    await flush();
    expect(container.textContent).toContain("Serveur injoignable");

    const retry = [...container.querySelectorAll("button")].find((b) =>
      b.textContent?.includes("Réessayer"),
    );
    await click(retry as Element);
    await flush();
    await flush();

    // the outage shell — retry button included — is gone; focus must land
    // on the new view's title, not fall back to <body>
    const h1 = container.querySelector("h1");
    expect(h1?.textContent).toBe("Connexion");
    expect(document.activeElement).toBe(h1);
  });

  it("Accueil: loading the demo set unmounts the whole screen — focus is rescued", async () => {
    // empty base: Browser renders the Accueil, whose buttons all vanish
    // the moment a list lands
    await DB.eraseAll();
    await act(async () => {
      root.render(<Browser />);
    });
    await flush();
    const demo = [...container.querySelectorAll("button")].find((b) =>
      b.textContent?.includes("essayer avec des données fictives"),
    ) as HTMLButtonElement;
    await act(async () => {
      demo.focus();
    });
    await act(async () => {
      demo.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    // SEQUENTIAL waits, not one long one: the rescue's timers fire inside
    // an act(), but React only flushes the unmount at act boundaries — a
    // single 120 ms act lets the belt check run BEFORE the commit it is
    // supposed to observe, and the focus falls after it
    for (const ms of [5, 40, 80]) {
      await act(async () => {
        await new Promise((r) => setTimeout(r, ms));
      });
    }

    // the list replaced the Accueil (the counter itself settles later —
    // it starts empty by doctrine)
    expect(container.textContent).toContain("Sainte-Fiction-1");
    expect(document.activeElement?.id).toBe("contenu");
  });

  it("Administration: the apex sign-in obeys the same rule as every other submit", async () => {
    vi.mocked(API.me).mockRejectedValueOnce(
      Object.assign(new Error("non connecté"), { code: 401 }),
    );
    // never settles: the button stays busy for the assertion
    vi.mocked(API.signIn).mockReturnValueOnce(new Promise<never>(() => {}));
    await act(async () => {
      root.render(<Instance config={INSTANCE_CONFIG} />);
    });
    await flush();
    // « Se connecter » names both the link to the sign-in and its submit
    const versConnexion = [...container.querySelectorAll("button.lien")].find(
      (b) => b.textContent?.includes("Se connecter"),
    );
    expect(versConnexion).toBeDefined();
    await click(versConnexion as Element);
    await flush();

    const [email, password] = container.querySelectorAll("input");
    const set = Object.getOwnPropertyDescriptor(
      window.HTMLInputElement.prototype,
      "value",
    )?.set;
    await act(async () => {
      set?.call(email, "admin@exemple.fr");
      email.dispatchEvent(new Event("input", { bubbles: true }));
      set?.call(password, "mdp");
      password.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const submit = container.querySelector(
      "button[type=submit]",
    ) as HTMLButtonElement;
    await act(async () => {
      submit.focus();
    });
    await act(async () => {
      submit.form?.requestSubmit(submit);
    });
    await flush();

    expect(submit.textContent).toContain("Connexion…");
    expect(submit.disabled).toBe(false);
    expect(submit.getAttribute("aria-disabled")).toBe("true");
    expect(document.activeElement).toBe(submit);
  });

  // The moderation pair refused the second press by STATE, which the handler
  // reads from the render it was created in — so two clicks in one tick both
  // saw it free. On this screen that means a decision filed twice, and on two
  // DIFFERENT cards it means one intended click deciding two campaigns.
  it("Moderation: two decisions in one tick file ONE", async () => {
    vi.mocked(API.me).mockResolvedValueOnce({
      account: {
        email: "admin@exemple.fr",
        name: "Administration",
        // the instance administrator's role lives outside the campaign
        // vocabulary the type enumerates
        role: "administration" as unknown as "coordination",
        team_id: null,
        active: true,
        personal_note: "",
        team_name: null,
      },
      departments: [],
      may_manage: true,
    });
    const pending = (id: number, slug: string) => ({
      id,
      slug,
      name: `Campagne ${slug}`,
      requester_email: "qui@exemple.fr",
      requester_name: "Qui",
      message: "",
      state: "pending" as const,
      listed: true,
      reason: "",
      ts: "2026-01-01T00:00",
      decided_at: "",
      decided_by: "",
    });
    vi.mocked(API.moderationQueue).mockResolvedValue({
      requests: [pending(1, "une"), pending(2, "deux")],
      organisations: [],
      base_domain: "paraphe.test",
    });
    // never settles: both clicks land while the first is still in flight
    vi.mocked(API.decideRequest).mockReturnValue(new Promise<never>(() => {}));
    await act(async () => {
      root.render(<Instance config={INSTANCE_CONFIG} />);
    });
    await flush();
    await flush();
    await confirmAddresses();

    const decide = [...container.querySelectorAll("button")].filter((b) =>
      b.textContent?.includes("Ouvrir la campagne"),
    );
    expect(decide.length, "the queue shows both requests").toBe(2);
    // two DIFFERENT cards, one tick: the costliest shape of the bug
    await act(async () => {
      decide[0].dispatchEvent(new MouseEvent("click", { bubbles: true }));
      decide[1].dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flush();
    expect(
      vi.mocked(API.decideRequest).mock.calls.map((c) => c[0]),
      "one intended decision must not reach two campaigns",
    ).toEqual([1]);
  });

  // Signing in as the instance administrator, with a queue of two, and
  // stopping wherever the caller asks.
  const queueOfTwo = async (
    decide: ReturnType<typeof vi.fn>,
    confirm = true,
  ) => {
    vi.mocked(API.me).mockResolvedValueOnce({
      account: {
        email: "admin@exemple.fr",
        name: "Administration",
        role: "administration" as unknown as "coordination",
        team_id: null,
        active: true,
        personal_note: "",
        team_name: null,
      },
      departments: [],
      may_manage: true,
    });
    const pending = (id: number, slug: string) => ({
      id,
      slug,
      name: `Campagne ${slug}`,
      requester_email: "qui@exemple.fr",
      requester_name: "Qui",
      message: "",
      state: "pending" as const,
      listed: true,
      reason: "",
      ts: "2026-01-01T00:00",
      decided_at: "",
      decided_by: "",
    });
    vi.mocked(API.moderationQueue).mockResolvedValue({
      requests: [pending(1, "une"), pending(2, "deux")],
      organisations: [],
      base_domain: "paraphe.test",
    });
    vi.mocked(API.decideRequest).mockImplementation(
      decide as unknown as typeof API.decideRequest,
    );
    await act(async () => {
      root.render(<Instance config={INSTANCE_CONFIG} />);
    });
    await flush();
    await flush();
    if (confirm) await confirmAddresses();
    return [...container.querySelectorAll("button")].filter((b) =>
      b.textContent?.includes("Ouvrir la campagne"),
    );
  };

  // ONE submit guard covers the whole queue, so while any card is in flight
  // every other button's click is swallowed. Wearing aria-disabled only on
  // the card in flight, the others look live and lie. The campaign-side
  // screen was fixed for exactly this; its mirror kept `busy === d.id`.
  it("Moderation: no other decision looks live while one is in flight", async () => {
    const open = await queueOfTwo(vi.fn(() => new Promise<never>(() => {})));
    expect(open.length, "the queue shows both requests").toBe(2);
    await click(open[0]);
    expect(
      open[1].getAttribute("aria-disabled"),
      "the other card's button must not look live while the guard would eat it",
    ).toBe("true");
  });

  // The password is returned ONCE and stored nowhere in the clear. The reload
  // that follows a decision can fail — and `load` swallows its own failure
  // into a message — so the queue was never refreshed: the password card
  // landed beside the very request it answers. A moderator reading that
  // contradiction discards the credential, and the campaign's coordination
  // has no way in.
  it("Moderation: a failed reload leaves no pending card beside the password", async () => {
    const open = await queueOfTwo(
      vi.fn(async () => ({
        id: 1,
        decision: "accepted",
        address: "une.paraphe.test",
        coordination: "qui@exemple.fr",
        password: "mot-de-passe-provisoire",
      })),
    );
    // the refetch the decision triggers never lands
    vi.mocked(API.moderationQueue).mockRejectedValue(new Error("réseau coupé"));
    await click(open[0]);
    await flush();

    const shown = container.textContent ?? "";
    expect(shown, "the one-time password is on screen").toContain(
      "mot-de-passe-provisoire",
    );
    expect(
      [...container.querySelectorAll("button")].filter((b) =>
        b.textContent?.includes("Ouvrir la campagne"),
      ).length,
      "the decided card must not still be waiting beside its own password",
    ).toBe(1);
  });

  // Approving SENDS: an email signed by this instance leaves for an address a
  // stranger typed, carrying a link that opens a session. The address is on
  // the card, but a queue is read fast — so the button stays inert until the
  // administrator has said, on that card, that they read it.
  it("Moderation: approving is inert until the address is confirmed", async () => {
    const decideRequest = vi.fn(async () => ({
      id: 1,
      decision: "accepted",
      address: "une.paraphe.test",
      coordination: "qui@exemple.fr",
      password: "mot-de-passe-provisoire",
    }));
    const open = await queueOfTwo(decideRequest, false);
    expect(
      open[0].getAttribute("aria-disabled"),
      "an unconfirmed accept must not look live",
    ).toBe("true");

    await click(open[0]);
    await flush();
    expect(
      decideRequest.mock.calls.length,
      "the click was swallowed, and nothing was sent",
    ).toBe(0);

    // Reached by « next button » rather than by Tab, an inert control says
    // only « indisponible ». It points at the sentence that would make it
    // live, and that sentence carries the address.
    const describedBy = open[0].getAttribute("aria-describedby");
    expect(
      describedBy,
      "the inert button says what would make it live",
    ).not.toBeNull();
    expect(
      container.querySelector(`#${describedBy}`)?.textContent,
      "and that description names the address being confirmed",
    ).toContain("qui@exemple.fr");

    // …and it opens the moment the address is confirmed
    await confirmAddresses();
    expect(open[0].getAttribute("aria-disabled")).toBeNull();
    await click(open[0]);
    await flush();
    expect(decideRequest.mock.calls.length).toBe(1);
  });

  // What the tick confirms is an ADDRESS, not a row number. Keyed by the id
  // alone, one confirmation would carry over to a different address — and a
  // moderator would have confirmed one thing while the system sent to
  // another, which is the only failure this box exists to prevent.
  it("Moderation: a confirmation is bound to the address it names", async () => {
    vi.mocked(API.me).mockResolvedValueOnce({
      account: {
        email: "admin@exemple.fr",
        name: "Administration",
        role: "administration" as unknown as "coordination",
        team_id: null,
        active: true,
        personal_note: "",
        team_name: null,
      },
      departments: [],
      may_manage: true,
    });
    // the same row identity, two different addresses: what a reordering, a
    // reused identifier or an edited row would look like from here
    const row = (slug: string, email: string) => ({
      id: 7,
      slug,
      name: `Campagne ${slug}`,
      requester_email: email,
      requester_name: "Qui",
      message: "",
      state: "pending" as const,
      listed: true,
      reason: "",
      ts: "2026-01-01T00:00",
      decided_at: "",
      decided_by: "",
    });
    vi.mocked(API.moderationQueue).mockResolvedValue({
      requests: [
        row("une", "premiere@exemple.fr"),
        row("deux", "seconde@exemple.fr"),
      ],
      organisations: [],
      base_domain: "paraphe.test",
    });
    await act(async () => {
      root.render(<Instance config={INSTANCE_CONFIG} />);
    });
    await flush();
    await flush();

    const boxes = [...container.querySelectorAll("label")]
      .filter((l) => l.textContent?.includes("J'ai vérifié"))
      .map((l) => l.querySelector<HTMLInputElement>('input[type="checkbox"]'));
    expect(boxes.length, "one confirmation per card").toBe(2);
    await act(async () => {
      boxes[0]?.click();
    });
    expect(
      boxes[1]?.checked,
      "confirming one address must not confirm another",
    ).toBe(false);
  });

  // The coordination password is returned once and stored nowhere in the
  // clear. It lived in a SINGLE slot, and two flows write it — approving a
  // request, and creating a campaign outright, each behind its own re-entry
  // guard. It did not even need a race: approving a second request while the
  // first password was still on screen replaced it, which is exactly how a
  // moderator works through a queue. Appending is what makes that impossible.
  it("Moderation: a second decision does not wipe the first password", async () => {
    vi.mocked(API.me).mockResolvedValueOnce({
      account: {
        email: "admin@exemple.fr",
        name: "Administration",
        role: "administration" as unknown as "coordination",
        team_id: null,
        active: true,
        personal_note: "",
        team_name: null,
      },
      departments: [],
      may_manage: true,
    });
    const pending = (id: number, slug: string) => ({
      id,
      slug,
      name: `Campagne ${slug}`,
      requester_email: "qui@exemple.fr",
      requester_name: "Qui",
      message: "",
      state: "pending" as const,
      listed: true,
      reason: "",
      ts: "2026-01-01T00:00",
      decided_at: "",
      decided_by: "",
    });
    // a REAL server: a request that was decided comes back decided, so the
    // first card leaves the queue and the second is the one still standing
    const settled = new Set<number>();
    vi.mocked(API.moderationQueue).mockImplementation(async () => ({
      requests: [pending(1, "alpha"), pending(2, "beta")].map((r) =>
        settled.has(r.id) ? { ...r, state: "accepted" as const } : r,
      ),
      organisations: [],
      base_domain: "paraphe.test",
    }));
    vi.mocked(API.decideRequest).mockImplementation((async (id: number) => {
      settled.add(id);
      return {
        id,
        decision: "accepted",
        address: id === 1 ? "alpha.paraphe.test" : "beta.paraphe.test",
        coordination: "qui@exemple.fr",
        password: id === 1 ? "MOT-DE-PASSE-ALPHA" : "MOT-DE-PASSE-BETA",
      };
    }) as unknown as typeof API.decideRequest);

    await act(async () => {
      root.render(<Instance config={INSTANCE_CONFIG} />);
    });
    await flush();
    await flush();
    await confirmAddresses();

    const open = () =>
      [...container.querySelectorAll("button")].filter((b) =>
        b.textContent?.includes("Ouvrir la campagne"),
      );
    expect(open().length, "two pending requests").toBe(2);

    await click(open()[0]);
    await flush();
    expect(container.textContent).toContain("MOT-DE-PASSE-ALPHA");

    // …and now the second, WITHOUT pressing « J'ai noté » on the first
    await click(open()[0]);
    await flush();

    const shown = container.textContent ?? "";
    expect(shown, "the second password is shown").toContain(
      "MOT-DE-PASSE-BETA",
    );
    expect(
      shown,
      "the first password is shown once and nowhere else: it must survive",
    ).toContain("MOT-DE-PASSE-ALPHA");

    // Each card says WHICH one it closes. Several stand at once, and a
    // screen reader that hears « J'ai noté » twice cannot tell them apart.
    const noted = [...container.querySelectorAll("button")].filter((b) =>
      b.textContent?.includes("J'ai noté"),
    );
    expect(noted.length, "one dismiss button per password").toBe(2);
    expect(
      new Set(noted.map((b) => b.textContent)).size,
      "the two dismiss buttons must not share one accessible name",
    ).toBe(2);

    // Noting a card announces NOTHING. The region carries the event that
    // happened, not a value derived from what is still on screen: derived,
    // closing the newest made it announce the opening of the one before —
    // an opening that never happened, and a password to hand to the wrong
    // person.
    const region = () =>
      [...container.querySelectorAll("[role='status']")]
        .map((n) => n.textContent ?? "")
        .join(" ");
    expect(region(), "it speaks of the campaign just opened").toContain(
      "beta.paraphe.test",
    );
    await click(noted[1]);
    await flush();
    expect(
      region(),
      "closing a card must not announce the opening of another",
    ).not.toContain("La campagne alpha.paraphe.test vient d'être ouverte");
  });

  it("Demande: a request accepted replaces the whole form — focus is rescued", async () => {
    vi.mocked(API.me).mockRejectedValueOnce(
      Object.assign(new Error("non connecté"), { code: 401 }),
    );
    vi.mocked(API.requestCampaign).mockResolvedValueOnce({
      id: 1,
      slug: "ma-campagne",
      message: "Demande enregistrée.",
    });
    await act(async () => {
      root.render(<Instance config={INSTANCE_CONFIG} />);
    });
    await flush();
    const versDemande = [...container.querySelectorAll("button")].find((b) =>
      b.textContent?.includes("Demander"),
    );
    await click(versDemande as Element);
    await flush();

    const set = Object.getOwnPropertyDescriptor(
      window.HTMLInputElement.prototype,
      "value",
    )?.set;
    await act(async () => {
      for (const input of container.querySelectorAll("input[required]")) {
        set?.call(input, "ma-campagne");
        input.dispatchEvent(new Event("input", { bubbles: true }));
      }
    });
    const submit = [...container.querySelectorAll("button")].find((b) =>
      b.textContent?.includes("Envoyer"),
    ) as HTMLButtonElement;
    await act(async () => {
      submit.focus();
    });
    await act(async () => {
      submit.form?.requestSubmit(submit);
    });
    // the rescue waits for React's commit, then checks again 60 ms later
    for (const ms of [5, 40, 80]) {
      await act(async () => {
        await new Promise((r) => setTimeout(r, ms));
      });
    }

    expect(container.textContent).toContain("Demande enregistrée");
    expect(document.activeElement?.id).toBe("contenu");
  });

  it("Demande: two submits in one tick file ONE request", async () => {
    // the spies live for the whole file: an earlier journey already called
    // this one, and counting its calls too would prove nothing
    vi.mocked(API.requestCampaign).mockClear();
    vi.mocked(API.me).mockRejectedValueOnce(
      Object.assign(new Error("non connecté"), { code: 401 }),
    );
    vi.mocked(API.requestCampaign).mockResolvedValue({
      id: 1,
      slug: "ma-campagne",
      message: "Demande enregistrée.",
    });
    await act(async () => {
      root.render(<Instance config={INSTANCE_CONFIG} />);
    });
    await flush();
    const versDemande = [...container.querySelectorAll("button")].find((b) =>
      b.textContent?.includes("Demander"),
    );
    await click(versDemande as Element);
    await flush();

    const set = Object.getOwnPropertyDescriptor(
      window.HTMLInputElement.prototype,
      "value",
    )?.set;
    await act(async () => {
      for (const input of container.querySelectorAll("input[required]")) {
        set?.call(input, "ma-campagne");
        input.dispatchEvent(new Event("input", { bubbles: true }));
      }
    });
    const submit = [...container.querySelectorAll("button")].find((b) =>
      b.textContent?.includes("Envoyer"),
    ) as HTMLButtonElement;
    // aria-disabled keeps the button clickable ON PURPOSE, so the handler is
    // what has to refuse the second press — and `sending`, read from the
    // render's closure, is still false for both
    await act(async () => {
      submit.form?.requestSubmit(submit);
      submit.form?.requestSubmit(submit);
    });
    await flush();

    expect(
      vi.mocked(API.requestCampaign).mock.calls.length,
      "one intended request filed two rows in the moderation queue",
    ).toBe(1);
  });

  it("RenderGuard: « Continuer » unmounts the error screen — focus is rescued", async () => {
    let boom = true;
    function Bomb() {
      if (boom) throw new Error("panne de rendu simulée");
      return <main id="contenu" tabIndex={-1} />;
    }
    // the guard logs the error on purpose; keep the test output readable
    const muted = vi.spyOn(console, "error").mockImplementation(() => {});
    await act(async () => {
      root.render(
        <RenderGuard>
          <Bomb />
        </RenderGuard>,
      );
    });
    muted.mockRestore();
    expect(container.textContent).toContain("Cet écran n'a pas pu s'afficher");

    boom = false;
    const continuer = [...container.querySelectorAll("button")].find((b) =>
      b.textContent?.includes("Continuer"),
    ) as HTMLButtonElement;
    await act(async () => {
      continuer.focus();
    });
    await act(async () => {
      continuer.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await act(async () => {
      await new Promise((r) => setTimeout(r, 120));
    });

    expect(document.activeElement?.id).toBe("contenu");
  });
});
