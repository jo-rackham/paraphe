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
      requests: [],
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
          onMe={() => {}}
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
    // approving SENDS a session link to an address a stranger typed: the
    // button stays inert until the moderator confirms having read it
    for (const l of host.querySelectorAll("label")) {
      if (!l.textContent?.includes("J'ai vérifié")) continue;
      const box = l.querySelector<HTMLInputElement>('input[type="checkbox"]');
      if (box && !box.checked) {
        await act(async () => {
          box.click();
        });
      }
    }
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

  // The THIRD flow that mints one. The rule is written once in the screen's
  // `showPassword`, and this is what keeps the next flow from re-deriving it.
  it("opens ONE coordination access however many times the button is pressed", async () => {
    mockHostedCampaign();
    let n = 0;
    vi.mocked(API.grantCoordination).mockImplementation(async () => {
      n += 1;
      return {
        slug: "alpha",
        address: "alpha.paraphe.test",
        coordination: `c${n}@exemple.fr`,
        password: `MOTDEPASSE-${n}`,
        invitation_sent: false,
      };
    });

    await act(async () => {
      root.render(<Moderation onMessage={() => {}} />);
    });
    const form = grantForm();
    await act(async () => {
      form.dispatchEvent(
        new Event("submit", { bubbles: true, cancelable: true }),
      );
      form.dispatchEvent(
        new Event("submit", { bubbles: true, cancelable: true }),
      );
    });

    expect(
      vi.mocked(API.grantCoordination).mock.calls.length,
      "two presses in one tick opened two accesses, and the screen kept the " +
        "second password — the first is gone from the only place it existed",
    ).toBe(1);
  });

  // Two cards CAN name one campaign: an access opened on a campaign created a
  // moment ago, or a second one opened after the first was mislaid. So the
  // card's key is a counter, not the address.
  //
  // What this asserts is React's own complaint, and that is deliberate. Keyed
  // by the address the two cards still rendered, and dismissing one still
  // left the other — measured, both ways. The defect is the one React states:
  // duplicate keys are "unsupported and could change", so the loss is a
  // version away rather than here today. An assertion about passwords on
  // screen would have been green under the bug and would have taught the next
  // reader that the key does not matter.
  it("gives each password card a key of its own", async () => {
    const complaints: string[] = [];
    const spy = vi
      .spyOn(console, "error")
      .mockImplementation((...a: unknown[]) => complaints.push(String(a[0])));
    mockHostedCampaign();
    let n = 0;
    vi.mocked(API.grantCoordination).mockImplementation(async () => {
      n += 1;
      return {
        slug: "alpha",
        address: "alpha.paraphe.test",
        coordination: `c${n}@exemple.fr`,
        password: `MOTDEPASSE-${n}`,
        invitation_sent: false,
      };
    });

    await act(async () => {
      root.render(<Moderation onMessage={() => {}} />);
    });
    for (let i = 0; i < 2; i++) {
      const form = grantForm();
      await act(async () => {
        form.dispatchEvent(
          new Event("submit", { bubbles: true, cancelable: true }),
        );
      });
    }

    expect(vi.mocked(API.grantCoordination).mock.calls.length).toBe(2);
    expect(text()).toContain("MOTDEPASSE-1");
    expect(text()).toContain("MOTDEPASSE-2");
    expect(
      complaints.filter((c) => c.includes("same key")),
      "two password cards share a React key: the address repeats across the " +
        "three flows that mint one, so the key cannot be the address",
    ).toEqual([]);
    spy.mockRestore();
  });
});

// A queue with one hosted campaign, which is what the access form needs to
// offer anything at all.
function mockHostedCampaign() {
  vi.mocked(API.moderationQueue).mockResolvedValue({
    requests: [],
    organisations: [
      {
        id: 1,
        slug: "alpha",
        name: "Alpha",
        state: "active",
        created_at: "2026-08-16",
      },
    ],
    base_domain: "paraphe.test",
  } as never);
}

// The access form is the one with a campaign chooser: the creation form
// beside it has none, and picking « the second form » would follow whichever
// order the screen happens to render them in.
function grantForm(): HTMLFormElement {
  const form = [...host.querySelectorAll("form")].find((f) =>
    f.querySelector("select"),
  );
  if (!form) throw new Error(`no access form on screen: ${text()}`);
  return form;
}
