// A link may name a CAMPAIGN. It may never name a host.
//
// That is the whole security of the pre-fill: the instance domain is baked
// at build time, so the values that will fill thousands of messages to
// mayors can only come from a campaign requested, moderated and
// approved there. A parameter carrying the configuration itself, or a URL
// to fetch it from, would let a forged link put an attacker's contact
// details under a real candidate's name.

import { afterEach, describe, expect, it, vi } from "vitest";
import { CAMPAIGN_KEYS, unfilledKeys } from "../../noyau/messages.ts";
import { EMPTY_CFG } from "./common.tsx";
import {
  ADOPTED_KEYS,
  fetchCampaign,
  instanceDomain,
  readAdoption,
  readOffer,
  requestedSlug,
  sameAdoptedFields,
  sameTemplates,
  untouchedCampaign,
  validSlug,
} from "./prefill.ts";

/** A campaign the app may adopt: every key filled, or it is refused. */
const whole = () =>
  Object.fromEntries(CAMPAIGN_KEYS.map((k) => [k, `valeur de ${k}`])) as Record<
    string,
    string
  >;

const withDomain = (domain: string) => {
  vi.stubEnv("PARAPHE_INSTANCE_DOMAIN", domain);
};

/**
 * What the server injects into the page it serves (api/pages.go,
 * markBrowserVersion) — the same mechanism as the mode marker, and the only
 * thing that makes ?org= work on the build an instance hosts under
 * /navigateur/, which is published carrying no domain at all.
 */
const withMarker = (domain: string) => {
  const meta = document.createElement("meta");
  meta.setAttribute("name", "paraphe-instance");
  meta.setAttribute("content", domain);
  document.head.appendChild(meta);
};

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  for (const m of document.querySelectorAll('meta[name="paraphe-instance"]')) {
    m.remove();
  }
});

describe("the slug a link may name", () => {
  it.each(["campagne", "ma-campagne", "c2027", "ab"])(
    "accepts %s, a DNS label",
    (slug) => {
      expect(validSlug(slug)).toBe(true);
    },
  );

  // Everything below would build a request somewhere else, or nowhere
  it.each([
    "évidemment",
    "MaCampagne",
    "a",
    "-campagne",
    "campagne-",
    "campagne.autre-domaine.fr",
    "campagne/../../etc",
    "campagne:8080",
    "campagne?x=1",
    "..",
    "a".repeat(64),
  ])("refuses %s", (slug) => {
    expect(validSlug(slug)).toBe(false);
  });

  it("does nothing at all when no instance was baked in", () => {
    withDomain("");
    expect(requestedSlug("?org=campagne")).toBeNull();
  });

  it("reads the slug when an instance was baked in", () => {
    withDomain("paraphe.fr");
    expect(requestedSlug("?org=campagne")).toBe("campagne");
    expect(requestedSlug("?org=https://ailleurs.test")).toBeNull();
  });

  // The published image bakes NO domain — one image serves every operator's
  // instance, and one carrying paraphe.org would send everybody else's
  // volunteers to fetch campaigns from there. The instance it actually
  // stands on is injected into the page it serves.
  it("takes the instance from the page when the build baked none", () => {
    withDomain("");
    withMarker("paraphe.fr");
    expect(requestedSlug("?org=campagne")).toBe("campagne");
  });

  // And it OUTRANKS the baked value, which is the publication's guess about
  // where it would be served. The document's is the server's own answer.
  it("prefers the page's instance to the one baked in", () => {
    withDomain("ailleurs.test");
    withMarker("paraphe.fr");
    expect(instanceDomain()).toBe("paraphe.fr");
  });

  // A marker is not a licence for the LINK to name a host: the parameter
  // still carries a DNS label, and everything else is refused as before.
  it("still refuses a slug that names anything but a campaign", () => {
    withMarker("paraphe.fr");
    for (const forged of [
      "?org=ailleurs.test",
      "?org=https://ailleurs.test",
      "?org=campagne.ailleurs.test",
      "?org=../autre",
    ]) {
      expect(requestedSlug(forged)).toBeNull();
    }
  });
});

describe("fetching the proposed campaign", () => {
  it("asks the baked instance, never a host from the link", async () => {
    withDomain("paraphe.fr");
    const seen: string[] = [];
    vi.stubGlobal("fetch", (url: string) => {
      seen.push(url);
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            slug: "campagne",
            name: "Camille Réel",
            campaign: { ...whole(), candidat: "Camille Réel" },
          }),
      });
    });
    const offer = await fetchCampaign("campagne");
    expect(seen).toEqual(["https://campagne.paraphe.fr/api/campaign/public"]);
    expect(offer.campaign.candidat).toBe("Camille Réel");
  });

  // Same rule one source over — the host is the instance the PAGE names,
  // with the slug as a label under it — and one difference that only a
  // running instance shows: an instance that named itself SERVED this page,
  // so it is reachable exactly as this page was reached. Written `https://…`
  // with no port, the request went to a host nothing answers at; the
  // end-to-end suite (:8399) and `task try-instance` (:8047) both failed
  // with « Failed to fetch », and production, on 443, hid it.
  it("asks the instance the page names, on the scheme and port it came from", async () => {
    withDomain("");
    withMarker("paraphe.fr");
    const seen: string[] = [];
    vi.stubGlobal("fetch", (url: string) => {
      seen.push(url);
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            slug: "campagne",
            name: "Camille Réel",
            campaign: whole(),
          }),
      });
    });
    await fetchCampaign("campagne");
    const { protocol, port } = window.location;
    expect(seen).toEqual([
      `${protocol}//campagne.paraphe.fr${port ? `:${port}` : ""}/api/campaign/public`,
    ]);
  });

  // The check that makes « a link names a campaign, never a host » true is
  // in `requestedSlug` today, and this is the function that INTERPOLATES the
  // label into a hostname. Asked directly — which is one new call site away
  // — it must refuse rather than resolve somebody else's machine.
  it("refuses a slug that names a host, whoever asked", async () => {
    withDomain("paraphe.fr");
    const seen: string[] = [];
    vi.stubGlobal("fetch", (url: string) => {
      seen.push(url);
      return Promise.resolve({ ok: true, json: () => Promise.resolve({}) });
    });
    for (const forged of [
      "ailleurs.test",
      "campagne@ailleurs.test",
      "campagne.ailleurs.test",
      "campagne/../..",
    ]) {
      await expect(fetchCampaign(forged)).rejects.toThrow(/ne nomme pas/);
    }
    expect(seen, "a forged slug reached the network").toEqual([]);
  });

  // A campaign still at its template values serves its TEXTS — the server
  // omits the nine rather than spread « Prénom NOM », and refused wholesale
  // it took the campaign's own templates down with it. The LINK door still
  // owes its reader the old 409's sentence: a link pre-fills the nine, and
  // this campaign has none to offer.
  it("refuses a campaign with no fields yet, in the old sentence", async () => {
    withDomain("paraphe.fr");
    vi.stubGlobal("fetch", () =>
      Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            slug: "campagne",
            name: "Camille Réel",
            templates: { "email.txt": "OBJET: s\n\nCorps." },
          }),
      }),
    );
    await expect(fetchCampaign("campagne")).rejects.toThrow(
      /pas encore configurée/,
    );
  });

  // A captive portal answers 200 with HTML, and adopting that would fill
  // every message with nothing at all
  it("refuses an answer that is not a campaign", async () => {
    withDomain("paraphe.fr");
    vi.stubGlobal("fetch", () =>
      Promise.resolve({
        ok: true,
        json: () => Promise.resolve({ hello: "world" }),
      }),
    );
    await expect(fetchCampaign("campagne")).rejects.toThrow(/ne ressemble pas/);
  });

  it("carries the server's own refusal through", async () => {
    withDomain("paraphe.fr");
    vi.stubGlobal("fetch", () =>
      Promise.resolve({
        ok: false,
        status: 409,
        json: () => Promise.resolve({ error: "pas encore configurée" }),
      }),
    );
    await expect(fetchCampaign("campagne")).rejects.toThrow(
      /pas encore configurée/,
    );
  });
});

// A half-filled object is worse than none: eight blank rows shown as a
// campaign, or a value that is not a string, which replaced the whole
// screen with the error boundary — and ?org= still in the URL, so a reload
// reproduced it with the volunteer's work behind that screen.
describe("what counts as a campaign", () => {
  it.each([
    ["a single key", { candidat: "Camille Réel" }],
    ["a value that is not a string", { ...whole(), signataire: { evil: 1 } }],
    ["an empty required value", { ...whole(), candidat: "   " }],
  ])("refuses %s", async (_case, campaign) => {
    withDomain("paraphe.fr");
    vi.stubGlobal("fetch", () =>
      Promise.resolve({
        ok: true,
        json: () => Promise.resolve({ campaign }),
      }),
    );
    await expect(fetchCampaign("campagne")).rejects.toThrow(
      /campagne complète/,
    );
  });

  // A campaign is allowed to give a telephone number to nobody, to run
  // without a website and not to name the town its letters leave from —
  // `campaign-optional.json` is the referee both languages answer to, and
  // the API applies it: those keys empty, /api/campaign/public answers 200
  // and the campaign IS configured.
  //
  // Refusing them here made the two ends disagree about what a campaign is,
  // and the disagreement was silent: the API said « voici la campagne », the
  // browser version said « ça ne ressemble pas à une campagne » and pre-
  // filled nothing, leaving « Prénom NOM » on screen under a « campagne non
  // configurée » banner — on a campaign whose team version substitutes
  // correctly.
  it.each(["contact_email", "contact_tel", "site", "ville_envoi"])(
    "accepts a campaign that left %s empty, as the API does",
    async (key) => {
      withDomain("paraphe.fr");
      vi.stubGlobal("fetch", () =>
        Promise.resolve({
          ok: true,
          json: () => Promise.resolve({ campaign: { ...whole(), [key]: "" } }),
        }),
      );
      const offer = await fetchCampaign("campagne");
      expect(offer.campaign[key]).toBe("");
      expect(offer.campaign.candidat).toBe(whole().candidat);
    },
  );
});

// The offer is for a campaign NOBODY has touched. "Not complete" is the
// wrong test: a volunteer who had filled eight fields of nine — their own
// name under « Qui signe les emails », their own phone — is offered a link
// that replaced all nine, and `signataire` is the signature at the bottom
// of every email to a mayor.
describe("when the offer may appear at all", () => {
  // untouchedCampaign is what the SCREEN calls. Rewriting the predicate
  // here would make the test a tautology: reinstating the defect it is written
  // for left it green.
  it("appears on a campaign nobody has typed into", () => {
    expect(untouchedCampaign(EMPTY_CFG)).toBe(true);
  });

  it.each(["signataire", "contact_email", "candidat"])(
    "does NOT appear once %s is the volunteer's own",
    (key) => {
      const cfg = { ...EMPTY_CFG, [key]: "Jeanne Bénévole" };
      // still incomplete — and that is exactly the state that must not be
      // offered a link replacing all nine fields
      expect(unfilledKeys(cfg).length).toBeGreaterThan(0);
      expect(
        untouchedCampaign(cfg),
        "an incomplete campaign is not an untouched one",
      ).toBe(false);
    },
  );
});

// The ORIGIN door's reading of a campaign with no fields yet: the offer
// stands, campaign null, texts and all — that door has nothing to pre-fill
// either way. A body with NEITHER block NOR slug stays refused: a captive
// portal's arbitrary JSON must not read as « not configured yet ».
describe("an offer whose campaign block is absent", () => {
  it("carries the texts with campaign null when the body names its slug", () => {
    const offer = readOffer({
      slug: "sienne",
      name: "Camille Sienne",
      templates: { "email.txt": "OBJET: s\n\nCorps." },
    });
    expect(offer.campaign).toBeNull();
    expect(offer.slug).toBe("sienne");
    expect(offer.templates).toEqual({ "email.txt": "OBJET: s\n\nCorps." });
  });

  it("still refuses a body that names nothing", () => {
    expect(() => readOffer({ hello: "world" })).toThrow(/ne ressemble pas/);
  });
});

// WHAT THE LAST ADOPTION WROTE, remembered — the reference that tells
// « still the campaign's » from « made their own since », which is what lets
// a campaign's own browser version follow the campaign as it edits its
// texts, and nothing else.
describe("the adoption snapshot", () => {
  it("never carries the keys that name a person", () => {
    expect(ADOPTED_KEYS).not.toContain("signataire");
    expect(ADOPTED_KEYS).not.toContain("signataire_qualite");
    expect(ADOPTED_KEYS).toContain("candidat");
  });

  // Who signs never travelled, so a volunteer signing for themselves must
  // not read as "made the campaign their own": the comparison skips the
  // personal keys entirely.
  it("compares the adopted keys and ignores who signs", () => {
    const a = { ...whole(), signataire: "Jeanne Bénévole" };
    const b = { ...whole(), signataire: "" };
    expect(sameAdoptedFields(a, b)).toBe(true);
    expect(sameAdoptedFields(a, { ...b, candidat: "Autre" })).toBe(false);
  });

  it("compares the six templates by value, absent reading as empty", () => {
    expect(sameTemplates({}, {})).toBe(true);
    expect(sameTemplates({ "email.txt": "" }, {})).toBe(true);
    expect(sameTemplates({ "email.txt": "x" }, {})).toBe(false);
    expect(sameTemplates({ "email.txt": "x" }, { "email.txt": "x" })).toBe(
      true,
    );
  });

  // Judged like everything read back from IndexedDB: a restored backup or
  // another version can have written anything under this key, and an
  // unusable snapshot is a missing one — which every browser from before it
  // existed has, and which must never throw the app into the boundary.
  it("reads back what an adoption stored", () => {
    const stored = {
      slug: "sienne",
      campaign: whole(),
      templates: { "email.txt": "OBJET: s\n\nCorps." },
    };
    const read = readAdoption(stored);
    expect(read?.slug).toBe("sienne");
    expect(read?.campaign.candidat).toBe("valeur de candidat");
    expect(read?.templates).toEqual({ "email.txt": "OBJET: s\n\nCorps." });
  });

  it.each([
    ["nothing", null],
    ["a plain string", "sienne"],
    ["a slug that names a host", { slug: "a.b", campaign: {}, templates: {} }],
    ["a campaign that is not an object", { slug: "ok", campaign: 3 }],
  ])("answers null to %s", (_what, value) => {
    expect(readAdoption(value)).toBeNull();
  });

  it("drops what the template filter would drop on the wire", () => {
    const read = readAdoption({
      slug: "sienne",
      campaign: whole(),
      templates: { "email.txt": 42, "courriel.txt": "x" },
    });
    expect(read?.templates).toEqual({});
  });
});
