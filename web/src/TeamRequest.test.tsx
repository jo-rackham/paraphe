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
});
