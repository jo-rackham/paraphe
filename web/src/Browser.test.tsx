// The pre-fill banner, tested through the RENDERED component.
//
// prefill.test.ts proves the predicate; none of that reaches the wiring,
// and the wiring is where every defect in this feature has lived: the
// guard computed once at mount, the warning suppressed by `&& !offer`,
// the refusal forgotten on reload. Each test here goes red when its
// defect is reinstated in Browser.tsx — that is its acceptance criterion.

import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CAMPAIGN_KEYS } from "../../noyau/messages.ts";
import Browser from "./Browser.tsx";
import { CAMPAIGN_FIELDS, EMPTY_CFG } from "./common.tsx";
import * as DB from "./db.ts";

// spy mode: real IndexedDB behavior everywhere, but a single test can make
// one write fail — the failure path has no other way to be exercised
vi.mock("./db.ts", { spy: true });

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

const OFFERED = Object.fromEntries(
  CAMPAIGN_KEYS.map((k) => [k, `valeur offerte de ${k}`]),
);

// one mayor already stored: the mount effect then skips the list download,
// and the only network request left is the campaign offer
const MAYOR = {
  // 90001: the code the demo set also uses — an INSEE names a COMMUNE,
  // and two lists can seat two different mayors under it
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
};

let container: HTMLDivElement;
let root: Root;

const flush = () =>
  act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });

/**
 * Waits, one macrotask at a time, until the screen shows a condition. A
 * single flush is a race: the mount effect chains five IndexedDB reads
 * and a fetch, and under CI load one tick is not always enough — the
 * suite failed one run in ten, making a real regression look like noise.
 */
async function until(pred: () => boolean, what: string) {
  for (let i = 0; i < 50; i++) {
    if (pred()) return;
    await flush();
  }
  throw new Error(`never happened: ${what}`);
}

/**
 * Renders Browser under ?org=campagne, ASKS for the proposal, and waits for
 * it to land.
 *
 * The click is not ceremony: this mode promises no request leaves the
 * browser, and the promise says it must be verifiable in the network tab.
 * The link NAMES a campaign; fetching it is the volunteer's decision, and
 * nothing before that click touches the network.
 */
async function renderAndSettle() {
  await act(async () => {
    root.render(<Browser />);
  });
  await until(
    () => text().includes("Ce lien propose une campagne"),
    "the ask appears",
  );
}

async function renderWithOffer() {
  await renderAndSettle();
  await click("Voir cette proposition");
  await until(
    () => text().includes("Reprendre la campagne"),
    "the offer lands",
  );
}

const text = () => container.textContent ?? "";

function button(label: string): HTMLButtonElement {
  const b = [...container.querySelectorAll("button")].find((el) =>
    el.textContent?.includes(label),
  );
  if (!b) throw new Error(`no button « ${label} » on screen`);
  return b;
}

function click(label: string) {
  return act(async () => {
    button(label).dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
}

/** Types into a controlled input or textarea the way a browser does. */
function type(input: HTMLInputElement | HTMLTextAreaElement, value: string) {
  const proto =
    input instanceof HTMLTextAreaElement
      ? window.HTMLTextAreaElement.prototype
      : window.HTMLInputElement.prototype;
  const set = Object.getOwnPropertyDescriptor(proto, "value")!.set!;
  return act(async () => {
    set.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

/** The email body on an open card: the only 16-row textarea. */
const emailBody = () =>
  container.querySelector<HTMLTextAreaElement>("textarea[rows='16']")!;

/** Loads one CSV row through the app's own import, without leaving the page. */
async function loadCsv(row: string) {
  const csv = [
    "insee_code;first_name;last_name;commune;department;rank;score;" +
      "recent_candidate;recent_year;democratic_theme_endorsement;title",
    row,
  ].join("\r\n");
  const input = [
    ...container.querySelectorAll<HTMLInputElement>("input[type=file]"),
  ].find((el) => el.accept.includes("csv"))!;
  Object.defineProperty(input, "files", {
    value: [new File([csv], "liste.csv", { type: "text/csv" })],
    configurable: true,
  });
  await act(async () => {
    input.dispatchEvent(new Event("change", { bubbles: true }));
  });
  await until(
    () => text().includes("depuis votre disque"),
    "the list is replaced",
  );
}

const firstCampaignField = () =>
  container.querySelector<HTMLInputElement>("#champ-candidat")!;

beforeEach(async () => {
  vi.stubEnv("PARAPHE_INSTANCE_DOMAIN", "paraphe.fr");
  // TWO answers, because there are two doors and this file is about the
  // second. The ORIGIN serving these tests is a static publication — the
  // GitHub Pages case — so its own `/api/campaign/public` answers nothing,
  // and every offer below is one a LINK made. A stub that answered both
  // alike would have this build adopt the campaign at load and no offer
  // would ever appear, which is exactly what happened when it did.
  vi.stubGlobal("fetch", (url: string) =>
    Promise.resolve(
      String(url).startsWith("/")
        ? { ok: false, status: 404, json: () => Promise.resolve({}) }
        : {
            ok: true,
            json: () =>
              Promise.resolve({
                slug: "campagne",
                name: "Camille Réel",
                campaign: OFFERED,
              }),
          },
    ),
  );
  window.history.replaceState({}, "", "/?org=campagne");
  await DB.eraseAll();
  await DB.replaceMayors([MAYOR]);
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => {
    root.unmount();
  });
  container.remove();
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
});

// The guard on the way OUT of IndexedDB, which nothing tested: both
// mutations that removed it — one per read site — left the whole suite
// green. A database written by an older build, by another tab, or by a
// tampered backup is not this code's doing, and this mode is published on
// GitHub Pages where no Content-Security-Policy catches what it renders.
describe("a logo already in the database", () => {
  // through renderAndSettle: the ?org= ask is the LAST thing the mount
  // effect does, strictly after the logo is read, so its appearing is the
  // proof that the read has happened — an assertion made before it would
  // pass on an empty page whatever the guard does. It used to wait for the
  // offer itself, which meant a network round trip this describe never
  // needed; the ask is the same proof without one.
  const stored = async (value: unknown) => {
    await DB.writeSetting("logo", value as string);
    await renderAndSettle();
    return [...container.querySelectorAll("img")].map((i) =>
      i.getAttribute("src"),
    );
  };

  it("is shown when it is an inline image", async () => {
    expect(await stored("data:image/png;base64,iVBORw0KGgo=")).toContain(
      "data:image/png;base64,iVBORw0KGgo=",
    );
  });

  it("is dropped when it is a remote address, on the way out", async () => {
    for (const hostile of [
      "https://tracker.attaquant.example/pixel.gif?qui=victime",
      "//ailleurs.example/x.png",
      "javascript:alert(1)",
      "data:text/html,<script>alert(1)</script>",
    ]) {
      await DB.eraseAll();
      await DB.replaceMayors([MAYOR]);
      expect(await stored(hostile), hostile).not.toContain(hostile);
    }
  });
});

describe("the offer banner, rendered", () => {
  // The promise this mode makes is not « we only send what is needed », it
  // is that NOTHING leaves — and index.html says it must be verifiable in
  // the network tab rather than asserted. A ?org= link naming a campaign
  // used to be fetched by the mount effect, so opening a link shared
  // publicly rang <slug>.<instance> with the visitor's address before any
  // click. Counted here, because a comment cannot hold that line.
  // THE CAMPAIGN THIS ORIGIN IS, and no clicks at all.
  //
  // Served under /navigateur/ by `<slug>.paraphe.org`, this build is that
  // campaign's own account-less version. Whoever opens it wants its texts,
  // and asking them to accept the texts of the site they are standing on is
  // a question with one answer — asked twice, it read as a tool that had
  // failed to substitute anything, because the example values it left on
  // screen are written to look exactly like placeholders.
  describe("the campaign this origin is", () => {
    /** An instance serving its own browser version, with no ?org= at all. */
    const servedByCampaign = async (
      campaign: Record<string, string> = OFFERED,
    ) => {
      window.history.replaceState({}, "", "/");
      vi.stubGlobal("fetch", (url: string) =>
        Promise.resolve(
          String(url).startsWith("/api/campaign/public")
            ? {
                ok: true,
                json: () =>
                  Promise.resolve({
                    slug: "sienne",
                    name: "Camille Sienne",
                    campaign,
                  }),
              }
            : { ok: false, status: 404, json: () => Promise.resolve({}) },
        ),
      );
      await act(async () => {
        root.render(<Browser />);
      });
    };

    it("fills the campaign in with no click and no offer", async () => {
      await servedByCampaign();
      // a POSITIVE signal: waiting for the banner to be ABSENT is satisfied
      // by the loading screen, before anything has been decided, and would
      // pass whatever the adoption does
      await until(
        () => text().includes("Camille Sienne"),
        "the campaign lands on its own",
      );
      expect(text()).not.toContain("Campagne non configurée");
      // never the offer: there is nothing to confirm about the site you
      // are already on
      expect(text()).not.toContain("Ce lien propose une campagne");
      expect(text()).not.toContain("Reprendre la campagne");
      // and it is STORED, not merely on screen: a claim about the base,
      // which every mutation that dropped the write left green
      const stored = await DB.readSetting<Record<string, string>>(
        "campagne",
        {},
      );
      expect(stored.candidat).toBe(OFFERED.candidat);
    });

    it("says where the texts came from", async () => {
      await servedByCampaign();
      await until(
        () => text().includes("Camille Sienne"),
        "the screen names the campaign it took",
      );
      // the values go out in every message to a mayor: where they came
      // from is not a detail to leave unsaid
      expect(text()).toContain("depuis son site");
    });

    // What the volunteer typed is theirs, whatever the origin says.
    it("never replaces a campaign already in this browser", async () => {
      await DB.writeSetting("campagne", { ...OFFERED, candidat: "Déjà Saisi" });
      await servedByCampaign();
      // the LIST is the positive signal that the mount effect has run to
      // the end: it renders after the campaign decision, not before
      await until(() => text().includes("Bourg-Réel"), "the app opens");
      const stored = await DB.readSetting<Record<string, string>>(
        "campagne",
        {},
      );
      expect(stored.candidat).toBe("Déjà Saisi");
    });

    // A static publication has no campaign to be: its own /api answers
    // HTML or 404, and nothing must be adopted from that.
    it("adopts nothing when the origin serves no campaign", async () => {
      window.history.replaceState({}, "", "/");
      vi.stubGlobal("fetch", () =>
        Promise.resolve({
          ok: true,
          json: () => Promise.resolve({ page: "<!doctype html>" }),
        }),
      );
      await act(async () => {
        root.render(<Browser />);
      });
      await until(
        () => text().includes("Bourg-Réel"),
        "the app opens with nothing adopted",
      );
      expect(text()).toContain("Campagne non configurée");
      const stored = await DB.readSetting<Record<string, string>>(
        "campagne",
        {},
      );
      expect(stored.candidat ?? "").not.toBe(OFFERED.candidat);
    });

    // WHO SIGNS DOES NOT TRAVEL. The other seven describe the candidate and
    // how to reach the campaign and exist to be handed over; these two name a
    // PERSON — and the person who filled the campaign form is the
    // coordination. Adopted with the rest, every message a volunteer produced
    // in this mode went out over the coordinator's name and role, and nothing
    // said so. Team mode never showed it: there, each account supplies its own.
    it("takes the campaign's texts but never its signatory", async () => {
      await servedByCampaign();
      await until(
        () =>
          text().includes("Campagne d’essai") ||
          text().includes("repris depuis son site"),
        "the campaign lands",
      );
      const stored = await DB.readSetting<Record<string, string>>(
        "campagne",
        {},
      );
      // the campaign's own values: taken
      expect(stored.candidat).toBe(OFFERED.candidat);
      expect(stored.contact_email).toBe(OFFERED.contact_email);
      // the person's: NOT taken, and left at the template value the form
      // shows, so the screen can ask for them
      expect(stored.signataire).not.toBe(OFFERED.signataire);
      expect(stored.signataire_qualite).not.toBe(OFFERED.signataire_qualite);
      expect(stored.signataire).toBe(EMPTY_CFG.signataire);
    });

    // …and the reader is TOLD, in the words that fit what is actually
    // missing. « Campagne non configurée » would send them looking for a
    // candidate already on their screen, and never say the signature at the
    // bottom is theirs to give.
    it("asks the volunteer to sign for themselves", async () => {
      await servedByCampaign();
      await until(
        () => text().includes("Signez de votre nom"),
        "the screen asks for a signature",
      );
      expect(text()).not.toContain("Campagne non configurée");
    });

    // A campaign may give a telephone number to nobody, run without a
    // website and not name the town its letters leave from. The API applies
    // that rule before answering 200 — so a build that then calls the same
    // answer "not a complete campaign" pre-fills NOTHING, and says nothing.
    //
    // Reported from production, on a configured campaign whose account
    // version substitutes correctly: « Prénom NOM » on screen under a
    // « Campagne non configurée » banner, which reads as an engine that
    // failed to substitute rather than as two ends disagreeing.
    it.each(["contact_tel", "site", "ville_envoi"])(
      "takes a campaign that left %s empty, as the API says it may",
      async (key) => {
        await servedByCampaign({ ...OFFERED, [key]: "" });
        await until(
          () => text().includes("Camille Sienne"),
          `the campaign lands with ${key} empty`,
        );
        const stored = await DB.readSetting<Record<string, string>>(
          "campagne",
          {},
        );
        expect(stored.candidat).toBe(OFFERED.candidat);
        expect(stored[key]).toBe("");
      },
    );

    // Absence is silent — an apex, a static host, a captive portal. An
    // ANSWER this build cannot take is not absence, and swallowing it is
    // exactly how the disagreement above survived a release without one
    // word on screen.
    it("says so when the origin answers something it cannot take", async () => {
      // campaign-shaped, and unusable: a required key blank
      await servedByCampaign({ ...OFFERED, candidat: "" });
      await until(
        () => text().includes("n'ont pas pu être repris"),
        "the refusal is said out loud",
      );
      // …and it is not a dead end: the tool works, the texts are there to
      // be typed
      expect(text()).toContain("Ma campagne");
    });
  });

  // THE WAY BACK. Every screen of the account version has carried a door
  // out to this one since it was built; this one had no door back, so a
  // volunteer who took it had left for good.
  describe("the way back to the account version", () => {
    const servedBy = (marker: boolean) => {
      for (const m of document.querySelectorAll(
        'meta[name="paraphe-served-by"]',
      )) {
        m.remove();
      }
      if (marker) {
        const meta = document.createElement("meta");
        meta.setAttribute("name", "paraphe-served-by");
        meta.setAttribute("content", "instance");
        document.head.appendChild(meta);
      }
    };
    afterEach(() => servedBy(false));

    const open = async () => {
      window.history.replaceState({}, "", "/");
      vi.stubGlobal("fetch", () =>
        Promise.resolve({
          ok: false,
          status: 404,
          json: () => Promise.resolve({}),
        }),
      );
      await act(async () => {
        root.render(<Browser />);
      });
      await until(() => text().includes("Bourg-Réel"), "the app opens");
    };

    it("is offered when an instance is the one serving this build", async () => {
      servedBy(true);
      await open();
      const back = [...container.querySelectorAll("a")].find((a) =>
        a.textContent?.includes("version avec compte"),
      );
      expect(back).toBeDefined();
      // the ROOT of this very origin: the account version is what the
      // instance serves there, and no other address is knowable from here
      expect(back?.getAttribute("href")).toBe("/");
    });

    // A static publication has no account version to send anybody to, and a
    // link to its own root is a link back to this same build.
    it("is absent from a build nobody marked", async () => {
      servedBy(false);
      await open();
      expect(text()).not.toContain("version avec compte");
    });
  });

  // WHICH HOST, and not how many requests. The promise this mode makes is
  // about a THIRD PARTY learning that you opened a link: a `?org=` names a
  // campaign on an instance you have never contacted, and ringing it at
  // load would tell it — with your address — every time the page opened.
  // That is what waits for a click, and it is verifiable in the network tab.
  //
  // A request to the origin that SERVED this page is a different act: it
  // already handed over the HTML, the bundle and 139 kB of mayor list, and
  // one more GET tells it nothing new. That is why a campaign's own
  // account-less version may ask which campaign it is — see ownCampaign —
  // and why this test counts hosts rather than calls. Written as « zero
  // requests », the rule read as something this mode never did: it
  // downloads its list at load, from that same origin, unasked.
  it("rings no host a LINK names until the volunteer clicks", async () => {
    const elsewhere: string[] = [];
    let own = 0;
    vi.stubGlobal("fetch", (url: string) => {
      if (String(url).startsWith("/")) {
        own++;
        return Promise.resolve({
          ok: false,
          status: 404,
          json: () => Promise.resolve({}),
        });
      }
      elsewhere.push(String(url));
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            slug: "campagne",
            name: "Camille Réel",
            campaign: OFFERED,
          }),
      });
    });
    await renderAndSettle();
    expect(
      elsewhere,
      "a request reached the instance the link names before any click",
    ).toEqual([]);
    // …and the one it did make went to its own origin, which is what tells
    // a campaign's own version from a static publication
    expect(own, "the origin was never asked which campaign it is").toBe(1);

    await click("Voir cette proposition");
    await until(
      () => text().includes("Reprendre la campagne"),
      "the offer lands",
    );
    expect(
      elsewhere,
      "the ask must send exactly one request, to the named campaign",
    ).toEqual(["https://campagne.paraphe.fr/api/campaign/public"]);
  });

  it("appears on an untouched campaign, and the warning stays with it", async () => {
    await renderWithOffer();
    expect(text()).toContain("Reprendre la campagne « Camille Réel »");
    // the state where messages are sendable with template values is
    // exactly the state where « n'envoyez rien » must be on screen
    expect(text()).toContain("n'envoyez rien");
  });

  it("disappears the moment the volunteer TYPES, before any save", async () => {
    await renderWithOffer();
    await click("Ma campagne");
    await type(firstCampaignField(), "Jeanne Bénévole");
    expect(
      text(),
      "an unsaved draft is the volunteer's work too",
    ).not.toContain("Reprendre la campagne");
  });

  it("disappears once a campaign is saved — and saved means STORED", async () => {
    await renderWithOffer();
    await click("Ma campagne");
    await type(firstCampaignField(), "Jeanne Bénévole");
    await click("Enregistrer");
    // prove the save LANDED first: asserting absence before it did would
    // pass whatever the guard does — the banner is hidden by the
    // typing itself
    await until(
      () => text().includes("Campagne enregistrée"),
      "the save lands",
    );
    expect(text()).not.toContain("Reprendre la campagne");
    // the banner is a claim about the BASE, not about the screen: under a
    // mutation that dropped the writes, every assertion above stayed green
    const stored = await DB.readSetting<Record<string, string>>("campagne", {});
    expect(stored.candidat).toBe("Jeanne Bénévole");
    expect(
      await DB.readSetting("argument", "missing"),
      "the personal note write must land with the campaign's",
    ).toBe("");
  });

  it("fills the form the volunteer is looking at when accepted there", async () => {
    await renderWithOffer();
    await click("Ma campagne");
    await click("Reprendre cette campagne");
    await until(() => text().includes("reprise"), "the adoption lands");
    expect(
      firstCampaignField().value,
      "the open form must show the adopted campaign, or « Enregistrer » " +
        "writes template values back over it",
    ).toBe(OFFERED.candidat);
  });

  it("stays refused: ?org= leaves the address bar", async () => {
    await renderWithOffer();
    await click("Non, je remplis moi-même");
    expect(text()).not.toContain("Reprendre la campagne");
    expect(window.location.search).not.toContain("org=");
  });

  // A link that names a campaign and produces NOTHING is the same silence
  // twice: whoever sent it believes it worked, and whoever opened it reads
  // the example values as the campaign's own. The refusal itself is right —
  // the values already here are this volunteer's, and a link does not
  // overwrite them — so what was missing is the sentence.
  it("says so when a campaign is already here, instead of ignoring the link", async () => {
    await DB.writeSetting("campagne", { ...OFFERED, candidat: "Déjà Saisi" });
    await act(async () => {
      root.render(<Browser />);
    });
    await until(
      () => text().includes("déjà enregistrée dans ce navigateur"),
      "the link says why it did nothing",
    );
    // …and it is not the OTHER message: this link is not broken, and the
    // campaign it names is not at fault
    expect(text()).not.toContain("ne propose aucune campagne");
    // the refusal still holds: no proposal, and the campaign already here
    // is untouched
    expect(text()).not.toContain("Ce lien propose une campagne");
    await click("Ma campagne");
    expect(firstCampaignField().value).toBe("Déjà Saisi");
  });
});

describe("unsent work on a card", () => {
  it("the rewritten email and the call note survive a look at the Guide", async () => {
    await renderWithOffer();
    await click("Bourg-Réel");
    await type(emailBody(), "Phrase écrite à la main pour ce maire.");
    const noteField = [...container.querySelectorAll("label")]
      .find((l) => l.textContent?.startsWith("Note"))!
      .querySelector("textarea")!;
    await type(noteField, "il rappelle jeudi");
    await click("Guide");
    await click("Les maires");
    await click("Bourg-Réel");
    // text addressed to a NAMED mayor: regenerating it from the template
    // because a tab is clicked is the same loss the campaign draft
    // already survives
    expect(emailBody().value).toBe("Phrase écrite à la main pour ce maire.");
    const noteBack = [...container.querySelectorAll("label")]
      .find((l) => l.textContent?.startsWith("Note"))!
      .querySelector("textarea")!;
    expect(noteBack.value).toBe("il rappelle jeudi");
  });

  // An INSEE names a COMMUNE, not a person. A list reload may seat a new
  // mayor under the same code, and reviving the predecessor's text there
  // is the project's founding trap on the interface side: « Cher M.
  // DUPONT » armed on the card of the mayor who replaced him.
  it("is NOT revived on a different mayor at the same INSEE", async () => {
    await renderWithOffer();
    await click("Bourg-Réel");
    await type(emailBody(), "Cher M. DUPONT, comme convenu au téléphone.");
    // the list changes under the store — here the demo set, which seats
    // its own mayors on real INSEE codes; a rebuilt CSV after a partial
    // election does the same
    await click("Mes données");
    await click("Données fictives");
    await until(() => text().includes("FICTIFS"), "the demo list loads");
    await click("Les maires");
    await until(
      () => text().includes("Sainte-Fiction-1"),
      "the demo mayors show",
    );
    await click("Sainte-Fiction-1");
    expect(
      emailBody().value,
      "text written to one mayor must never arm another's mailto",
    ).not.toContain("DUPONT");
  });

  // The successor case the demo list cannot prove: same commune, same
  // civility, same rank — no email template carries a name, so the two
  // renders are IDENTICAL and only the identity tells them apart.
  it("is NOT revived on a SUCCESSOR whose message renders identically", async () => {
    await renderWithOffer();
    await click("Bourg-Réel");
    await type(emailBody(), "Chère Madame MARTIN, comme convenu hier.");
    const note = [...container.querySelectorAll("label")]
      .find((l) => l.textContent?.startsWith("Note"))!
      .querySelector("textarea")!;
    await type(note, "sa fille est en fac de droit");
    await click("Mes données");
    await loadCsv(
      `${MAYOR.insee_code};Sophie;DURAND;${MAYOR.commune};` +
        `${MAYOR.department};has_endorsed;3;Camille Réel;2022;;Mme`,
    );
    await click("Les maires");
    await click("Bourg-Réel");
    expect(text()).toContain("DURAND");
    expect(
      emailBody().value,
      "the predecessor's rewrite must never arm his successor's mailto",
    ).not.toContain("MARTIN");
    const noteBack = [...container.querySelectorAll("label")]
      .find((l) => l.textContent?.startsWith("Note"))!
      .querySelector("textarea")!;
    expect(
      noteBack.value,
      "nor a private note about the predecessor's family",
    ).toBe("");
    expect(
      text(),
      "and no « régénéré » banner: this volunteer never wrote " +
        "a word to this person",
    ).not.toContain("Message régénéré");
  });

  it("survives the personal touch being written after the card was opened", async () => {
    await renderWithOffer();
    await click("Bourg-Réel");
    const note = [...container.querySelectorAll("label")]
      .find((l) => l.textContent?.startsWith("Note"))!
      .querySelector("textarea")!;
    await type(note, "il rappelle jeudi");
    await click("Ma campagne");
    const touch = [...container.querySelectorAll("label")]
      .find((l) => l.textContent?.includes("touche personnelle"))!
      .querySelector("textarea")!;
    await type(touch, "j'ai grandi dans une commune de 300 habitants");
    await click("Enregistrer");
    await until(
      () => text().includes("Campagne enregistrée"),
      "the save lands",
    );
    await click("Les maires");
    await click("Bourg-Réel");
    // the screen promises « insérée dans vos emails »: a draft kept
    // across a change of touch would keep the email without it, while
    // the letter beside it carries it
    expect(
      emailBody().value,
      "the touch must reach the email of a card opened before it was written",
    ).toContain("j'ai grandi dans une commune de 300 habitants");
    // and the call note, which derives from no campaign field at all,
    // is still there: tying it to the render throws away what was said
    // on the phone at the first unrelated change
    const noteBack = [...container.querySelectorAll("label")]
      .find((l) => l.textContent?.startsWith("Note"))!
      .querySelector("textarea")!;
    expect(noteBack.value).toBe("il rappelle jeudi");
  });

  // The screen and the mailto must never disagree about what happened.
  // Listing what the render derives from missed the rank: a list rebuilt
  // with a corrected false positive left « vous avez présenté » armed in
  // the mail client while the card beside it announced a discovery
  // message — the founding editorial rule, broken by a kept draft.
  it("is regenerated, and says so, when the mayor's signal changes", async () => {
    await renderWithOffer();
    await click("Bourg-Réel");
    expect(emailBody().value).toContain("vous avez présenté");
    await type(emailBody(), "Merci encore pour votre parrainage de 2022.");
    // the SAME person with a corrected signal, loaded the way a volunteer
    // picks up a rebuilt list — in place, without leaving the page, so
    // the draft store is still the one holding the rewrite
    await click("Mes données");
    await loadCsv(
      `${MAYOR.insee_code};${MAYOR.first_name};${MAYOR.last_name};` +
        `${MAYOR.commune};${MAYOR.department};no_signal;0;;;;Mme`,
    );
    await click("Les maires");
    // the corrected mayor left the priority pool — that IS the change
    const pool = [...container.querySelectorAll("select")].find((el) =>
      [...el.options].some((o) => o.value === "all"),
    )!;
    await act(async () => {
      const set = Object.getOwnPropertyDescriptor(
        window.HTMLSelectElement.prototype,
        "value",
      )!.set!;
      set.call(pool, "all");
      pool.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await until(
      () => text().includes("Bourg-Réel"),
      "the corrected list shows",
    );
    await click("Bourg-Réel");
    expect(
      emailBody().value,
      "no one may be thanked for an endorsement the list just withdrew",
    ).not.toContain("parrainage");
    expect(emailBody().value).not.toContain("vous avez présenté");
    // and the loss is SAID: swapping the text under the volunteer without
    // a word is how they discover it in the sent folder
    expect(text()).toContain("Message régénéré");
    // the banner speaks of the past: writing anew makes it false
    await type(
      emailBody(),
      "Je vous écris pour vous présenter notre candidate.",
    );
    expect(
      text(),
      "a banner that is always there stops being read",
    ).not.toContain("Message régénéré");
  });

  it("arms the leave-page dialog on unsent card work", async () => {
    await renderWithOffer();
    await click("Bourg-Réel");
    const before = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(before);
    expect(before.defaultPrevented, "nothing typed yet").toBe(false);
    await type(emailBody(), "Phrase écrite à la main.");
    const after = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(after);
    expect(
      after.defaultPrevented,
      "a rewritten email is the dearest text there is",
    ).toBe(true);
  });

  it("arms it for a call note alone, with the email untouched", async () => {
    await renderWithOffer();
    await click("Bourg-Réel");
    const note = [...container.querySelectorAll("label")]
      .find((l) => l.textContent?.startsWith("Note"))!
      .querySelector("textarea")!;
    await type(note, "il rappelle jeudi");
    const armed = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(armed);
    expect(
      armed.defaultPrevented,
      "what was said on the phone exists nowhere else",
    ).toBe(true);
  });

  it("a kept draft is DISCARDED when the campaign changes under it", async () => {
    await renderWithOffer();
    await click("Bourg-Réel");
    await type(emailBody(), "Texte fondé sur les valeurs de gabarit.");
    await click("Reprendre cette campagne");
    await until(() => text().includes("reprise"), "the adoption lands");
    // the kept draft is written under « Prénom NOM »: restoring it after
    // adoption would re-arm the very mailto the round-12 fix disarms
    expect(emailBody().value).toContain(OFFERED.candidat);
    expect(emailBody().value).not.toContain("Texte fondé sur les valeurs");
  });
});

describe("the draft in « Ma campagne »", () => {
  it("survives a look at the Guide tab", async () => {
    await renderWithOffer();
    await click("Ma campagne");
    await type(firstCampaignField(), "Jeanne Bénévole");
    await click("Guide");
    await click("Ma campagne");
    expect(
      firstCampaignField().value,
      "typed work must not vanish because a tab was clicked",
    ).toBe("Jeanne Bénévole");
  });

  it("does not lose keystrokes that land while a save is in flight", async () => {
    await renderWithOffer();
    await click("Ma campagne");
    await type(firstCampaignField(), "Jeanne");
    await click("Enregistrer");
    // typed before the two IndexedDB writes settle: a resync-from-cfg
    // an effect reverting this to « Jeanne » under the volunteer's hands
    await type(firstCampaignField(), "Jeanne Bénévole");
    await until(
      () => text().includes("Campagne enregistrée"),
      "the save lands",
    );
    expect(firstCampaignField().value).toBe("Jeanne Bénévole");
    // the save stores what was clicked, and the screen must NOT pretend
    // the newer keystrokes are saved: the marker tells them apart
    const stored = await DB.readSetting<Record<string, string>>("campagne", {});
    expect(stored.candidat).toBe("Jeanne");
    expect(text()).toContain("modifications non enregistrées");
  });

  it("the unsaved marker appears on typing and clears once stored", async () => {
    await renderWithOffer();
    await click("Ma campagne");
    expect(text()).not.toContain("modifications non enregistrées");
    await type(firstCampaignField(), "Jeanne Bénévole");
    expect(text()).toContain("modifications non enregistrées");
    await click("Enregistrer");
    await until(
      () => !text().includes("modifications non enregistrées"),
      "the marker clears when draft and base agree again",
    );
    // the personal touch enters every email: its half of the predicate
    // must raise the marker too
    const touch = [...container.querySelectorAll("label")]
      .find((l) => l.textContent?.includes("touche personnelle"))!
      .querySelector("textarea")!;
    await type(touch, "j'ai grandi dans une commune de 300 habitants");
    expect(text(), "an unsaved personal touch is unsaved work").toContain(
      "modifications non enregistrées",
    );
    // and the browser's own leave-page dialog is armed exactly then
    const leave = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(leave);
    expect(
      leave.defaultPrevented,
      "closing the tab on an unsaved draft must warn",
    ).toBe(true);
  });

  it("says so when the save FAILS", async () => {
    await renderWithOffer();
    await click("Ma campagne");
    await type(firstCampaignField(), "Jeanne Bénévole");
    // quota, private window, base blocked by a stale tab: the write
    // rejects, and the button must not silently do nothing at all
    vi.mocked(DB.writeSetting).mockRejectedValueOnce(
      new Error("QuotaExceededError"),
    );
    await click("Enregistrer");
    await until(
      () => text().includes("Enregistrement impossible"),
      "the failure is announced",
    );
    expect(text()).not.toContain("Campagne enregistrée");
    expect(text()).toContain("modifications non enregistrées");
  });

  it("re-arms an open card's email when the offer is adopted above it", async () => {
    await renderWithOffer();
    await click("Bourg-Réel"); // the banner renders above the open card too
    await click("Reprendre cette campagne");
    await until(() => text().includes("reprise"), "the adoption lands");
    // subject/body are controlled state reset per mayor: keyed on the
    // mayor alone, the mailto stayed armed with « Prénom NOM » while the
    // letter already shows the real candidate — and the warning is gone
    const bodies = [...container.querySelectorAll("textarea")].map(
      (t) => t.value,
    );
    expect(
      bodies.some((b) => b.includes(OFFERED.candidat)),
      "the email body must be rebuilt from the adopted campaign",
    ).toBe(true);
    expect(
      bodies.every((b) => !b.includes("Prénom NOM")),
      "no channel may keep template values after adoption",
    ).toBe(true);
  });

  it("says so when the ADOPTION fails", async () => {
    await renderWithOffer();
    vi.mocked(DB.writeSetting).mockRejectedValueOnce(
      new Error("QuotaExceededError"),
    );
    await click("Reprendre cette campagne");
    await until(
      () => text().includes("Reprise impossible"),
      "the failed adoption is announced",
    );
    expect(text()).not.toContain("reprise. Elle reste");
    // nothing is adopted: the campaign is still untouched and the offer
    // still stands
    expect(text()).toContain("Reprendre la campagne");
  });

  it("lets the offer come back after « Tout effacer »", async () => {
    vi.stubGlobal("confirm", () => true);
    await renderWithOffer();
    await click("Ma campagne");
    await type(firstCampaignField(), "Jeanne Bénévole");
    expect(text()).not.toContain("Reprendre la campagne");
    await click("Mes données");
    await click("Effacer ce navigateur");
    // everything typed is gone with consent: the offer is relevant again
    await until(
      () => text().includes("Reprendre la campagne"),
      "the offer returns on an erased browser",
    );
  });
});

// The nine campaign fields must not be described TWICE — a map in Browser.tsx and a
// tuple list in Team.tsx — and the two had already drifted: they disagreed on
// what ville_envoi does, and one told the volunteer to type "(email,
// téléphone)" into a one-line description of the candidate. A second list
// costs nothing to write and is invisible until a volunteer reads the wrong
// one.
describe("the campaign fields are described once", () => {
  it("covers exactly the keys the engine fills, in order", () => {
    expect(CAMPAIGN_FIELDS.map((f) => f.key)).toEqual(CAMPAIGN_KEYS);
  });

  it("says whose each field is, and shows what to type", () => {
    for (const f of CAMPAIGN_FIELDS) {
      expect(f.group, `${f.key} belongs to no group`).toBeTruthy();
      expect(f.example, `${f.key} shows no example`).toBeTruthy();
    }
  });

  it("names those keys in no other source file", () => {
    // jsdom rewrites import.meta.url to an http:// URL, so the directory is
    // found from the working directory instead — and the search THROWS
    // rather than scan nothing: a canary over an empty directory passes
    // while proving nothing.
    let dir = "";
    for (let d = process.cwd(), i = 0; i < 4; i++, d = dirname(d)) {
      for (const c of [join(d, "src"), join(d, "web", "src")]) {
        if (existsSync(join(c, "common.tsx"))) dir = c;
      }
      if (dir) break;
    }
    if (!dir) throw new Error("web/src not found from " + process.cwd());

    const scanned = readdirSync(dir)
      .filter((n) => /\.tsx?$/.test(n) && !/\.test\.tsx?$/.test(n))
      .filter((n) => n !== "common.tsx");
    expect(scanned.length, "nothing was scanned").toBeGreaterThan(5);

    // `key: "some string"`, and not the bare word: French prose says
    // "candidat", "site" and "signataire" all the time, and a first attempt
    // that keys on the quoted key is written around by an object literal
    // whose keys are unquoted — the very shape a label map has.
    const offenders = scanned.filter((n) => {
      const src = readFileSync(join(dir, n), "utf8");
      const named = CAMPAIGN_KEYS.filter((k) =>
        new RegExp(`\\b${k}\\s*:\\s*["'\`]`).test(src),
      );
      return named.length >= 3;
    });
    expect(offenders).toEqual([]);
  });
});
