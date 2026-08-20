// A campaign writes its own texts, and each of its teams writes its own on
// top. What is EMPTY is inherited — and the screen has to keep it empty, or
// the whole design is a trap.
//
// The API is mocked: the refusals themselves are the server's and are pinned
// in api/templates_test.go. What is tested here is the layering, and the one
// thing only this end can get wrong.

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as API from "./api.ts";
import { Fiche } from "./common.tsx";
import * as M from "./messages.ts";
import { readOffer } from "./prefill.ts";
import { ModelesMessages } from "./Templates.tsx";
import { teamConfig } from "./testing/fixtures.ts";
import type { Mayor, Templates } from "./types.ts";

// An ENDORSER, so the card renders `courrier.txt` and not its discovery
// twin: the rank chooses the file, and that choice stays the engine's
// whoever wrote the text.
const ENDORSER: Mayor = {
  insee_code: "01001",
  commune: "Artemare",
  department: "Ain",
  last_name: "DESCHAMPS",
  first_name: "Roland",
  title: "M.",
  rank: "has_endorsed",
  recent_candidate: "Anasse Kazib",
  recent_year: "2022",
  endorsement_history: "2022: KAZIB Anasse (A)",
};

vi.mock("./api.ts", { spy: true });

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

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
  vi.resetAllMocks();
});

const editor = async (
  niveau: "campaign" | "team" | "navigateur",
  propres: Templates,
  herites: Templates = {},
  onEnregistre: (t: Templates) => void = () => {},
  onSave: (t: Templates) => Promise<Templates> = async (t) =>
    niveau === "team"
      ? (await API.updateTeamTemplates(t)).templates
      : (await API.updateCampaignTemplates(t)).templates,
) => {
  await act(async () => {
    root.render(
      <ModelesMessages
        niveau={niveau}
        propres={propres}
        herites={herites}
        onSave={onSave}
        onEnregistre={onEnregistre}
        onError={() => {}}
        onMessage={() => {}}
      />,
    );
  });
  await flush();
};

const box = () =>
  container.querySelector("#modele-texte") as HTMLTextAreaElement;

const choose = async (file: string) => {
  const select = container.querySelector("#modele-choisi") as HTMLSelectElement;
  await act(async () => {
    select.value = file;
    select.dispatchEvent(new Event("change", { bubbles: true }));
  });
};

const press = async (label: string) => {
  const b = [...container.querySelectorAll("button")].find((x) =>
    (x.textContent ?? "").includes(label),
  );
  if (!b) throw new Error(`no button « ${label} »`);
  await act(async () => b.click());
  await flush();
};

// THE ONE THING ONLY THIS END CAN GET WRONG.
//
// Shown as the VALUE of the box, the inherited text is a copy the moment
// anybody presses Enregistrer — and every later correction the coordination
// makes stops reaching that team, silently, because the team now has a
// template of its own that happens to be identical. As a placeholder it stays
// visible and stays inherited.
it("shows the inherited text without ever putting it in the box", async () => {
  await editor("team", {}, { "courrier.txt": "Texte de la campagne." });
  await choose("courrier.txt");
  expect(box().value).toBe("");
  expect(box().placeholder).toBe("Texte de la campagne.");

  // and where the level above has written nothing, the image's own text —
  // for an email, split the way the fields are: the subject line in the
  // subject's placeholder, the body in the box's
  await editor("campaign", {});
  await choose("email.txt");
  expect(box().value).toBe("");
  const shipped = M.SHIPPED_TEMPLATES["email.txt"];
  const firstLine = shipped.slice(0, shipped.indexOf("\n"));
  expect(subject().placeholder).toBe(firstLine.slice("OBJET:".length).trim());
  // the two placeholders recompose the shipped file byte for byte: what the
  // fields show apart is exactly what one box used to show whole
  expect(`OBJET: ${subject().placeholder}\n\n${box().placeholder}`).toBe(
    shipped,
  );
});

// -- The email subject, a field of its own ----------------------------------
//
// Stored, the subject stays the template's first line — the one-file format
// the engine and the mass mailing share. Typed, it is its own field: asking
// people to open their text with an exact uppercase « OBJET: » was a trap,
// and the refusal it tripped named a format nobody chose.

const subject = () =>
  container.querySelector("#modele-objet") as HTMLInputElement;

const type = async (el: HTMLInputElement | HTMLTextAreaElement, v: string) => {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(
      el instanceof HTMLInputElement
        ? HTMLInputElement.prototype
        : HTMLTextAreaElement.prototype,
      "value",
    )?.set;
    setter?.call(el, v);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
};

it("composes the stored OBJET line from the two fields", async () => {
  vi.mocked(API.updateCampaignTemplates).mockResolvedValue({ templates: {} });
  await editor("campaign", {});
  await choose("email.txt");
  await type(subject(), "Un rendez-vous en 2027");
  await type(box(), "Bonjour {salutation}.\n");
  await press("Enregistrer");
  const sent = vi.mocked(API.updateCampaignTemplates).mock.calls[0][0];
  expect(sent["email.txt"]).toBe(
    "OBJET: Un rendez-vous en 2027\n\nBonjour {salutation}.\n",
  );
});

it("decomposes a stored email into the two fields, and keeps its bytes untouched", async () => {
  // single newline after the subject, as an older overlay may hold: shown
  // decomposed, and NOT recomposed behind its owner's back — an untouched
  // file must not read as modified
  await editor("campaign", { "email.txt": "OBJET: Ancien objet\ncorps\n" });
  await choose("email.txt");
  expect(subject().value).toBe("Ancien objet");
  expect(box().value).toBe("corps\n");
  expect(container.textContent).not.toContain("modifications non enregistrées");
});

it("emptying both fields sends the empty value that means « inherit »", async () => {
  // the shape a cleared box has always sent: an empty value, which BOTH ends
  // read as the override removed — the server stores nothing and answers
  // without the key
  vi.mocked(API.updateCampaignTemplates).mockResolvedValue({ templates: {} });
  await editor("campaign", { "email.txt": "OBJET: X\n\ncorps\n" });
  await choose("email.txt");
  await type(subject(), "");
  await type(box(), "");
  await press("Enregistrer");
  const sent = vi.mocked(API.updateCampaignTemplates).mock.calls[0][0];
  expect(sent["email.txt"]).toBe("");
});

// The refusal, IN THE CARD. The page-level banner lives at the top of a long
// screen: it speaks to a screen reader and misses the eye of whoever is
// scrolled down to the editor — a refused save whose reason shows nowhere
// visible was reported as « nothing happens ».
it("shows a refused save in the card's own slot", async () => {
  await editor(
    "campaign",
    {},
    {},
    () => {},
    async () => {
      throw new Error(
        "le modèle « email.txt » doit commencer par une ligne « OBJET: … »",
      );
    },
  );
  await choose("courrier.txt");
  await type(box(), "Un texte.");
  await press("Enregistrer");
  const alert = container.querySelector('form [role="alert"]');
  expect(alert?.textContent).toContain("doit commencer par une ligne");
});

it("saves nothing for a level that has touched nothing", async () => {
  vi.mocked(API.updateTeamTemplates).mockResolvedValue({ templates: {} });
  await editor("team", {}, { "courrier.txt": "Texte de la campagne." });
  await press("Enregistrer");
  expect(API.updateTeamTemplates).toHaveBeenCalledWith({});
});

it("sends the campaign's overlay to the campaign route and the team's to its own", async () => {
  vi.mocked(API.updateCampaignTemplates).mockResolvedValue({ templates: {} });
  await editor("campaign", { "courrier.txt": "x" });
  await press("Enregistrer");
  expect(API.updateCampaignTemplates).toHaveBeenCalled();
  expect(API.updateTeamTemplates).not.toHaveBeenCalled();
});

// « Revenir au texte fourni » REMOVES the key. Sent as an empty string it
// would be an override that renders one blank page, five hundred times — and
// the server reads an empty value the same way for that reason, so this is
// the two ends agreeing rather than one of them carrying the rule.
it("drops the key when the text goes back to the inherited one", async () => {
  vi.mocked(API.updateTeamTemplates).mockResolvedValue({ templates: {} });
  await editor("team", { "courrier.txt": "propre à l'équipe" });
  await choose("courrier.txt");
  expect(box().value).toBe("propre à l'équipe");
  await press("Revenir au texte");
  expect(box().value).toBe("");
  await press("Enregistrer");
  const sent = vi.mocked(API.updateTeamTemplates).mock.calls[0][0];
  expect(Object.hasOwn(sent, "courrier.txt")).toBe(false);
});

// What comes BACK is what the screen shows, not what went out: an emptied
// text is an override removed, and the box has to fall back to the inherited
// one rather than keep the string the person deleted.
it("takes the stored overlay from the answer, not from what it sent", async () => {
  vi.mocked(API.updateTeamTemplates).mockResolvedValue({ templates: {} });
  const seen: Templates[] = [];
  await editor("team", { "courrier.txt": "   " }, {}, (t) => seen.push(t));
  await press("Enregistrer");
  expect(seen).toEqual([{}]);
});

// The card is what a mayor actually receives, and it renders from the layers
// in order: the team's over the campaign's over the image's.
it("renders a card from the team's text, then the campaign's, then the image's", async () => {
  const cfg = teamConfig().campaign;
  const render = async (templates: (Templates | undefined)[]) => {
    await act(async () => {
      root.render(
        <Fiche
          mayor={ENDORSER}
          cfg={cfg}
          templates={templates}
          onBack={() => {}}
          onStatus={() => {}}
        />,
      );
    });
    await flush();
    return container.textContent ?? "";
  };

  const campaign = { "courrier.txt": "Lettre de la campagne.\n" };
  const team = { "courrier.txt": "Lettre de l'équipe Nord.\n" };

  expect(await render([campaign, {}])).toContain("Lettre de la campagne.");
  expect(await render([campaign, team])).toContain("Lettre de l'équipe Nord.");
  // a team that rewrote nothing keeps following the campaign — including
  // when the campaign changes its text afterwards
  expect(await render([campaign, { "email.txt": "OBJET: x\ny\n" }])).toContain(
    "Lettre de la campagne.",
  );
  // and with no layer at all, the image's own letter
  expect(await render([])).toContain(cfg.signataire);
});

// THE ACCOUNT-LESS VERSION, which has no server to ask and no live link to
// the campaign it adopted.
describe("the templates a campaign hands to the browser version", () => {
  const answer = (templates: unknown) => ({
    slug: "campagne",
    name: "Campagne",
    campaign: Object.fromEntries(
      M.CAMPAIGN_KEYS.map((k) => [k, `valeur de ${k}`]),
    ),
    templates,
  });

  it("carries the campaign's own texts through the offer", () => {
    const offer = readOffer(answer({ "courrier.txt": "Notre lettre.\n" }));
    expect(offer.templates).toEqual({ "courrier.txt": "Notre lettre.\n" });
  });

  it("adopts the shipped texts when the campaign has rewritten none", () => {
    expect(readOffer(answer({})).templates).toEqual({});
    expect(readOffer(answer(undefined)).templates).toEqual({});
  });

  // FILTERED, not refused: a key outside the six is a text nothing renders,
  // and throwing would refuse a whole campaign — nine fields and a logo — over
  // one stray key that changes nothing.
  it.each([
    ["a name nothing renders", { "courriel.txt": "x" }],
    ["a value that is not a string", { "courrier.txt": 42 }],
    ["an empty text, which means inherit", { "courrier.txt": "   " }],
  ])("drops %s rather than refusing the campaign", (_case, templates) => {
    const offer = readOffer(answer(templates));
    expect(offer.templates).toEqual({});
    // …and the nine values still came through, which is the point
    expect(offer.campaign.candidat).toBe("valeur de candidat");
  });

  // THE ONE BOUND THAT IS NOT COSMETIC. This mode stores what it adopts and
  // promises to hold only what its owner put there; a campaign answering with
  // a megabyte per file would fill a volunteer's disk on one click.
  it("refuses a template past the size the server itself applies", () => {
    const huge = "é".repeat(M.MAX_TEMPLATE_RUNES + 1);
    expect(readOffer(answer({ "courrier.txt": huge })).templates).toEqual({});
    const atTheLimit = "é".repeat(M.MAX_TEMPLATE_RUNES);
    expect(readOffer(answer({ "courrier.txt": atTheLimit })).templates).toEqual(
      { "courrier.txt": atTheLimit },
    );
  });

  // No server here to reproduce the engine's rules, so the engine is asked
  // directly — and at BOTH ranks, because a template is chosen by rank and
  // checking one leaves the other to be found by a mayor.
  it.each([
    ["an unknown placeholder", { "courrier.txt": "Bonjour {prénom}." }],
    [
      "the other rank's placeholder",
      { "courrier_decouverte.txt": "En {annee_recente}." },
    ],
    ["an email with no subject", { "email.txt": "Bonjour {salutation}." }],
  ])("says why %s cannot be used", (_case, overlay) => {
    const why = M.invalidTemplate(
      M.mergeTemplates(M.SHIPPED_TEMPLATES, overlay),
    );
    expect(why).toBeTruthy();
  });

  it("accepts the texts the campaign actually sends", () => {
    expect(M.invalidTemplate(M.SHIPPED_TEMPLATES)).toBeNull();
    expect(
      M.invalidTemplate(
        M.mergeTemplates(M.SHIPPED_TEMPLATES, {
          "courrier.txt": "Bonjour {salutation} de {commune_de}.\n",
        }),
      ),
    ).toBeNull();
  });
});
