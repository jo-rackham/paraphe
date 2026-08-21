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
import * as API from "./api.ts";
import Team from "./Team.tsx";
import { teamConfig, who } from "./testing/fixtures.ts";
import type { Me } from "./types.ts";

vi.mock("./api.ts", { spy: true });

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

const CONFIG = teamConfig();

const MAYOR = {
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
  democratic_theme_endorsement: "",
  email: "mairie@exemple.fr",
  status: "to_contact",
};

const inDept90 = (email: string, name: string): Me => who(email, name, ["90"]);

const ALICE = inDept90("alice@exemple.fr", "Alice Bénévole");
// same display name on purpose: the only barrier left when the email is
// not compared is `signer`, a free field — and this project's thesis is
// that names repeat
const BRUNO = inDept90("bruno@exemple.fr", "Alice Bénévole");

let container: HTMLDivElement;
let root: Root;

const flush = () =>
  act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
const text = () => container.textContent ?? "";

async function until(pred: () => boolean, what: string) {
  for (let i = 0; i < 50; i++) {
    if (pred()) return;
    await flush();
  }
  throw new Error(`never happened: ${what}`);
}

function button(label: string): HTMLElement {
  // "button, a": the nav tabs are links now — a real href is what lets
  // ctrl+clic open a view in a new tab — and this helper reaches them too
  const b = [...container.querySelectorAll<HTMLElement>("button, a")].find(
    (el) => el.textContent?.includes(label),
  );
  if (!b) throw new Error(`no button « ${label} » on screen`);
  return b;
}

const click = (label: string) =>
  act(async () => {
    button(label).dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });

function type(field: HTMLTextAreaElement, value: string) {
  const set = Object.getOwnPropertyDescriptor(
    window.HTMLTextAreaElement.prototype,
    "value",
  )!.set!;
  return act(async () => {
    set.call(field, value);
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

const emailBody = () =>
  container.querySelector<HTMLTextAreaElement>("textarea[rows='16']")!;

const noteField = () =>
  [...container.querySelectorAll("label")]
    .find((l) => l.textContent?.startsWith("Note"))!
    .querySelector("textarea")!;

/** Signs in through the form, the way the screen does. */
async function signIn(person: Me) {
  vi.mocked(API.signIn).mockResolvedValueOnce(person);
  const [email, password] = container.querySelectorAll("input");
  const set = Object.getOwnPropertyDescriptor(
    window.HTMLInputElement.prototype,
    "value",
  )!.set!;
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
  await until(
    () => text().includes("Bourg-Réel"),
    "the dashboard lists the card",
  );
  await act(async () => {
    container
      .querySelector<HTMLButtonElement>("table button.lien")!
      .dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
  await until(() => !!emailBody(), "the card opens");
}

beforeEach(() => {
  vi.mocked(API.detectMode).mockResolvedValue({ kind: "team", config: CONFIG });
  vi.mocked(API.dashboard).mockResolvedValue({
    stats: {},
    total: 1,
    departments_with_promise: [],
    departments_covered: 0,
    mine: [MAYOR],
    team: [],
    departments: ["90"],
    by_rank: { has_endorsed: 1 },
    batch_size: 10,
  });
  vi.mocked(API.card).mockResolvedValue({ mayor: MAYOR, notes: [] });
  vi.mocked(API.signOut).mockResolvedValue(undefined);
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
  // and the second test would then call the real API
  vi.resetAllMocks();
});

// The management screen is reached by two roles and is not the same thing
// for them. A référent leads ONE team; the coordination has none — it holds
// the campaign, its texts and every team — and « Mon équipe » there asks a
// question the screen answers with « aucune équipe », which reads as the
// tool having made them one.
describe("what the management screen is called", () => {
  const asRole = (role: "coordination" | "lead"): Me => ({
    ...ALICE,
    account: { ...ALICE.account, role },
    may_manage: true,
  });

  it.each([
    ["coordination", "Ma campagne", "Mon équipe"],
    ["lead", "Mon équipe", "Ma campagne"],
  ] as const)("names it %s → « %s »", async (role, shown, hidden) => {
    const who = asRole(role);
    vi.mocked(API.me).mockResolvedValueOnce(who);
    vi.mocked(API.team).mockResolvedValue({
      accounts: [],
      teams: [],
      departments: [],
      requests: [],
    });
    await act(async () => {
      root.render(<Team config={CONFIG} />);
    });
    await until(() => text().includes(shown), `the ${role} tab`);

    // the TAB, and the heading behind it, and the document title: one
    // function feeds all three, and a name written three times is a name
    // that stays behind in two of them
    expect(button(shown)).toBeDefined();
    // scoped to the NAVIGATION, which is what this is about. Swept over the
    // whole document it also read the GUIDE, and the guide names both screens
    // on purpose — it is shared by every role, and a référent has to be told
    // theirs is called « Mon équipe ». A guard that cries on prose is one the
    // next author routes around.
    expect(container.querySelector("nav")?.textContent ?? "").not.toContain(
      hidden,
    );
    await click(shown);
    await until(() => text().includes("Les accès"), "the screen opens");
    expect(container.querySelector("h1")?.textContent).toBe(shown);
    expect(document.title).toContain(shown);
  });
});

describe("unsent card work in team mode", () => {
  it("does not follow the computer to the next person", async () => {
    // Alice arrives by COOKIE — the scenario the guard exists for: a
    // shared computer, a session left open overnight. That path never
    // called onSignedIn, the account is never recorded and the store
    // was never cleared.
    vi.mocked(API.me).mockResolvedValueOnce(ALICE);
    await act(async () => {
      root.render(<Team config={CONFIG} />);
    });
    await until(
      () => text().includes("Mon tableau"),
      "Alice's session is restored",
    );
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
    expect(
      emailBody().value,
      "Bruno must not inherit Alice's rewrite",
    ).not.toContain("Texte d'Alice");
    expect(
      noteField().value,
      "nor her private note about a named mayor's family",
    ).toBe("");
  });

  // Signing out is deliberate: the volunteer is leaving the machine. A
  // lost session is not — hence the two are treated differently.
  it("is discarded by an explicit sign-out, even for the same person", async () => {
    vi.mocked(API.me).mockResolvedValueOnce(ALICE);
    await act(async () => {
      root.render(<Team config={CONFIG} />);
    });
    await until(
      () => text().includes("Mon tableau"),
      "the session is restored",
    );
    await openCard();
    await type(emailBody(), "Texte d'Alice pour Mme MARTIN.");

    await click("déconnexion");
    await until(() => text().includes("Se connecter"), "Alice is signed out");
    await signIn(ALICE);
    await openCard();
    expect(
      emailBody().value,
      "she left the computer: what she had not sent goes with her",
    ).not.toContain("Texte d'Alice");
  });

  it("keeps it for the SAME person signing back in after a lost session", async () => {
    vi.mocked(API.me).mockResolvedValueOnce(ALICE);
    await act(async () => {
      root.render(<Team config={CONFIG} />);
    });
    await until(
      () => text().includes("Mon tableau"),
      "Alice's session is restored",
    );
    await openCard();
    await type(emailBody(), "Texte d'Alice pour Mme MARTIN.");

    // the session expires under her, and she signs straight back in
    await act(async () => {
      window.dispatchEvent(new Event(API.SESSION_LOST));
    });
    await until(() => text().includes("Se connecter"), "the session is gone");
    await signIn(ALICE);
    await openCard();
    expect(
      emailBody().value,
      "her own work, lost to nothing but a cookie expiring",
    ).toContain("Texte d'Alice");
  });

  it("arms the leave-page dialog, and only while the work still stands", async () => {
    vi.mocked(API.me).mockResolvedValueOnce(ALICE);
    await act(async () => {
      root.render(<Team config={CONFIG} />);
    });
    await until(
      () => text().includes("Mon tableau"),
      "the session is restored",
    );
    const quiet = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(quiet);
    expect(quiet.defaultPrevented, "nothing typed yet").toBe(false);

    await openCard();
    await type(emailBody(), "Phrase écrite à la main.");
    const armed = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(armed);
    expect(
      armed.defaultPrevented,
      "a rewritten email addressed to a named mayor",
    ).toBe(true);
  });

  // The predicate is written twice, once per mode: a divergence in one of
  // them goes unnoticed unless both are held.
  it("arms it for a call note alone, with the email untouched", async () => {
    vi.mocked(API.me).mockResolvedValueOnce(ALICE);
    await act(async () => {
      root.render(<Team config={CONFIG} />);
    });
    await until(
      () => text().includes("Mon tableau"),
      "the session is restored",
    );
    await openCard();
    await type(noteField(), "il rappelle jeudi");
    const armed = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(armed);
    expect(
      armed.defaultPrevented,
      "what was said on the phone exists nowhere else",
    ).toBe(true);
  });
});

// The header of a free card is the ONE place the interface speaks about
// reservation, and it kept promising « enregistrer un statut vous
// l'attribue » after the claim was removed from the write. Standing, it was
// worse than a stale sentence: two volunteers each read that the fiche was
// about to become theirs, both wrote, and both called the same mayor —
// dressing up the one case the new rule accepts as a reservation. Nothing
// pinned it, which is how it stayed true-looking.
describe("what the header of a free card promises", () => {
  it("does not say a status write reserves it, and names what does", async () => {
    vi.mocked(API.me).mockResolvedValueOnce(ALICE);
    await act(async () => {
      root.render(<Team config={CONFIG} />);
    });
    await until(
      () => text().includes("Mon tableau"),
      "Alice's session is restored",
    );
    await openCard();
    expect(text(), "the write no longer takes the card").not.toContain(
      "vous l'attribue",
    );
    expect(text(), "the door that DOES reserve has to be named").toContain(
      "prenez un lot",
    );
  });
});

// A CAMPAIGN IS NOT ITS CANDIDATE, and the mark beside the logo is where a
// volunteer reads which one they are working for.
//
// The subtitle used to be `campaign.candidat`, which for a hosted campaign is
// neither the name its coordination asked for nor the one an administrator
// approved: « Alliance écologiste » signed in and read « Marie Dupont ». The
// two are equal on a campaign bootstrapped from a file, so a fixture where
// they agree passes either way — this one makes them disagree on purpose.
describe("the mark beside the logo", () => {
  const named = teamConfig({
    organisation: {
      slug: "alliance",
      name: "Alliance écologiste",
      listed: true,
    },
    campaign: { ...CONFIG.campaign, candidat: "Marie Dupont" },
  });

  const open = async (config: typeof CONFIG) => {
    vi.mocked(API.detectMode).mockResolvedValue({ kind: "team", config });
    vi.mocked(API.me).mockResolvedValueOnce(ALICE);
    await act(async () => {
      root.render(<Team config={config} />);
    });
    await until(() => text().includes("Mon tableau"), "the app opens");
  };

  it("names the campaign, not its candidate", async () => {
    await open(named);
    const marque = container.querySelector(".marque")!;
    expect(marque.textContent).toContain("Alliance écologiste");
    expect(marque.textContent).not.toContain("Marie Dupont");
  });

  // An unnamed campaign is a supported state — the annuaire skips it, and
  // every campaign bootstrapped without a name starts there. The mark shows
  // the word alone rather than an empty line under it.
  it("shows the word alone when the campaign has no name", async () => {
    await open(teamConfig());
    expect(container.querySelector(".marque .sous")).toBeNull();
  });
});
