// Asking a campaign to open a local team, and the coordination deciding.
//
// The API is mocked: what is proved here is what the INTERFACE decides on
// its own — that the door exists for someone with no account, that a double
// submit files one request, and above all that accepting sends the name and
// the perimeter the coordination settled on rather than the ones asked for.
// A screen that quietly posts back what it was given makes the two editable
// fields decoration.

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CAMPAIGN_KEYS } from "../../noyau/messages.ts";
import * as API from "./api.ts";
import Team from "./Team.tsx";
import type { Me, ServerConfig, TeamRequest } from "./types.ts";

vi.mock("./api.ts", { spy: true });

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

// The REAL class: under `spy: true` the exported one is a wrapper, and
// `new` on it throws. Team.tsx tells a first-load 401 from an outage with
// an instanceof, so the rejection has to be the genuine article.
const { APIError } =
  await vi.importActual<typeof import("./api.ts")>("./api.ts");
const noSessionYet = () => new APIError(401, "Session absente ou expirée.");

const CONFIG: ServerConfig = {
  mode: "team",
  departments: ["01", "02", "03"],
  campaign: Object.fromEntries(CAMPAIGN_KEYS.map((k) => [k, `valeur ${k}`])),
  batch_size: 10,
  unfilled: [],
  source_url: "",
  magic_link: false,
  logo: null,
  statuses: [{ key: "to_contact", label: "À contacter", colour: "#eee" }],
  ranks: [{ key: "has_endorsed", label: "A parrainé" }],
};

const PENDING: TeamRequest = {
  id: 7,
  name: "Équipe du 01",
  departments: "01",
  requester_email: "referente@exemple.fr",
  requester_name: "Référente Possible",
  message: "Nous sommes cinq sur le département.",
  state: "pending",
  reason: "",
  ts: "2026-08-16T09:00",
  decided_at: "",
  decided_by: "",
};

const boss = (role: "coordination" | "lead"): Me => ({
  account: {
    email: "chef@exemple.fr",
    name: "Chef",
    role,
    team_id: null,
    active: true,
    personal_note: "",
    team_name: null,
  },
  departments: [],
  may_manage: true,
});

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

/** A labelled field, found the way a person reads the form. */
function field<T extends HTMLElement>(labelStart: string): T {
  const l = [...container.querySelectorAll("label")].find((el) =>
    el.textContent?.trim().startsWith(labelStart),
  );
  const control = l?.querySelector("input, textarea, select");
  if (!control) throw new Error(`no field « ${labelStart} » on screen`);
  return control as T;
}

function fill(labelStart: string, value: string) {
  const el = field<HTMLInputElement | HTMLTextAreaElement>(labelStart);
  const proto =
    el.tagName === "TEXTAREA"
      ? window.HTMLTextAreaElement.prototype
      : window.HTMLInputElement.prototype;
  const set = Object.getOwnPropertyDescriptor(proto, "value")!.set!;
  return act(async () => {
    set.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

function select(labelStart: string, values: string[]) {
  const el = field<HTMLSelectElement>(labelStart);
  return act(async () => {
    for (const o of el.options) o.selected = values.includes(o.value);
    el.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

beforeEach(() => {
  vi.mocked(API.detectMode).mockResolvedValue({ kind: "team", config: CONFIG });
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
});

describe("asking a campaign to open a local team", () => {
  it("is offered on the sign-in screen, to someone with no account at all", async () => {
    vi.mocked(API.me).mockRejectedValueOnce(noSessionYet());
    vi.mocked(API.requestTeam).mockResolvedValue({
      id: 7,
      name: "Équipe du 02",
      message: "Demande enregistrée.",
    });
    await act(async () => {
      root.render(<Team config={CONFIG} />);
    });
    await until(() => text().includes("Se connecter"), "the sign-in screen");

    await click("Demander à créer une équipe");
    await fill("Nom de l'équipe", "Équipe du 02");
    await select("Départements", ["02"]);
    await fill("Votre nom", "Référente Possible");
    await fill("Votre adresse email", "referente@exemple.fr");
    // FOCUSED first, then clicked: the rescue leaves an untouched focus
    // alone, so a click nobody was holding proves nothing about it
    const send = button("Envoyer la demande");
    await act(async () => {
      send.focus();
    });
    await act(async () => {
      send.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await until(
      () => text().includes("Demande enregistrée"),
      "the confirmation replaces the form",
    );
    for (const ms of [5, 40, 80]) {
      await act(async () => {
        await new Promise((r) => setTimeout(r, ms));
      });
    }

    expect(vi.mocked(API.requestTeam)).toHaveBeenCalledWith({
      name: "Équipe du 02",
      departments: ["02"],
      requester_name: "Référente Possible",
      requester_email: "referente@exemple.fr",
      message: "",
    });
    // the form — the pressed button included — is gone: focus lands on the
    // content landmark instead of falling to <body>, where the next Tab
    // would restart at the skip link
    expect(document.activeElement?.id).toBe("contenu");
  });

  it("files ONE request when the button is pressed twice in one tick", async () => {
    vi.mocked(API.me).mockRejectedValueOnce(noSessionYet());
    vi.mocked(API.requestTeam).mockResolvedValue({
      id: 7,
      name: "Équipe du 02",
      message: "Demande enregistrée.",
    });
    await act(async () => {
      root.render(<Team config={CONFIG} />);
    });
    await until(() => text().includes("Se connecter"), "the sign-in screen");
    await click("Demander à créer une équipe");
    await fill("Nom de l'équipe", "Équipe du 02");
    await fill("Votre nom", "Référente Possible");
    await fill("Votre adresse email", "referente@exemple.fr");

    // aria-disabled leaves the button clickable on purpose: the guard is a
    // ref, because state is a render behind
    const send = button("Envoyer la demande");
    await act(async () => {
      send.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      send.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flush();
    expect(vi.mocked(API.requestTeam)).toHaveBeenCalledTimes(1);
  });
});

describe("the coordination's moderation queue", () => {
  const openTeamTab = async (
    role: "coordination" | "lead",
    requests: TeamRequest[],
  ) => {
    vi.mocked(API.me).mockResolvedValueOnce(boss(role));
    vi.mocked(API.team).mockResolvedValue({
      accounts: [],
      teams: [],
      departments: ["01", "02", "03"],
      requests,
    });
    await act(async () => {
      root.render(<Team config={CONFIG} />);
    });
    await until(() => text().includes("Mon équipe"), "the app opens");
    await click("Mon équipe");
    await until(() => text().includes("Les accès"), "the team screen");
  };

  it("opens the team under the name and the perimeter the coordination settled on", async () => {
    await openTeamTab("coordination", [PENDING]);
    await until(
      () => text().includes("Demandes d'équipe"),
      "the queue is on screen",
    );
    expect(text()).toContain("Référente Possible");

    vi.mocked(API.decideTeamRequest).mockResolvedValue({
      id: 7,
      decision: "accepted",
      team: 3,
      name: "Équipe Nord-Est",
      lead: "referente@exemple.fr",
      password: "mot-de-passe-provisoire",
    });
    // the coordination corrects both: the requester knows their department,
    // not the campaign's map
    await fill("Nom de l'équipe ouverte", "Équipe Nord-Est");
    await select("Départements accordés", ["01", "02"]);
    await confirmAddresses();
    // the queue comes back empty: the decided card unmounts under the button
    vi.mocked(API.team).mockResolvedValue({
      accounts: [],
      teams: [],
      departments: ["01", "02", "03"],
      requests: [{ ...PENDING, state: "accepted" }],
    });
    const accept = button("Accepter");
    await act(async () => {
      accept.focus();
    });
    await act(async () => {
      accept.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await until(
      () => text().includes("mot-de-passe-provisoire"),
      "the lead's password is shown once",
    );
    // SEQUENTIAL waits: the rescue's timers fire inside an act(), but React
    // only flushes the unmount at act boundaries
    for (const ms of [5, 40, 80]) {
      await act(async () => {
        await new Promise((r) => setTimeout(r, ms));
      });
    }

    expect(vi.mocked(API.decideTeamRequest)).toHaveBeenCalledWith(
      7,
      "accepted",
      {
        name: "Équipe Nord-Est",
        departments: ["01", "02"],
      },
    );
    // the accepted card left the pending list, and the button that was under
    // the reader's focus went with it
    expect(document.activeElement?.id).toBe("contenu");
  });

  it("is not served to a team lead, who reads the same screen", async () => {
    await openTeamTab("lead", []);
    expect(text()).not.toContain("Demandes d'équipe");
  });

  // The decided card unmounts when the RELOAD lands, not when the decision
  // answers. A rescue fired at the decision watches a button that is still
  // there, finds focus intact, and is done long before the unmount — so on
  // any server slower than 60 ms, which is every server, focus falls to
  // <body> and the next Tab restarts at the skip link.
  const decideWithASlowReload = async (
    label: string,
    answer: Awaited<ReturnType<typeof API.decideTeamRequest>>,
  ) => {
    await openTeamTab("coordination", [PENDING]);
    await until(() => text().includes("Demandes d'équipe"), "the queue");
    await confirmAddresses();

    vi.mocked(API.decideTeamRequest).mockResolvedValue(answer);
    // the reload the decision triggers takes 200 ms, as a round trip does
    vi.mocked(API.team).mockImplementation(
      () =>
        new Promise((resolve) =>
          setTimeout(
            () =>
              resolve({
                accounts: [],
                teams: [],
                departments: ["01", "02", "03"],
                requests: [
                  { ...PENDING, state: answer.decision as "accepted" },
                ],
              }),
            200,
          ),
        ),
    );

    const decide = button(label);
    await act(async () => {
      decide.focus();
    });
    await act(async () => {
      decide.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    for (const ms of [5, 40, 80, 150, 250, 300]) {
      await act(async () => {
        await new Promise((r) => setTimeout(r, ms));
      });
    }
  };

  it("keeps focus when the reload that unmounts the card is slower than the rescue", async () => {
    await decideWithASlowReload("Accepter", {
      id: 7,
      decision: "accepted",
      team: 3,
      name: "Équipe du 01",
      lead: "referente@exemple.fr",
      password: "mot-de-passe-provisoire",
    });
    expect(text(), "the card left the pending list").not.toContain(
      "Nom de l'équipe ouverte",
    );
    expect(document.activeElement?.id).toBe("contenu");
  });

  // A refusal opens no access, so it lands in no password card: with no
  // message either, the queue simply loses a card and a moderator who cannot
  // see the screen is told nothing at all.
  it("says out loud that a refusal was recorded", async () => {
    await decideWithASlowReload("Refuser", { id: 7, decision: "refused" });
    const spoken = [...container.querySelectorAll("[role='status']")]
      .map((n) => n.textContent ?? "")
      .join(" ");
    expect(spoken).toContain("refus");
    expect(document.activeElement?.id).toBe("contenu");
  });

  // One submit guard covers the whole list, but only the card in flight
  // wears aria-disabled: every other Accepter looks live, swallows the
  // click, and leaves the moderator believing they accepted it.
  it("does not leave a second decision looking live while one is in flight", async () => {
    const second: TeamRequest = { ...PENDING, id: 8, name: "Équipe du 02" };
    await openTeamTab("coordination", [PENDING, second]);
    await until(() => text().includes("Demandes d'équipe"), "the queue");
    await confirmAddresses();

    vi.mocked(API.decideTeamRequest).mockImplementation(
      () =>
        new Promise((resolve) =>
          setTimeout(() => resolve({ id: 7, decision: "refused" }), 200),
        ),
    );
    const [first, secondAccept] = [
      ...container.querySelectorAll("button"),
    ].filter((b) => b.textContent?.includes("Accepter"));
    await act(async () => {
      first.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(
      secondAccept.getAttribute("aria-disabled"),
      "the other card's button must not look live while the guard would eat it",
    ).toBe("true");
  });

  // The lead's password is returned ONCE and stored nowhere in the clear. The
  // reload that follows the acceptance can fail, and the queue is then never
  // refreshed: the password card lands beside the very request it answers.
  // A moderator reading that contradiction either presses Accepter again —
  // 409, « déjà traitée », so they conclude it never worked — or dismisses
  // the password as stale. Either way the team has a lead who cannot sign in.
  it("leaves no pending card beside the password when the reload fails", async () => {
    await openTeamTab("coordination", [PENDING]);
    await until(() => text().includes("Demandes d'équipe"), "the queue");
    await confirmAddresses();

    vi.mocked(API.decideTeamRequest).mockResolvedValue({
      id: 7,
      decision: "accepted",
      team: 3,
      name: "Équipe du 01",
      lead: "referente@exemple.fr",
      password: "mot-de-passe-provisoire",
    });
    vi.mocked(API.team).mockRejectedValue(new Error("réseau coupé"));

    const accept = button("Accepter");
    // the moderator's focus is ON the button they press: that is the whole
    // case, and holdFocusThrough rescues nobody who was holding nothing
    await act(async () => {
      accept.focus();
    });
    await act(async () => {
      accept.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });

    expect(text(), "the one-time password is on screen").toContain(
      "mot-de-passe-provisoire",
    );
    expect(
      text(),
      "the decided card must not still be waiting beside its own password",
    ).not.toContain("Nom de l'équipe ouverte");
    // and the control that died with the card did not take the focus with it
    expect(document.activeElement?.id).toBe("contenu");
  });

  // Accepting SENDS: an email signed by the campaign leaves for an address a
  // stranger typed, carrying a link that opens the lead's session. The button
  // stays inert until the coordination has said, on that card, that it read
  // the address. The mirror screen is guarded the same way.
  it("is inert until the coordination confirms the address", async () => {
    await openTeamTab("coordination", [PENDING]);
    await until(() => text().includes("Demandes d'équipe"), "the queue");

    const accept = button("Accepter");
    expect(
      accept.getAttribute("aria-disabled"),
      "an unconfirmed accept must not look live",
    ).toBe("true");
    await act(async () => {
      accept.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flush();
    expect(
      vi.mocked(API.decideTeamRequest).mock.calls.length,
      "the click was swallowed, and nothing was sent",
    ).toBe(0);

    await confirmAddresses();
    expect(button("Accepter").getAttribute("aria-disabled")).toBeNull();
  });

  // The lead's password is shown once and stored nowhere in the clear. It
  // lived in a SINGLE slot written by two flows — accepting a request, and
  // opening an access directly — each behind its own re-entry guard, so
  // neither saw the other. Accepting a second request before noting the
  // first password replaced it, and that is simply how a queue is worked
  // through. Appending is what makes losing one impossible.
  it("accepting a second request does not wipe the first password", async () => {
    const second: TeamRequest = { ...PENDING, id: 8, name: "Équipe du 02" };
    await openTeamTab("coordination", [PENDING, second]);
    await until(() => text().includes("Demandes d'équipe"), "the queue");
    await confirmAddresses();

    // a REAL server: a decided request comes back decided
    const settled = new Set<number>();
    vi.mocked(API.team).mockImplementation(async () => ({
      accounts: [],
      teams: [],
      departments: ["01", "02", "03"],
      requests: [PENDING, second].map((r) =>
        settled.has(r.id) ? { ...r, state: "accepted" as const } : r,
      ),
    }));
    vi.mocked(API.decideTeamRequest).mockImplementation((async (id: number) => {
      settled.add(id);
      return {
        id,
        decision: "accepted",
        team: id,
        name: id === 7 ? "Équipe du 01" : "Équipe du 02",
        lead: id === 7 ? "premiere@exemple.fr" : "seconde@exemple.fr",
        password: id === 7 ? "MOT-DE-PASSE-UN" : "MOT-DE-PASSE-DEUX",
      };
    }) as unknown as typeof API.decideTeamRequest);

    await act(async () => {
      button("Accepter").dispatchEvent(
        new MouseEvent("click", { bubbles: true }),
      );
    });
    await until(() => text().includes("MOT-DE-PASSE-UN"), "the first password");

    // …and now the second, WITHOUT pressing « j'ai noté » on the first
    await act(async () => {
      button("Accepter").dispatchEvent(
        new MouseEvent("click", { bubbles: true }),
      );
    });
    await until(
      () => text().includes("MOT-DE-PASSE-DEUX"),
      "the second password",
    );

    expect(
      text(),
      "the first password is shown once and nowhere else: it must survive",
    ).toContain("MOT-DE-PASSE-UN");
  });
});
