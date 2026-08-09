// Configuration guardrails for the mass mailing.
//
// They exist because the template really did go out: 1,934 emails
// containing "Prénom NOM", ready to be sent to mayors.

import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { CAMPAIGN_ENV, loadConfig, loadTemplates, ROOT } from "./config.ts";
import { CAMPAIGN_KEYS, createEngine } from "../noyau/messages.ts";

const ENV_KEYS = [
  ...CAMPAIGN_KEYS.map((k) => CAMPAIGN_ENV[k]),
  "PARAPHE_BATCH_SIZE",
];

const filled = () => {
  for (const k of CAMPAIGN_KEYS) {
    process.env[CAMPAIGN_ENV[k]] = `valeur de ${k}`;
  }
};

afterEach(() => {
  for (const k of ENV_KEYS) delete process.env[k];
});

describe("the configuration", () => {
  it("refuses to serve a template to the mass mailing", () => {
    filled();
    process.env[CAMPAIGN_ENV.candidat] = "Prénom NOM";
    expect(() => loadConfig({ strict: true })).toThrow(/gabarit/);
  });

  it("flags the template without blocking when not required", () => {
    filled();
    process.env[CAMPAIGN_ENV.candidat] = "Prénom NOM";
    expect(loadConfig({ strict: false }).unfilled).toContain("candidat");
  });

  it("ignores an environment variable made of spaces", () => {
    filled();
    process.env[CAMPAIGN_ENV.contact_email] = "   ";
    const { campaign } = loadConfig({ strict: false });
    expect(campaign.contact_email.trim()).not.toBe("");
  });

  it("refuses a batch size that is not an integer", () => {
    filled();
    process.env.PARAPHE_BATCH_SIZE = "beaucoup";
    expect(() => loadConfig({ strict: false })).toThrow(/entier/);
  });

  it("accepts a fully filled campaign", () => {
    filled();
    const cfg = loadConfig({ strict: true });
    expect(cfg.unfilled).toEqual([]);
    expect(cfg.batchSize).toBeGreaterThan(0);
  });
});

describe("the repository templates", () => {
  it("provide the six expected texts", () => {
    const templates = loadTemplates();
    expect(Object.keys(templates).sort()).toEqual([
      "courrier.txt", "courrier_decouverte.txt", "email.txt",
      "email_decouverte.txt", "telephone.txt", "telephone_decouverte.txt",
    ]);
  });

  it("render without any leftover placeholder", () => {
    filled();
    const { campaign } = loadConfig({ strict: true });
    const engine = createEngine(loadTemplates());
    const mayor = {
      rank: "has_endorsed", title: "Mme", first_name: "Camille",
      last_name: "MARTIN", commune: "Sainte-Fiction", department: "Aveyron",
      insee_code: "90001",
      recent_candidate: "Alex Exemple", recent_year: "2022",
      endorsement_history: "2022: EXEMPLE Alex (A)",
    };
    for (const text of [engine.email(mayor, campaign).body,
      engine.letter(mayor, campaign), engine.phoneScript(mayor, campaign)]) {
      expect(text).not.toMatch(/\{[a-z_]+\}/);
    }
  });
});

// The two implementations each hold their own copy of the table — Go cannot
// import TypeScript. This is what keeps them equal: the shared JSON is the
// referee, and api/config_test.go checks its map against the same file.
describe("the campaign variables", () => {
  const shared: Record<string, string> = JSON.parse(
    readFileSync(join(ROOT, "noyau", "campaign-env.json"), "utf8"));

  it("match the table both languages are checked against", () => {
    const expected = Object.fromEntries(
      Object.entries(shared).filter(([k]) => !k.startsWith("_")));
    expect(CAMPAIGN_ENV).toEqual(expected);
  });

  it("cover every key the engine fills, and nothing else", () => {
    expect(Object.keys(CAMPAIGN_ENV).sort()).toEqual([...CAMPAIGN_KEYS].sort());
  });

  it("are all English, none left in French", () => {
    const french = /MOTDEPASSE|CANDIDAT_|SIGNATAIRE|VILLE|TAILLE|SORTIES|DOMAINE|ESSAI|_NOM\b|_TEL\b/;
    for (const v of Object.values(CAMPAIGN_ENV)) {
      expect(v, `${v} still reads as French`).not.toMatch(french);
    }
  });
});
