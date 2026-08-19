// Each of these tests reproduces a defect that would send a wrong text to
// a real mayor — either outright, or at the first upstream change.

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  type Campaign,
  context,
  createEngine,
  elidedCommune,
  fields,
  InvalidTemplate,
  type Mayor,
  MissingField,
  OPTIONAL_CAMPAIGN_KEYS,
  rank,
  recipientAddress,
  type Templates,
  unfilledKeys,
} from "./messages.ts";

/** The templates as shipped: what the campaign actually sends. */
const REAL_TEMPLATES: Templates = Object.fromEntries(
  [
    "email",
    "email_decouverte",
    "courrier",
    "courrier_decouverte",
    "telephone",
    "telephone_decouverte",
  ].map((name) => [
    `${name}.txt`,
    readFileSync(
      join(
        dirname(fileURLToPath(import.meta.url)),
        "..",
        "modeles",
        `${name}.txt`,
      ),
      "utf8",
    ),
  ]),
);

const CFG: Campaign = Object.fromEntries(
  [
    "candidat",
    "candidat_description",
    "candidat_description_longue",
    "signataire",
    "signataire_qualite",
    "contact_tel",
    "contact_email",
    "site",
    "ville_envoi",
  ].map((k) => [k, `valeur de ${k}`]),
);

const ENDORSER: Mayor = {
  rank: "has_endorsed",
  title: "M.",
  first_name: "Xavier",
  last_name: "BEDOS",
  commune: "Rieux-en-Val",
  department: "Aude",
  insee_code: "11318",
  recent_candidate: "Anasse Kazib",
  recent_year: "2022",
  endorsement_history: "2022: KAZIB Anasse (A)",
};

describe("the placeholder guard", () => {
  // `\w` = [A-Za-z0-9_]: "{prénom}" walked through and went out in the
  // clear in 1,953 emails. Editing the templates is a goal of the project,
  // and "{prénom}" is what a French speaker writes spontaneously.
  it.each(["{prénom}", "{code-postal}", "{inconnu}", "{Nom}", "{ prénom }"])(
    "refuses %s instead of copying it through",
    (hole) => {
      const engine = createEngine({
        "email.txt": `OBJET: x\nBonjour ${hole},\n`,
      });
      expect(() => engine.email(ENDORSER, CFG)).toThrow(InvalidTemplate);
    },
  );

  // Two kinds of error: a broken template affects ALL recipients, an empty
  // field only one. Conflating them skipped the 1,972 mayors one by one and
  // replaced the previous mailing with four empty files, exiting 0.
  it("tells a broken template apart from missing data", () => {
    const engine = createEngine({ "email.txt": "OBJET: x\n{salutation}\n" });
    expect(() => engine.email({ ...ENDORSER, first_name: "" }, CFG)).toThrow(
      MissingField,
    );
    expect(() =>
      engine.email({ ...ENDORSER, first_name: "" }, CFG),
    ).not.toThrow(InvalidTemplate);
    expect(() => engine.letter(ENDORSER, CFG)).toThrow(InvalidTemplate);
  });

  // A REAL key surrounded by spaces resolves: "{ commune }" means the
  // commune, and writing it that way is not a mistake. What must no longer
  // exist is the silent pass-through — now it either resolves or refuses.
  it.each(["{ salutation }", "{ commune }", "{  nom  }"])(
    "resolves %s, which designates a real field",
    (hole) => {
      const engine = createEngine({ "email.txt": `OBJET: x\n${hole}\n` });
      expect(engine.email(ENDORSER, CFG).body).not.toContain("{");
    },
  );
});

describe("campaign values", () => {
  // config/campagne.yaml itself talks about "{placeholders}": writing
  // "équipe de campagne de {candidat}" is the natural move, and the string
  // went out verbatim to mayors.
  it("are flagged as template when they carry a hole", () => {
    for (const v of [
      "équipe de {candidat}",
      "candidat(e) [courant]",
      "Je suis <qui>",
    ]) {
      expect(unfilledKeys({ ...CFG, signataire_qualite: v })).toContain(
        "signataire_qualite",
      );
    }
  });

  // A decomposed "é" (copy-paste from a PDF) or a zero-width character made
  // the shipped template pass for a filled value.
  it("stay detected despite NFD decomposition or an invisible character", () => {
    expect(unfilledKeys({ ...CFG, candidat: "Prénom NOM" })).toContain(
      "candidat",
    );
    expect(unfilledKeys({ ...CFG, candidat: "Prénom​ NOM" })).toContain(
      "candidat",
    );
  });

  it("are neutralised like the volunteer's text", () => {
    const ch = fields(ENDORSER, {
      ...CFG,
      signataire_qualite: "équipe de {candidat}",
    });
    expect(ch.signataire_qualite).toBe("équipe de (candidat)");
  });

  it("let a genuinely filled campaign through", () => {
    expect(unfilledKeys(CFG)).toEqual([]);
  });
});

describe("the rank", () => {
  // The fallback failed on the wrong side: one uppercase letter in the
  // column is enough to route 3,047 mayors to the thanking template.
  it.each(["Autre", "AUTRE", "inconnu", "gauche_droite", "constructor"])(
    '%s does not resolve into "they endorsed"',
    (value) => {
      expect(rank({ rank: value })).toBe("no_signal");
    },
  );

  it("keeps the has_endorsed fallback when the column is absent", () => {
    // file 01 has no rank column: it only contains endorsers
    expect(rank({})).toBe("has_endorsed");
    expect(rank({ rank: "" })).toBe("has_endorsed");
  });

  it("does not thank an unknown rank, even with the columns filled", () => {
    const engine = createEngine({
      "email.txt": "OBJET: merci\nvous avez présenté {candidat_recent}\n",
      "email_decouverte.txt": "OBJET: découverte\nbonjour {salutation}\n",
    });
    const { subject } = engine.email(
      { ...ENDORSER, rank: "gauche_droite" },
      CFG,
    );
    expect(subject).toBe("découverte");
  });

  // Choosing the template file by rank is not enough on its own. The
  // templates exist to be edited without touching the code, and pasting
  // the thank-you sentence into the discovery one printed "En , vous
  // avez présenté ." — the project's cardinal mistake, in silence.
  it.each(["annee_recente", "candidat_recent", "parrainages"])(
    "refuses {%s} in a discovery template, by name",
    (key) => {
      const engine = createEngine({
        "email.txt": "OBJET: merci\n{salutation}\n",
        "email_decouverte.txt": `OBJET: x\nEn {${key}}, vous avez présenté.\n`,
      });
      expect(() =>
        engine.email({ ...ENDORSER, rank: "no_signal" }, CFG),
      ).toThrow(new RegExp(`email_decouverte\\.txt : \\{${key}\\}`));
    },
  );

  // and symmetrically: the discovery context has nothing to say to
  // someone who did endorse
  it.each(["contexte", "contexte_tel"])(
    "refuses {%s} in the thanking template, by name",
    (key) => {
      const engine = createEngine({
        "email.txt": `OBJET: x\n{salutation}{${key}}\n`,
        "email_decouverte.txt": "OBJET: x\n{salutation}\n",
      });
      expect(() => engine.email(ENDORSER, CFG)).toThrow(
        new RegExp(`email\\.txt : \\{${key}\\}`),
      );
    },
  );

  it("still renders all three channels, at all three ranks", () => {
    const engine = createEngine(REAL_TEMPLATES);
    for (const value of ["has_endorsed", "commune_has_endorsed", "no_signal"]) {
      const m = { ...ENDORSER, rank: value };
      expect(() => {
        engine.email(m, CFG);
        engine.letter(m, CFG);
        engine.phoneScript(m, CFG);
      }, `rank ${value}`).not.toThrow();
    }
  });
});

describe("the communal precedent", () => {
  const communeCase: Mayor = {
    rank: "commune_has_endorsed",
    title: "M.",
    first_name: "Xavier",
    last_name: "BEDOS",
    commune: "Rieux-en-Val",
    department: "Aude",
    insee_code: "11318",
    predecessor: "Jean DUPONT",
    predecessor_mayor: "oui",
  };

  // "Jean Dupont avait présenté ." went out in a printed letter.
  it.each([
    ["empty", ""],
    ["malformed", "parrainage de 2017"],
  ])("states nothing when the history is %s", (_case, hist) => {
    const m = { ...communeCase, endorsement_history: hist };
    expect(context(m)).toBe("");
    expect(fields(m, CFG).contexte_tel).toBe("");
  });

  it("cites the precedent when there is one to cite", () => {
    const m = {
      ...communeCase,
      endorsement_history: "2017: ARTHAUD Nathalie (B)",
    };
    expect(context(m)).toContain("Nathalie Arthaud en 2017");
    expect(fields(m, CFG).contexte_tel).toContain("Nathalie Arthaud en 2017");
  });
});

// The same cases the Go guard answers (api/config_test.go). One file, two
// languages: the server's refusal and the volunteer's banner must draw the
// line in the same place, or the weaker of the two is the one that counts.
describe("the unfilled-template guard, shared with the API", () => {
  const shared = JSON.parse(
    readFileSync(
      join(dirname(fileURLToPath(import.meta.url)), "gabarit-cases.json"),
      "utf8",
    ),
  ) as { cases: { value: string; unfilled: boolean; why: string }[] };

  it("has cases to answer for", () => {
    expect(shared.cases.length).toBeGreaterThan(0);
  });

  it.each(shared.cases)("$why", ({ value, unfilled }) => {
    // one key at a time, the others filled
    const cfg = Object.fromEntries(
      Object.keys(CFG).map((k) => [k, "valeur réelle et remplie"]),
    );
    cfg.signataire_qualite = value;
    expect(unfilledKeys(cfg)).toEqual(unfilled ? ["signataire_qualite"] : []);
  });
});

// "au service de Ambléon" — 257 of the 1 959 priority communes begin with a
// vowel, or the last sentence a mayor reads is ungrammatical. Communes
// carry their article in their name, and the elision depends on it.
describe("the commune in the closing line", () => {
  it.each([
    ["Ambléon", "d'Ambléon"],
    ["Étampes", "d'Étampes"],
    ["Lyon", "de Lyon"],
    ["Le Mans", "du Mans"],
    ["Les Sables-d'Olonne", "des Sables-d'Olonne"],
    ["La Rochelle", "de la Rochelle"],
    ["L'Haÿ-les-Roses", "de L'Haÿ-les-Roses"],
    // French communal toponymy is Germanic, Frankish or Norman: its h is
    // aspirated. The 665 communes beginning with one cluster in Moselle,
    // Alsace, Nord and Picardie, and eliding them is wrong on 44 letters.
    ["Havange", "de Havange"],
    ["Le Havre", "du Havre"],
    ["Honfleur", "de Honfleur"],
    ["Œting", "d'Œting"],
  ])('%s reads "au service %s"', (commune, expected) => {
    expect(elidedCommune(commune)).toBe(expected);
  });

  it("reaches the four templates that close on it", () => {
    const engine = createEngine(REAL_TEMPLATES);
    const m = { ...ENDORSER, commune: "Ambléon" };
    expect(engine.email(m, CFG).body).toContain("au service d'Ambléon");
    expect(engine.letter(m, CFG)).toContain("au service d'Ambléon");
    const discovery = { ...m, rank: "no_signal" };
    expect(engine.email(discovery, CFG).body).toContain("au service d'Ambléon");
    expect(engine.letter(discovery, CFG)).toContain("au service d'Ambléon");
  });

  // GUIDE.md promises the personal touch is inserted automatically; the
  // a letter template that does not carry the placeholder drops it on the
  // printed channel without a word.
  // the first line visible through the envelope window
  it.each([
    ["Le Havre", "Mairie du Havre"],
    ["La Rochelle", "Mairie de la Rochelle"],
    ["Les Andelys", "Mairie des Andelys"],
    ["Ambléon", "Mairie d'Ambléon"],
    ["Havange", "Mairie de Havange"],
  ])('addresses the envelope of %s to "%s"', (commune, expected) => {
    expect(recipientAddress({ ...ENDORSER, commune })).toContain(expected);
  });

  // All FOUR templates, both channels × both ranks. Guarded on one of
  // them, the placeholder could be dropped from courrier_decouverte.txt —
  // the printed channel for 32 854 of the 34 826 mayors, and the one the
  // campaign spends money on — with every suite green.
  it.each([
    ["email", "has_endorsed"],
    ["email", "no_signal"],
    ["letter", "has_endorsed"],
    ["letter", "no_signal"],
  ])(
    "carries the volunteer's personal touch into the %s at rank %s",
    (channel, value) => {
      const engine = createEngine(REAL_TEMPLATES);
      const note = "Je suis moi-même élu d'une commune rurale.";
      const m = { ...ENDORSER, rank: value };
      const text =
        channel === "email"
          ? engine.email(m, CFG, { personalNote: note }).body
          : engine.letter(m, CFG, { personalNote: note });
      expect(text).toContain(note);
      // a paragraph of its own: without the blank line it reads as one
      // sentence with what follows
      expect(text).toContain(`${note}\n\n`);
    },
  );
});

// What a campaign is REQUIRED to fill, and what it is merely offered.
//
// The gate has two consumers that must agree — the banner on every screen and
// the mass mailing's refusal to run, read from TypeScript; /api/config's
// `unfilled`, read from Go — so the list lives in a shared file both answer
// to, exactly like campaign-env.json.
describe("what an unconfigured campaign means", () => {
  const filled = (): Campaign =>
    Object.fromEntries(
      Object.keys(CFG).map((k) => [k, "une valeur réelle"]),
    ) as Campaign;

  it("keeps its own copy in step with the shared list", () => {
    const shared = JSON.parse(
      readFileSync(
        join(dirname(fileURLToPath(import.meta.url)), "campaign-optional.json"),
        "utf8",
      ),
    ) as { optional: string[] };
    expect(shared.optional.length).toBeGreaterThan(0);
    expect([...OPTIONAL_CAMPAIGN_KEYS].sort()).toEqual(
      [...shared.optional].sort(),
    );
  });

  it("does not block a campaign that gives no telephone, site or town", () => {
    const cfg = filled();
    for (const k of OPTIONAL_CAMPAIGN_KEYS) cfg[k] = "";
    expect(unfilledKeys(cfg)).toEqual([]);
  });

  // The distinction the whole change rests on: declining to give a number is
  // a choice, leaving the shipped one is a number that goes out verbatim.
  it("still blocks an optional key left at the shipped template", () => {
    const cfg = filled();
    cfg.contact_tel = "06 00 00 00 00";
    cfg.site = "https://exemple.fr";
    expect(unfilledKeys(cfg)).toEqual(["contact_tel", "site"]);
  });

  it("still blocks a required key that is empty", () => {
    const cfg = filled();
    cfg.signataire = "";
    expect(unfilledKeys(cfg)).toEqual(["signataire"]);
  });

  // What the mayor actually receives once those three may be absent. The
  // signature line joins them with « — », and a missing one used to leave
  // the separator standing at the foot of every letter.
  describe("the signature line", () => {
    const engine = createEngine(REAL_TEMPLATES);

    it("carries no orphan separator when a contact is missing", () => {
      const cfg: Campaign = { ...CFG, contact_tel: "", site: "" };
      for (const text of [
        engine.email(ENDORSER, cfg).body,
        engine.letter(ENDORSER, cfg),
      ]) {
        expect(text).toContain(cfg.contact_email);
        expect(text).not.toMatch(/^ *— /m);
        expect(text).not.toMatch(/ — *$/m);
        expect(text).not.toContain(" —  — ");
      }
    });

    it("is untouched when every contact is there", () => {
      const cfg: Campaign = { ...CFG, contact_tel: "06 12 34 56 78" };
      expect(engine.letter(ENDORSER, cfg)).toContain(
        `06 12 34 56 78 — ${cfg.contact_email}`,
      );
      expect(engine.email(ENDORSER, cfg).body).toContain(
        `06 12 34 56 78 — ${cfg.contact_email} — ${cfg.site}`,
      );
    });

    // WHOEVER SENDS IT IS WHOEVER SIGNS IT, and the letter used not to.
    // It was written at the candidate's own « je » — « Je m'appelle X », « mes
    // idées », « mon équipe » — and signed with the candidate's name, while
    // the person who prints it, stamps it and posts it is a volunteer. Five
    // hundred letters putting words in a candidate's mouth and a signature
    // they never gave.
    //
    // Structural, not prose: every template that LEAVES carries the
    // signatory. The candidate is quoted in it — `candidat_description_longue`
    // is first-person by design — but quoting is announced, and the name at
    // the bottom is the sender's. The telephone scripts are not in this list:
    // nobody signs a phone call.
    it.each(["email", "letter"] as const)(
      "signs the %s with the person who sends it",
      (channel) => {
        // both ranks: the four templates that leave, not one of them
        const noSignal: Mayor = {
          ...ENDORSER,
          rank: "no_signal",
          recent_candidate: "",
          recent_year: "",
          endorsement_history: "",
        };
        for (const mayor of [ENDORSER, noSignal]) {
          const made = engine[channel](mayor, CFG);
          const text = typeof made === "string" ? made : made.body;
          expect(text).toContain(CFG.signataire);
          expect(text).toContain(CFG.signataire_qualite);
          // the last line that names a person is the signatory's, never the
          // candidate's: a message signed by the candidate is one the sender
          // cannot answer for
          const signature = text
            .trimEnd()
            .split("\n")
            .filter(Boolean)
            .slice(-2);
          expect(signature.join("\n")).toContain(CFG.signataire);
          expect(signature.join("\n")).not.toContain(CFG.candidat);
        }
      },
    );
  });
});
