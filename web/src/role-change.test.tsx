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

const render = async (onError: (e: unknown) => void = () => {}) => {
  vi.mocked(API.team).mockResolvedValue(structuredClone(DATA));
  await act(async () => {
    root.render(
      <GestionEquipe
        onError={onError}
        me={ME}
        cfg={CONFIG}
        onCfg={() => {}}
        onMessage={() => {}}
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

it("promotes a volunteer to coordination and reloads the list", async () => {
  await render();
  vi.mocked(API.changeRole).mockResolvedValue({
    email: "benevole@exemple.fr",
    role: "coordination",
  });
  const b = buttonFor("promouvoir coordination", "Bénévole Motivée");
  expect(b).toBeTruthy();
  await act(async () => {
    b?.click();
  });
  await flush();
  expect(API.changeRole).toHaveBeenCalledWith(
    "benevole@exemple.fr",
    "coordination",
  );
  // reloaded: what the table shows is what the server now holds
  expect(vi.mocked(API.team).mock.calls.length).toBeGreaterThan(1);
});

it("offers to step a fellow coordinator down, and nothing on its own row", async () => {
  await render();
  expect(buttonFor("rendre bénévole", "Pair Coordination")).toBeTruthy();
  // one's own access carries NO action: neither deactivation nor demotion —
  // stepping oneself down is the server's 409 guard, not a button
  expect(buttonFor("rendre bénévole", "Moi Coordination")).toBeFalsy();
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
  const b = buttonFor("rendre bénévole", "Pair Coordination");
  await act(async () => {
    b?.click();
  });
  await flush();
  expect(seen).toHaveLength(1);
  expect(String(seen[0])).toContain("dernier accès de coordination");
});
