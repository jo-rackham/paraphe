// The message engine's first rule: never claim an endorsement from
// someone who made none. These tests hold it at the surface the
// interface actually calls.

import { describe, expect, it } from "vitest";
import type { Mayor } from "./types.ts";
import {
  clean, email, emailAddresses, incompleteAddress, letter, letterHeader,
  MissingField, phoneScript, proseName, readableHistory, unfilledKeys,
} from "./messages.ts";

const CFG = {
  candidat: "Alex Exemple",
  candidat_description: "médecin, candidature citoyenne",
  candidat_description_longue: "Je suis médecin. Je porte ceci.",
  signataire: "Bénévole Test",
  signataire_qualite: "équipe de campagne",
  contact_tel: "01 23 45 67 89",
  contact_email: "contact@exemple-fictif.fr",
  site: "https://exemple-fictif.fr",
  ville_envoi: "Rodez",
};

const base = (extra: Mayor = {}): Mayor => ({
  rank: "no_signal", title: "M.", first_name: "Dominique", last_name: "MARTIN",
  commune: "Sainte-Fiction", department: "Aveyron", insee_code: "90001",
  endorsement_history: "", predecessor: "", predecessor_mayor: "",
  recent_candidate: "", recent_year: "", email: "mairie@exemple-fictif.fr",
  phone: "01 23 45 67 89", town_hall_hours: "lundi 9h-12h",
  postal_address: "1 place de la Fiction", postal_code: "12000",
  city: "Sainte-Fiction", ...extra,
});

const ENDORSER = base({
  rank: "has_endorsed", recent_candidate: "Alex Exemple", recent_year: "2022",
  endorsement_history: "2017: EXEMPLE Alex (A) | 2022: EXEMPLE Alex (A)",
});
const COMMUNE = base({
  rank: "commune_has_endorsed", predecessor: "Claude ANCIEN",
  predecessor_mayor: "oui",
  endorsement_history: "2017: EXEMPLE Alex (A)",
});

const CLAIMS = /vous avez présenté|votre présentation de \d{4}|votre parrainage|vous en remercier|ce geste/i;

describe("the central invariant", () => {
  it.each([["commune_has_endorsed", COMMUNE], ["no_signal", base()]])(
    "claims no endorsement at rank %s", (_rank, mayor) => {
      const { subject, body } = email(mayor, CFG);
      for (const text of [subject, body, letter(mayor, CFG),
        phoneScript(mayor, CFG)]) {
        expect(text).not.toMatch(CLAIMS);
      }
    });

  it("does thank proven endorsers", () => {
    const { body } = email(ENDORSER, CFG);
    expect(body).toContain("vous avez présenté");
    expect(body).toContain("Alex Exemple");
  });

  it("attributes the precedent to the commune, not the person", () => {
    const { body } = email(COMMUNE, CFG);
    expect(body).toContain("Votre commune");
    expect(body).toContain("Claude Ancien");
  });
});

// `ville_envoi` appears in no template: this line is the only thing that
// consumes it, and it is what makes a posted letter a letter. The mass
// mailing and the on-screen letter must build it the same way — they used to
// build it in two places, one of which the screen never showed.
describe("the letter's dateline", () => {
  const ON = { today: new Date(2026, 7, 9) };

  it("carries the sending town and the date, in French", () => {
    expect(letterHeader(base(), CFG, ON)).toBe("Rodez, le 9 août 2026");
  });

  it("is not folded into the letter body, which would double it", () => {
    expect(letter(base(), CFG, ON)).not.toContain("Rodez, le");
  });
});

describe("rendering hygiene", () => {
  it.each([["endorser", ENDORSER], ["commune", COMMUNE], ["no_signal", base()]])(
    "leaves no placeholder (%s)", (_n, mayor) => {
      const { subject, body } = email(mayor, CFG);
      for (const t of [subject, body, letter(mayor, CFG),
        phoneScript(mayor, CFG)]) {
        expect(t).not.toMatch(/[{}]/);
      }
    });

  it.each([["endorser", ENDORSER], ["commune", COMMUNE], ["no_signal", base()]])(
    "inserts no empty field into the email or the letter (%s)",
    (_n, mayor) => {
      // the call script is markdown: its lists are indented by two spaces,
      // deliberately. The email and the letter are raw text sent as is.
      const { body } = email(mayor, CFG);
      for (const t of [body, letter(mayor, CFG)]) {
        expect(t).not.toContain("  ");
        expect(t).not.toMatch(/ [,.]/);
      }
    });

  it("follows the civility for the salutation", () => {
    expect(email(base({ title: "Mme", first_name: "Camille" }), CFG).body)
      .toMatch(/^Madame la Maire/);
    expect(email(base(), CFG).body).toMatch(/^Monsieur le Maire/);
  });

  it("refuses a civility outside the domain", () => {
    for (const bad of ["Madame", "Mlle", "", "Dr"]) {
      expect(() => email(base({ title: bad }), CFG))
        .toThrow(MissingField);
    }
  });
});

describe("the volunteer's free text", () => {
  it("neutralises braces", () => {
    const { body } = email(base(), CFG,
      { personalNote: "Je crois que {candidat} compte." });
    expect(body).not.toContain("{candidat}");
    expect(body).toContain("(candidat)");
  });

  it("absorbs the browser's CRLFs", () => {
    const { body } = email(base(), CFG,
      { personalNote: "Un.\r\n\r\n\r\n\r\nDeux." });
    expect(body).not.toMatch(/\n{3,}/);
  });
});

describe("directory data", () => {
  it("decodes CP1252 bytes instead of erasing them", () => {
    expect(clean("ting")).toBe("Œting");
    expect(clean("rue des Surs")).toBe("rue des Sœurs");
  });

  it("splits concatenated addresses", () => {
    const { valid, rejected } = emailAddresses({ email: "a@x.fr;b@y.fr,c@z.fr" });
    expect(valid).toEqual(["a@x.fr", "b@y.fr", "c@z.fr"]);
    expect(rejected).toEqual([]);
  });

  it("rejects an address with a broken domain", () => {
    const { valid, rejected } = emailAddresses({ email: "mairie@orangefr" });
    expect(valid).toEqual([]);
    expect(rejected).toEqual(["mairie@orangefr"]);
  });

  it("flags an undeliverable letter", () => {
    expect(incompleteAddress({ postal_code: "", commune: "X" })).toBeTruthy();
    expect(incompleteAddress({ postal_code: "12000", commune: "X" })).toBeNull();
  });
});

describe("formatting", () => {
  it("groups the years of the same candidate", () => {
    expect(readableHistory("2017: ARTHAUD Nathalie (B) | 2022: ARTHAUD Nathalie (B)"))
      .toBe("Nathalie Arthaud en 2017 et 2022");
  });

  it("turns names back into prose", () => {
    expect(proseName("THOUY Hélène")).toBe("Hélène Thouy");
  });
});

describe("configuration guardrail", () => {
  it("spots values still at the template", () => {
    expect(unfilledKeys({ ...CFG, candidat: "Prénom NOM" }))
      .toContain("candidat");
    expect(unfilledKeys(CFG)).toEqual([]);
  });
});
