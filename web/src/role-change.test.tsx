// The multi-coordination controls, tested through the RENDERED component: a
// coordinator promotes, steps a peer down, never sees an action on their own
// row — and the server's refusal (the last active coordinator) reaches the
// error channel instead of vanishing. The API is mocked: the guard itself is
// the server's and is pinned in api/role_change_test.go.

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import * as API from "./api.ts";
import { GestionEquipe } from "./TeamAdmin.tsx";
import { teamConfig, who } from "./testing/fixtures.ts";
import type { Me, TeamAccount, TeamData } from "./types.ts";

vi.mock("./api.ts", { spy: true });

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

const CONFIG = teamConfig();

const access = (
  email: string,
  name: string,
  role: TeamAccount["role"],
): TeamAccount => ({
  email,
  name,
  role,
  team_id: null,
  active: true,
  personal_note: "",
  team_name: null,
  created_at: "2026-01-01",
  created_by: "test",
  team: null,
});

const DATA: TeamData = {
  accounts: [
    access("moi@exemple.fr", "Moi Coordination", "coordination"),
    access("pair@exemple.fr", "Pair Coordination", "coordination"),
    access("benevole@exemple.fr", "Bénévole Motivée", "volunteer"),
  ],
  teams: [],
  departments: ["01"],
  requests: [],
};

const ME: Me = (() => {
  const m = who("moi@exemple.fr", "Moi Coordination");
  return { ...m, account: { ...m.account, role: "coordination" } };
})();

let container: HTMLDivElement;
let root: Root;

const flush = () =>
  act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });

beforeEach(() => {
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  // reset, not restore: `spy: true` mocks are removed by restoreAllMocks,
  // and the next test would then call the real API
  vi.resetAllMocks();
});

const render = async (
  onError: (e: unknown) => void = () => {},
  said?: { tone: string; text: string }[],
  teams: TeamData["teams"] = [
    {
      id: 7,
      name: "Nord",
      departments: "01",
      created_at: "2026-01-01",
      members: 0,
      reserved: 0,
    },
  ],
) => {
  vi.mocked(API.team).mockResolvedValue({
    ...structuredClone(DATA),
    teams: structuredClone(teams),
  });
  await act(async () => {
    root.render(
      <GestionEquipe
        onError={onError}
        me={ME}
        cfg={CONFIG}
        onCfg={() => {}}
        onMe={() => {}}
        onMessage={(m) => said?.push(m)}
      />,
    );
  });
  await flush();
};

const buttonFor = (label: string, name: string) =>
  [...container.querySelectorAll("button")].find(
    (b) =>
      (b.textContent ?? "").includes(label) &&
      (b.textContent ?? "").includes(name),
  );

/** The role control of ONE row, found by the name it is labelled with. */
const roleSelectFor = (name: string): HTMLSelectElement | undefined =>
  [...container.querySelectorAll("select")].find((sel) =>
    (sel.closest("label")?.textContent ?? "").includes(name),
  );

const chooseRole = async (name: string, role: string) => {
  const sel = roleSelectFor(name);
  if (!sel) throw new Error(`no role control for ${name}`);
  await act(async () => {
    sel.value = role;
    sel.dispatchEvent(new Event("change", { bubbles: true }));
  });
  await flush();
};

it("promotes a volunteer to coordination and reloads the list", async () => {
  await render();
  vi.mocked(API.changeRole).mockResolvedValue({
    email: "benevole@exemple.fr",
    role: "coordination",
  });
  await chooseRole("Bénévole Motivée", "coordination");
  expect(API.changeRole).toHaveBeenCalledWith(
    "benevole@exemple.fr",
    "coordination",
    undefined,
  );
  // reloaded: what the table shows is what the server now holds
  expect(vi.mocked(API.team).mock.calls.length).toBeGreaterThan(1);
});

// THE ROLE THE OLD CONTROL COULD NOT REACH. It swung coordination↔bénévole,
// so a campaign whose référent left could not name another one — though the
// server has always accepted it, and refuses a lead with no team. Opening a
// second account for somebody who already had one was the only way round.
it("names a référent, with the team the server requires", async () => {
  await render();
  vi.mocked(API.changeRole).mockResolvedValue({
    email: "benevole@exemple.fr",
    role: "lead",
  });
  await chooseRole("Bénévole Motivée", "lead");
  expect(API.changeRole).toHaveBeenCalledWith("benevole@exemple.fr", "lead", 7);
});

// …and a campaign with no team yet is told what to do, rather than sent to
// a refusal that reads « rôle inconnu » to whoever just picked it from a list.
it("refuses to name a référent when there is no team to lead", async () => {
  const said: { tone: string; text: string }[] = [];
  await render(() => {}, said, []);
  await chooseRole("Bénévole Motivée", "lead");
  expect(API.changeRole).not.toHaveBeenCalled();
  expect(said.map((m) => m.text).join(" ")).toContain("créez");
});

it("offers every role on a fellow coordinator, and none on its own row", async () => {
  await render();
  expect(roleSelectFor("Pair Coordination")).toBeTruthy();
  // one's own access carries NO action: neither deactivation nor demotion —
  // stepping oneself down is the server's 409 guard, not a control
  expect(roleSelectFor("Moi Coordination")).toBeFalsy();
  expect(buttonFor("désactiver", "Moi Coordination")).toBeFalsy();
});

it("hands the server's refusal to the error channel", async () => {
  const seen: unknown[] = [];
  await render((e) => seen.push(e));
  vi.mocked(API.changeRole).mockRejectedValue(
    new Error(
      "Impossible : ce compte est le dernier accès de coordination actif " +
        "de la campagne.",
    ),
  );
  await chooseRole("Pair Coordination", "volunteer");
  expect(seen).toHaveLength(1);
  expect(String(seen[0])).toContain("dernier accès de coordination");
});
