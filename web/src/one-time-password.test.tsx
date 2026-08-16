// A password shown ONCE must not be overwritten by a second press.
//
// Opening an access and approving a hosting request both answer with a
// generated password that is stored nowhere else — the card on screen is the
// only place it exists. Both handlers guarded themselves with STATE, which
// is a render behind: two clicks in the same tick run two handlers built by
// the same render, both read the flag as clear, and both fire. Two accounts
// or two campaigns are created, and the card keeps the LAST answer, so the
// first password is gone from the only screen that ever had it.
//
// `CLAUDE.md` states the rule these broke: a re-entry guard is a REF, never
// state. This file holds the two routes that mint a credential to it.

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as API from "./api.ts";
import { Moderation } from "./InstanceModeration.tsx";
import { GestionEquipe } from "./TeamAdmin.tsx";
import { teamConfig, who } from "./testing/fixtures.ts";

vi.mock("./api.ts", { spy: true });

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.resetAllMocks();
});

const text = () => host.textContent ?? "";
const button = (label: string): HTMLButtonElement | undefined =>
  [...host.querySelectorAll("button")].find((b) =>
    (b.textContent ?? "").includes(label),
  );

describe("a one-time password survives a double press", () => {
  it("opens ONE access however many times the button is pressed", async () => {
    vi.mocked(API.team).mockResolvedValue({
      accounts: [],
      teams: [],
      departments: ["90"],
    });
    let n = 0;
    vi.mocked(API.createAccount).mockImplementation(async () => {
      n += 1;
      return {
        email: `v${n}@exemple.fr`,
        name: "Bénévole",
        role: "volunteer",
        password: `MOTDEPASSE-${n}`,
        invitation_sent: false,
      };
    });

    await act(async () => {
      root.render(
        <GestionEquipe
          me={who("coord@exemple.fr", "Coordination")}
          cfg={teamConfig()}
          onCfg={() => {}}
          onError={() => {}}
          onMessage={() => {}}
        />,
      );
    });

    const form = host.querySelector("form");
    if (!form) throw new Error("no account form on screen");
    (form.querySelector('input[type="text"]') as HTMLInputElement).value = "x";
    await act(async () => {
      form.dispatchEvent(
        new Event("submit", { bubbles: true, cancelable: true }),
      );
      form.dispatchEvent(
        new Event("submit", { bubbles: true, cancelable: true }),
      );
    });

    expect(
      vi.mocked(API.createAccount).mock.calls.length,
      "two presses in one tick opened two accesses, and the card kept the " +
        "second password — the first volunteer's is gone from the only " +
        "screen it was ever on",
    ).toBe(1);
  });

  it("approves ONE hosting request however many times the button is pressed", async () => {
    vi.mocked(API.moderationQueue).mockResolvedValue({
      requests: [
        {
          id: 42,
          slug: "alpha",
          name: "Alpha",
          requester_email: "a@exemple.fr",
          requester_name: "A",
          state: "pending",
          ts: "2026-08-16",
          listed: true,
          message: "",
        },
      ],
      organisations: [],
      base_domain: "paraphe.test",
    } as never);
    let n = 0;
    vi.mocked(API.decideRequest).mockImplementation(async () => {
      n += 1;
      return {
        id: 42,
        slug: "alpha",
        decision: "accepted",
        address: `c${n}.paraphe.test`,
        coordination: `c${n}@exemple.fr`,
        password: `MOTDEPASSE-${n}`,
        invitation_sent: false,
      };
    });

    await act(async () => {
      root.render(<Moderation onMessage={() => {}} />);
    });
    const approve = button("Ouvrir");
    if (!approve) throw new Error(`no approve button on screen: ${text()}`);
    await act(async () => {
      approve.click();
      approve.click();
    });

    expect(
      vi.mocked(API.decideRequest).mock.calls.length,
      "two presses in one tick opened two campaigns, and the card kept the " +
        "second password — the first coordinator's is gone",
    ).toBe(1);
  });
});
