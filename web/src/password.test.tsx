// Choosing one's own password, and drawing one for somebody who lost theirs.
//
// The API is mocked: what is decided here is what the SCREENS do — that the
// current password is asked for, that a mismatch is caught before a round
// trip, that a refusal lands beside the fields it is about rather than
// throwing the volunteer out, and that two drawn passwords can sit on screen
// at once without one replacing the other.

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as API from "./api.ts";
import Team from "./Team.tsx";
import { teamConfig, who } from "./testing/fixtures.ts";
import type { Me, TeamAccount } from "./types.ts";

vi.mock("./api.ts", { spy: true });

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

const CONFIG = teamConfig();
const ALICE = who("alice@exemple.fr", "Alice Bénévole");
const COORD: Me = {
  ...ALICE,
  account: { ...ALICE.account, role: "coordination" },
  may_manage: true,
};

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

function button(label: string): HTMLButtonElement {
  const b = [...container.querySelectorAll("button")].find((el) =>
    el.textContent?.includes(label),
  );
  if (!b) throw new Error(`no button « ${label} » on screen`);
  return b;
}

const click = (label: string) =>
  act(async () => {
    button(label).dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });

const setValue = (field: HTMLInputElement, value: string) => {
  const set = Object.getOwnPropertyDescriptor(
    window.HTMLInputElement.prototype,
    "value",
  )?.set;
  set?.call(field, value);
  field.dispatchEvent(new Event("input", { bubbles: true }));
};

/** The three fields of the password card, in document order. */
const passwordFields = () => {
  const form = [...container.querySelectorAll("form")].find((f) =>
    f.textContent?.includes("Changer mon mot de passe"),
  );
  if (!form) throw new Error("no password card on screen");
  return [...form.querySelectorAll<HTMLInputElement>("input[type=password]")];
};

async function fill(current: string, next: string, confirm: string) {
  const [a, b, c] = passwordFields();
  await act(async () => {
    setValue(a, current);
    setValue(b, next);
    setValue(c, confirm);
  });
}

async function openProfile(person: Me = ALICE) {
  vi.mocked(API.me).mockResolvedValueOnce(person);
  await act(async () => {
    root.render(<Team config={CONFIG} />);
  });
  await until(() => text().includes("Mon profil"), "the app opens");
  await click("Mon profil");
  await until(() => text().includes("Changer mon mot de passe"), "the card");
}

beforeEach(() => {
  vi.mocked(API.detectMode).mockResolvedValue({ kind: "team", config: CONFIG });
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

/** Cleanups a test registers for itself — a console spy must be restored
 *  even when the assertion below it fails. */
const afterThisTest: (() => void)[] = [];

afterEach(async () => {
  await act(async () => {
    root.unmount();
  });
  container.remove();
  while (afterThisTest.length > 0) afterThisTest.pop()?.();
  vi.resetAllMocks();
});

describe("changing one's own password", () => {
  it("sends the current one with the new one", async () => {
    vi.mocked(API.changePassword).mockResolvedValue({
      state: "password_changed",
    });
    await openProfile();
    await fill("ancien-mot-de-passe", "colline-verger-42", "colline-verger-42");
    await click("Changer mon mot de passe");
    await flush();

    // the current password is the whole guard: a session cookie is a bearer
    // token, and without this proof a borrowed afternoon becomes ownership
    expect(API.changePassword).toHaveBeenCalledWith(
      "ancien-mot-de-passe",
      "colline-verger-42",
    );
    await until(
      () => text().includes("Vos autres sessions"),
      "the screen says what the change did",
    );
    // …and the fields are emptied: three passwords left in a form on a
    // shared computer are three passwords the next person reads by
    // unmasking them
    expect(passwordFields().map((f) => f.value)).toEqual(["", "", ""]);
  });

  // Caught HERE, before the round trip: the server cannot see it — it never
  // receives the confirmation — and a mistyped new password is an account
  // nobody opens again, since nothing on this screen can show what was typed.
  it("refuses two different new passwords without asking the server", async () => {
    await openProfile();
    await fill("ancien-mot-de-passe", "colline-verger-42", "colline-verger-43");
    await click("Changer mon mot de passe");
    await flush();

    expect(API.changePassword).not.toHaveBeenCalled();
    expect(text()).toContain("ne correspondent pas");
  });

  // A wrong current password answers 403 and the screen says so BESIDE the
  // field. A 401 would have fired SESSION_LOST and returned the volunteer to
  // the sign-in form — thrown out of a live session by a typo.
  it("says a wrong current password without ending the session", async () => {
    const { APIError } =
      await vi.importActual<typeof import("./api.ts")>("./api.ts");
    vi.mocked(API.changePassword).mockRejectedValue(
      new APIError(403, "Mot de passe actuel incorrect."),
    );
    await openProfile();
    await fill("pas-le-bon", "colline-verger-42", "colline-verger-42");
    await click("Changer mon mot de passe");
    await flush();

    await until(
      () => text().includes("Mot de passe actuel incorrect"),
      "the refusal is on screen",
    );
    // still on the profile, still signed in: the form is where it was
    expect(text()).toContain("Changer mon mot de passe");
    expect(text()).not.toContain("Votre session a expiré");
  });

  // Two clicks in the same tick run two handlers built by the same render.
  // On this form that spends two of the ten attempts an account is allowed
  // per quarter of an hour, and the second carries a password the first has
  // already replaced.
  it("files one change however fast the button is pressed", async () => {
    vi.mocked(API.changePassword).mockResolvedValue({
      state: "password_changed",
    });
    await openProfile();
    await fill("ancien-mot-de-passe", "colline-verger-42", "colline-verger-42");
    await act(async () => {
      const b = button("Changer mon mot de passe");
      b.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      b.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flush();
    expect(API.changePassword).toHaveBeenCalledTimes(1);
  });
});

describe("drawing a password for somebody who lost theirs", () => {
  const accounts: TeamAccount[] = [
    {
      email: "benevole@exemple.fr",
      name: "Bruno Bénévole",
      role: "volunteer",
      team_id: null,
      active: true,
      personal_note: "",
      team_name: null,
      created_at: "",
      created_by: "",
      team: null,
    },
  ];

  async function openManagement() {
    vi.mocked(API.me).mockResolvedValueOnce(COORD);
    vi.mocked(API.team).mockResolvedValue({
      accounts,
      teams: [],
      departments: [],
      requests: [],
    });
    await act(async () => {
      root.render(<Team config={CONFIG} />);
    });
    await until(() => text().includes("Ma campagne"), "the app opens");
    await click("Ma campagne");
    await until(() => text().includes("Les accès"), "the management screen");
  }

  it("shows the drawn password once, and says the old sessions are closed", async () => {
    vi.mocked(API.resetPassword).mockResolvedValue({
      email: "benevole@exemple.fr",
      name: "Bruno Bénévole",
      password: "abricot-clocher-tilleul-rivage-42",
    });
    await openManagement();
    await click("nouveau mot de passe");
    await flush();

    await until(
      () => text().includes("abricot-clocher-tilleul-rivage-42"),
      "the password is on screen",
    );
    expect(text()).toContain("Nouveau mot de passe pour");
    // what the act DID, which is the half a manager has to know: whoever
    // held the old password is out
    expect(text()).toContain("sessions ouvertes avec l'ancien");
  });

  // TWO cards can name one person — draw a password, fail to note it, draw
  // another. Keyed by the address they were one card that replaced itself,
  // and the password nobody had written down left the only screen it existed
  // on. React calls duplicate keys unsupported, and this is the same lesson
  // the instance's moderation screen already paid for.
  it("keeps both passwords when one person is drawn twice", async () => {
    const complaints: string[] = [];
    const spy = vi
      .spyOn(console, "error")
      .mockImplementation((...a: unknown[]) => complaints.push(String(a[0])));
    afterThisTest.push(() => spy.mockRestore());
    vi.mocked(API.resetPassword)
      .mockResolvedValueOnce({
        email: "benevole@exemple.fr",
        name: "Bruno Bénévole",
        password: "premier-mot-de-passe-11",
      })
      .mockResolvedValueOnce({
        email: "benevole@exemple.fr",
        name: "Bruno Bénévole",
        password: "second-mot-de-passe-22",
      });
    await openManagement();
    await click("nouveau mot de passe");
    await until(() => text().includes("premier-mot-de-passe-11"), "the first");
    await click("nouveau mot de passe");
    await until(() => text().includes("second-mot-de-passe-22"), "the second");

    expect(
      text(),
      "the first password left the screen when the second arrived: it is " +
        "stored nowhere else, so it is simply gone",
    ).toContain("premier-mot-de-passe-11");
    expect(text()).toContain("2 mots de passe sont affichés");

    // …and THIS is the assertion that bites. Keyed by the address the two
    // cards still render and still dismiss correctly — measured, and every
    // assertion above stayed green under that mutation. What is wrong is
    // what React states: duplicate keys are "unsupported and could change",
    // so the loss is a version away rather than here today. The same
    // measurement the instance's moderation screen recorded before it.
    expect(
      complaints.filter((c) => c.includes("same key")),
      "two password cards share a React key: one person can be drawn twice, " +
        "so the key cannot be the address",
    ).toEqual([]);
  });

  // One's own password is not drawn from here: it would show a password
  // instead of taking one you chose, and end the session doing it.
  it("offers no such button on one's own row", async () => {
    vi.mocked(API.me).mockResolvedValueOnce(COORD);
    vi.mocked(API.team).mockResolvedValue({
      accounts: [
        ...accounts,
        { ...accounts[0], email: COORD.account.email, name: "Moi" },
      ],
      teams: [],
      departments: [],
      requests: [],
    });
    await act(async () => {
      root.render(<Team config={CONFIG} />);
    });
    await until(() => text().includes("Ma campagne"), "the app opens");
    await click("Ma campagne");
    await until(() => text().includes("Les accès"), "the management screen");

    const mine = [...container.querySelectorAll("tr")].find((tr) =>
      tr.textContent?.includes(COORD.account.email),
    );
    expect(mine?.textContent).not.toContain("nouveau mot de passe");
  });
});
