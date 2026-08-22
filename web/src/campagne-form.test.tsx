// The word beside the campaign save button, in both modes.
//
// The page banner lives at the top of a form long enough that the press
// happens off-screen from it, so both forms answer LOCALLY — and the local
// word obeys ONE rule, dirty first: a form retyped after a save is unsaved
// again, whatever was said in between. Written state-only on the team side,
// « Enregistré. » stood beside a field the coordination had just edited —
// a confirmation that lies, found by an adversarial round and red-proven
// before the derivation below existed.

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as API from "./api.ts";
import { CampaignTab } from "./BrowserCampagne.tsx";
import { GestionEquipe } from "./TeamAdmin.tsx";
import { teamConfig, who } from "./testing/fixtures.ts";
import type { Campaign, Templates } from "./types.ts";

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

const typeInto = (field: HTMLInputElement, value: string) =>
  act(async () => {
    const setter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )?.set;
    setter?.call(field, value);
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });

describe("the team campaign form's word beside its button", () => {
  const mount = async () => {
    vi.mocked(API.team).mockResolvedValue({
      accounts: [],
      teams: [],
      requests: [],
      departments: ["90"],
    });
    vi.mocked(API.updateCampaign).mockImplementation(async (campaign) => ({
      campaign,
      batch_size: 10,
      listed: true,
      phone_outreach: false,
      unfilled: [],
      name: "",
    }));
    const coord = who("coord@exemple.fr", "Coordination");
    coord.account.role = "coordination";
    coord.may_manage = true;
    await act(async () => {
      root.render(
        <GestionEquipe
          me={coord}
          cfg={teamConfig()}
          onCfg={() => {}}
          onMe={() => {}}
          onError={() => {}}
          onMessage={() => {}}
        />,
      );
    });
    const form = [...host.querySelectorAll("form")].find((f) =>
      (f.textContent ?? "").includes("La campagne"),
    );
    if (!form) throw new Error("no campaign form on screen");
    return form;
  };

  it("says nothing on open: a pristine form is neither saved nor dirty", async () => {
    const form = await mount();
    const span = form.querySelector('span[role="status"]');
    expect(span?.textContent, "a phantom marker on a form nobody touched").toBe(
      "",
    );
  });

  it("confirms a save, then yields to « modifications non enregistrées » the moment a field is retyped", async () => {
    const form = await mount();
    await act(async () => {
      form.dispatchEvent(
        new Event("submit", { bubbles: true, cancelable: true }),
      );
    });
    expect(form.textContent).toContain("Enregistré.");

    const batch = form.querySelector(
      'input[type="number"]',
    ) as HTMLInputElement;
    await typeInto(batch, "42");

    // DIRTY FIRST: the field just typed is NOT saved, and the previous
    // save's word must not stand beside it
    expect(
      form.textContent,
      "the confirmation outlived the edit it no longer describes",
    ).not.toContain("Enregistré.");
    expect(form.textContent).toContain("modifications non enregistrées");
  });
});

describe("the browser campaign form's word beside its button", () => {
  const DRAFT: Campaign = {
    candidat: "Camille",
    candidat_description: "desc",
    candidat_description_longue: "longue",
    signataire: "Vero",
    signataire_qualite: "coord",
    contact_tel: "01",
    contact_email: "a@b.fr",
    site: "https://exemple.fr",
    ville_envoi: "V",
  };
  const EMPTY_TPL: Templates = {};

  const mount = (props: {
    dirty: boolean;
    onSave: (c: Campaign, n: string, a: boolean) => void | Promise<void>;
    onErreur?: (m: string) => void;
  }) =>
    act(async () => {
      root.render(
        <CampaignTab
          draft={DRAFT}
          note=""
          dirty={props.dirty}
          logo=""
          onEdit={() => {}}
          onNote={() => {}}
          onLogo={() => {}}
          onErreur={props.onErreur ?? (() => {})}
          appelTelephonique={false}
          onAppelTelephonique={() => {}}
          templates={EMPTY_TPL}
          campaignTemplates={EMPTY_TPL}
          onTemplates={async (t) => t}
          onMessage={() => {}}
          onSave={props.onSave}
        />,
      );
    });

  const saveButton = () =>
    [...host.querySelectorAll("button")].find(
      (b) => (b.textContent ?? "").trim() === "Enregistrer",
    )!;

  it("stays on the dirty marker when the parent's save left the draft dirty", async () => {
    // the parent catches its own failure and does NOT update cfg: the child
    // stays dirty, and dirty wins over whatever the press got as far as
    await mount({ dirty: true, onSave: async () => {} });
    await act(async () => {
      saveButton().dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(host.textContent).toContain("modifications non enregistrées");
    expect(
      host.textContent,
      "with the draft dirty, the label must not claim it is saved",
    ).not.toContain("Enregistré.");
  });

  // The signature is `void | Promise<void>`: the current parent never
  // rejects, but the widened API invites a caller that does — and an
  // uncaught rejection in an async click handler surfaces nowhere a
  // volunteer looks. The child catches, tells onErreur, and does not say
  // « Enregistré. » about a save that threw.
  it("routes a rejecting onSave to onErreur instead of leaking the rejection", async () => {
    const said: string[] = [];
    const leaked: unknown[] = [];
    const listener = (ev: PromiseRejectionEvent) => {
      leaked.push(ev.reason);
      ev.preventDefault();
    };
    window.addEventListener("unhandledrejection", listener);
    try {
      await mount({
        dirty: false,
        onSave: () => Promise.reject(new Error("relais indisponible")),
        onErreur: (m) => said.push(m),
      });
      await act(async () => {
        saveButton().dispatchEvent(new MouseEvent("click", { bubbles: true }));
      });
      await act(async () => {
        await new Promise((r) => setTimeout(r, 10));
      });
      expect(said, "the fault is shown, not swallowed").toEqual([
        "relais indisponible",
      ]);
      expect(leaked, "an async handler must not leak a rejection").toEqual([]);
      expect(host.textContent).not.toContain("Enregistré.");
    } finally {
      window.removeEventListener("unhandledrejection", listener);
    }
  });
});
