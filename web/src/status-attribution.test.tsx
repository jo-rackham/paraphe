// Who wrote the status you are reading — at TEAM granularity, never at the
// person's.
//
// A status crosses the teams of a campaign (that is what keeps two of them
// off the same mayor), and since writing one stopped claiming the card it was
// attributable to nobody. The team answers « who put that there » without a
// name of another team's crossing to say it.
//
// The whole decision lives in a comparison between two encodings of the same
// fact: the account says `team_id: null` for « no team », the card says `"0"`
// — and « nobody wrote this » is a THIRD answer that must not collapse into
// either. All three are here.

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as API from "./api.ts";
import Team, { equipeAyantEcrit } from "./Team.tsx";
import { teamConfig, who } from "./testing/fixtures.ts";
import type { Account, MayorCard, Me } from "./types.ts";

vi.mock("./api.ts", { spy: true });

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

const card = (fields: Partial<MayorCard> = {}): MayorCard =>
  ({
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
    ...fields,
  }) as MayorCard;

const member = (team: number | null): Account => ({
  ...who("moi@exemple.fr", "Moi").account,
  team_id: team,
  team_name: team === null ? null : "Sud",
});

describe("the team a status is attributed to", () => {
  it("names the team that wrote it, when it is not yours", () => {
    expect(
      equipeAyantEcrit(
        card({ updated_by_team: "7", updated_by_team_name: "Nord" }),
        member(3),
      ),
    ).toBe("l'équipe Nord");
  });

  it("says nothing about your own team", () => {
    expect(
      equipeAyantEcrit(
        card({ updated_by_team: "3", updated_by_team_name: "Sud" }),
        member(3),
      ),
    ).toBeNull();
  });

  // The account says null, the card says "0", and MyTeam() on the server
  // normalises the first into the second. Comparing them without doing the
  // same makes the national scope foreign to itself: every coordinator reads
  // « enregistré par l'équipe nationale » on their own writes.
  it("says nothing to the national scope about its own writes", () => {
    expect(
      equipeAyantEcrit(
        card({ updated_by_team: "0", updated_by_team_name: null }),
        member(null),
      ),
    ).toBeNull();
  });

  // …and the other half: the national scope must not collapse into « nobody
  // wrote this », which is what the number 0 does to any reader testing for
  // truthiness. The team that goes looking for who set « refusé » would find
  // nobody again — the exact defect the column exists to close.
  it("names the national scope, which has no team row hence no name", () => {
    expect(
      equipeAyantEcrit(
        card({ updated_by_team: "0", updated_by_team_name: null }),
        member(3),
      ),
    ).toBe("l'équipe nationale");
  });

  it("says nothing about a card statused before the column existed", () => {
    expect(equipeAyantEcrit(card(), member(3))).toBeNull();
    expect(
      equipeAyantEcrit(card({ updated_by_team: null }), member(3)),
    ).toBeNull();
  });

  // A real team whose name did not come back is not the national scope: that
  // is a different team, and naming it would be a wrong answer rather than
  // no answer.
  it("does not call a nameless team the national one", () => {
    expect(
      equipeAyantEcrit(
        card({ updated_by_team: "7", updated_by_team_name: null }),
        member(3),
      ),
    ).toBe("une autre équipe");
  });
});

// And the wiring: the card is where the question gets asked, so the sentence
// has to reach it — with the team's name and nothing else of that team's.
describe("the card of a mayor another team statused", () => {
  const CONFIG = teamConfig();
  let container: HTMLDivElement;
  let root: Root;

  const flush = () =>
    act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  const screen = () => container.textContent ?? "";

  async function until(pred: () => boolean, what: string) {
    for (let i = 0; i < 50; i++) {
      if (pred()) return;
      await flush();
    }
    throw new Error(`never happened: ${what}`);
  }

  const WRITTEN = card({
    status: "refused",
    updated_by_team: "7",
    updated_by_team_name: "Nord",
  });

  const SUD: Me = {
    ...who("moi@exemple.fr", "Moi", ["90"]),
    account: member(3),
  };

  beforeEach(() => {
    vi.mocked(API.detectMode).mockResolvedValue({
      kind: "team",
      config: CONFIG,
    });
    vi.mocked(API.me).mockResolvedValue(SUD);
    vi.mocked(API.dashboard).mockResolvedValue({
      stats: {},
      total: 1,
      departments_with_promise: [],
      departments_covered: 0,
      mine: [WRITTEN],
      team: [],
      departments: ["90"],
      by_rank: { has_endorsed: 1 },
      batch_size: 10,
    });
    vi.mocked(API.card).mockResolvedValue({ mayor: WRITTEN, notes: [] });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => {
      root.unmount();
    });
    container.remove();
    vi.resetAllMocks();
  });

  it("names the team that wrote, and nobody in it", async () => {
    await act(async () => {
      root.render(<Team config={CONFIG} />);
    });
    await until(() => screen().includes("Mon tableau"), "the app opens");
    await act(async () => {
      [...container.querySelectorAll<HTMLElement>("button, a")]
        .find((b) => b.textContent?.includes("Mon tableau"))!
        .dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await until(
      () => !!container.querySelector("table button.lien"),
      "the dashboard lists the card",
    );
    await act(async () => {
      container
        .querySelector<HTMLButtonElement>("table button.lien")!
        .dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await until(() => screen().includes("Bourg-Réel"), "the card opens");

    // What the Nord team's identity amounts to on this screen is its name:
    // the API answers nothing else about it, which is where that is pinned.
    expect(screen()).toContain("Dernier statut enregistré par l'équipe Nord");
  });
});
