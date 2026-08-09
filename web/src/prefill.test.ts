// A link may name a CAMPAIGN. It may never name a host.
//
// That is the whole security of the pre-fill: the instance domain is baked
// at build time, so the values that will fill thousands of messages to
// mayors can only come from a campaign that was requested, moderated and
// approved there. A parameter carrying the configuration itself, or a URL
// to fetch it from, would let a forged link put an attacker's contact
// details under a real candidate's name.

import { afterEach, describe, expect, it, vi } from "vitest";
import { CAMPAIGN_KEYS, unfilledKeys } from "../../noyau/messages.ts";
import { EMPTY_CFG } from "./common.tsx";
import {
  fetchCampaign, requestedSlug, untouchedCampaign, validSlug,
} from "./prefill.ts";

/** A campaign the app may adopt: every key filled, or it is refused. */
const whole = () => Object.fromEntries(
  CAMPAIGN_KEYS.map((k) => [k, `valeur de ${k}`])) as Record<string, string>;

const withDomain = (domain: string) => {
  vi.stubEnv("PARAPHE_INSTANCE_DOMAIN", domain);
};

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
});

describe("the slug a link may name", () => {
  it.each(["campagne", "ma-campagne", "c2027", "ab"])(
    "accepts %s, a DNS label", (slug) => {
      expect(validSlug(slug)).toBe(true);
    });

  // Everything below would build a request somewhere else, or nowhere
  it.each([
    "évidemment", "MaCampagne", "a", "-campagne", "campagne-",
    "campagne.autre-domaine.fr", "campagne/../../etc", "campagne:8080",
    "campagne?x=1", "..", "a".repeat(64),
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
});

describe("fetching the proposed campaign", () => {
  it("asks the baked instance, never a host from the link", async () => {
    withDomain("paraphe.fr");
    const seen: string[] = [];
    vi.stubGlobal("fetch", (url: string) => {
      seen.push(url);
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({
          slug: "campagne", name: "Camille Réel",
          campaign: { ...whole(), candidat: "Camille Réel" },
        }),
      });
    });
    const offer = await fetchCampaign("campagne");
    expect(seen).toEqual(["https://campagne.paraphe.fr/api/campaign/public"]);
    expect(offer.campaign.candidat).toBe("Camille Réel");
  });

  // A captive portal answers 200 with HTML, and adopting that would fill
  // every message with nothing at all
  it("refuses an answer that is not a campaign", async () => {
    withDomain("paraphe.fr");
    vi.stubGlobal("fetch", () => Promise.resolve({
      ok: true, json: () => Promise.resolve({ hello: "world" }),
    }));
    await expect(fetchCampaign("campagne")).rejects.toThrow(/ne ressemble pas/);
  });

  it("carries the server's own refusal through", async () => {
    withDomain("paraphe.fr");
    vi.stubGlobal("fetch", () => Promise.resolve({
      ok: false, status: 409,
      json: () => Promise.resolve({ error: "pas encore configurée" }),
    }));
    await expect(fetchCampaign("campagne")).rejects.toThrow(/pas encore configurée/);
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
    ["an empty value", { ...whole(), contact_email: "   " }],
  ])("refuses %s", async (_case, campaign) => {
    withDomain("paraphe.fr");
    vi.stubGlobal("fetch", () => Promise.resolve({
      ok: true, json: () => Promise.resolve({ campaign }),
    }));
    await expect(fetchCampaign("campagne")).rejects.toThrow(/campagne complète/);
  });
});

// The offer is for a campaign NOBODY has touched. "Not complete" was the
// wrong test: a volunteer who had filled eight fields of nine — their own
// name under « Qui signe les emails », their own phone — was offered a link
// that replaced all nine, and `signataire` is the signature at the bottom
// of every email to a mayor.
describe("when the offer may appear at all", () => {
  // untouchedCampaign is what the SCREEN calls. Rewriting the predicate
  // here made the test a tautology: reinstating the defect it was written
  // for left it green.
  it("appears on a campaign nobody has typed into", () => {
    expect(untouchedCampaign(EMPTY_CFG)).toBe(true);
  });

  it.each(["signataire", "contact_email", "candidat"])(
    "does NOT appear once %s is the volunteer's own", (key) => {
      const cfg = { ...EMPTY_CFG, [key]: "Jeanne Bénévole" };
      // still incomplete — and that is exactly the state that used to be
      // offered a link replacing all nine fields
      expect(unfilledKeys(cfg).length).toBeGreaterThan(0);
      expect(untouchedCampaign(cfg),
        "an incomplete campaign is not an untouched one").toBe(false);
    });
});
