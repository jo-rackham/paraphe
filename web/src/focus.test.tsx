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
import { CAMPAIGN_KEYS } from "../../noyau/messages.ts";
import App from "./App.tsx";
import * as API from "./api.ts";
import Browser from "./Browser.tsx";
import { Alerte, RenderGuard, resetViewMemory } from "./common.tsx";
import * as DB from "./db.ts";
import Instance from "./Instance.tsx";
import Team from "./Team.tsx";
import type { InstanceConfig, Message, ServerConfig } from "./types.ts";

vi.mock("./db.ts", { spy: true });

vi.mock("./api.ts", { spy: true });

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

const INSTANCE_CONFIG: InstanceConfig = {
  mode: "instance",
  base_domain: "paraphe.test",
  source_url: "",
  browser_version_url: "",
  campaign_keys: CAMPAIGN_KEYS,
};

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
