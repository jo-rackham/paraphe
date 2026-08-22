// Message engine — the project's single implementation.
//
// The invariant it carries: "you presented X" is said ONLY to a proven
// endorser (rank has_endorsed). A mayor with no history receives a text
// that ascribes nothing to them. It is the most expensive false positive
// of the whole tool, and that is why this file is shared rather than
// copied: the interface (both modes) and the mass mailing call the same
// code.
//
// The templates are injected: the browser inlines them at build time, the
// command-line tool reads them from disk. The engine itself knows neither
// `fetch` nor `fs`.
//
// The template {placeholders} STAY FRENCH ({prenom}, {commune}…): the
// campaign team edits the templates themselves. fields() is where the
// French placeholder vocabulary maps onto the English data columns.

import { CP1252, isControl, isUppercase, titleCase } from "./texte.ts";

export type Mayor = Record<string, string | null | undefined>;
export type Campaign = Record<string, string>;
export type Templates = Record<string, string>;

export const RANKS: Record<string, string> = {
  has_endorsed: "A déjà parrainé un candidat peu médiatisé",
  commune_has_endorsed: "Sa commune l'a déjà fait (maire différent depuis)",
  no_signal: "Aucun signal connu",
};

// Keys of config/campagne.yaml — French on purpose: the campaign team
// edits that file themselves.
export const CAMPAIGN_KEYS = [
  "candidat",
  "candidat_description",
  "candidat_description_longue",
  "signataire",
  "signataire_qualite",
  "contact_tel",
  "contact_email",
  "site",
  "ville_envoi",
];

// Keys a campaign may leave EMPTY without being told it is unconfigured.
// noyau/campaign-optional.json is the referee both languages answer to, and
// it carries the reasoning; api/config.go holds the other copy.
export const OPTIONAL_CAMPAIGN_KEYS = [
  "contact_email",
  "contact_tel",
  "site",
  "ville_envoi",
];

/**
 * WHO SIGNS is the sender's, not the campaign's — the two keys a campaign
 * hands to nobody.
 *
 * The other seven describe the candidate and how to reach the campaign, and
 * they are meant to travel: that is the whole point of pre-filling. These two
 * name a PERSON. In team mode each volunteer's own account supplies them, so
 * the question never arises; the account-less version has no account, so they
 * fell back to the campaign's — which is whoever filled the form, i.e. the
 * coordination. Every message a volunteer produced went out over the
 * coordinator's name and role, and the volunteer had no way to know.
 *
 * So they are NOT adopted, by either door. They stay at the template value,
 * the screen says so, and the person sending signs for themselves.
 */
export const PERSONAL_CAMPAIGN_KEYS = ["signataire", "signataire_qualite"];

// Values of the shipped template: letting them through would send
// "Prénom NOM" to thousands of mayors.
const TEMPLATE_VALUES = new Set([
  "prénom nom",
  "ville",
  "06 00 00 00 00",
  "contact@exemple.fr",
  "https://exemple.fr",
]);

const MONTHS = [
  "janvier",
  "février",
  "mars",
  "avril",
  "mai",
  "juin",
  "juillet",
  "août",
  "septembre",
  "octobre",
  "novembre",
  "décembre",
];

/** Data missing or unusable FOR THIS MAYOR: skip them, keep going. */
export class MissingField extends Error {}

/**
 * Unusable template: unknown placeholder, missing file, missing header.
 * This affects ALL recipients, never a single one — conflating it with a
 * missing field skipped the 1,972 mayors one by one and replaced the
 * previous mailing with four empty files, exiting 0.
 */
export class InvalidTemplate extends Error {}

/** Value safe to print: controls decoded or removed, spaces collapsed. */
export function clean(v: unknown): string {
  let s = "";
  for (const c of String(v ?? "")) {
    const p = c.codePointAt(0) as number;
    const replacement = CP1252[p];
    // a control becomes a SPACE: removing it glued words together, and
    // five directory cards already carry line breaks in their opening
    // hours
    if (replacement !== undefined) s += replacement;
    else s += isControl(p) ? " " : c;
  }
  return s.replace(/\s{2,}/g, " ").trim();
}

// Brace replacement: nothing reinterprets them, but a "{candidat}" copied
// from an internal template would reach the mayor verbatim.
const volunteerText = (v: unknown): string =>
  clean(v).replaceAll("{", "(").replaceAll("}", ")");

/** 'THOUY Hélène' -> 'Hélène Thouy' (all-uppercase tokens = last name). */
export function proseName(candidate: unknown): string {
  const lastNames: string[] = [];
  const firstNames: string[] = [];
  for (const tok of String(candidate ?? "")
    .split(/\s+/)
    .filter(Boolean)) {
    (isUppercase(tok) ? lastNames : firstNames).push(tok);
  }
  return [...firstNames, ...lastNames.map(titleCase)].join(" ");
}

/**
 * '2017: ARTHAUD Nathalie | 2022: ARTHAUD Nathalie' ->
 * 'Nathalie Arthaud en 2017 et 2022'. Grouped: repeating the same name
 * twice in one sentence reads like a robot wrote it.
 */
export function readableHistory(hist: unknown): string {
  const years = new Map<string, Set<string>>();
  for (const t of String(hist ?? "").split(" | ")) {
    const sep = t.indexOf(": ");
    if (sep < 0) continue;
    const year = t.slice(0, sep);
    const cand = proseName(t.slice(sep + 2).replace(/\s\([AB]\)$/, ""));
    const seen = years.get(cand);
    if (seen) seen.add(year);
    else years.set(cand, new Set([year]));
  }
  return [...years.entries()]
    .map(([cand, ys]) => `${cand} en ${[...ys].sort().join(" et ")}`)
    .join(", ");
}

export function endorsementsProse(raw: unknown): string {
  return String(raw ?? "")
    .split(" | ")
    .filter((t) => t.includes(": "))
    .map((t) => {
      const sep = t.indexOf(": ");
      const cand = t.slice(sep + 2).replace(/\s\([AB]\)$/, "");
      return `${proseName(cand)} (${t.slice(0, sep)})`;
    })
    .join(", ");
}

/**
 * Outreach rank. Decides the template: file 01 only contains endorsers,
 * the full base mixes all three ranks.
 */
export function rank(mayor: Mayor): string {
  const r = clean(mayor.rank);
  // ABSENT (file 01 has no rank column, it only contains endorsers) ->
  // has_endorsed. PRESENT but unknown -> no_signal: a value we do not
  // recognise must never resolve into "you presented X". The fallback
  // failed on the wrong side, and one uppercase letter in the column was
  // enough to thank 3,047 mayors for an endorsement they never made.
  // `Object.hasOwn` and not `in`: `in` walks the prototype, and
  // rank="constructor" passed for valid.
  if (!r) return "has_endorsed";
  return Object.hasOwn(RANKS, r) ? r : "no_signal";
}

/**
 * The mayor's gender. Strict domain: guessing would produce "Monsieur le
 * Maire" addressed to a woman, with nothing signalling it.
 */
export function isWoman(mayor: Mayor): boolean {
  const civ = clean(mayor.title).toUpperCase().replace(/\.$/, "");
  if (civ !== "M" && civ !== "MME") {
    throw new MissingField(
      `civilité non reconnue : ${JSON.stringify(mayor.title)} ` +
        `(${mayor.first_name} ${mayor.last_name}, ${mayor.commune})`,
    );
  }
  return civ === "MME";
}

/**
 * HOW A MAYOR IS NAMED, decided once.
 *
 * The same elected official used to be addressed three ways on one contact:
 * « Mme le Maire » through the envelope window, « Madame la Maire » at the
 * head of the letter and the email, and « Madame le Maire » in the words the
 * volunteer speaks on the telephone. Whichever form she prefers, a tool that
 * cannot hold to one has chosen none — and the volunteer is the one who looks
 * careless.
 *
 * The feminine article is what the letter has always used and what a large
 * share of women mayors ask for; the Académie française dropped its objection
 * in 2019. The two others followed it rather than the reverse.
 */
export function mayorTitle(mayor: Mayor): string {
  return isWoman(mayor) ? "Madame la Maire" : "Monsieur le Maire";
}

/**
 * Rank-specific opening sentence. It only states the verifiable: what the
 * commune did, or what the official signed — never an intention.
 */
export function context(mayor: Mayor): string {
  if (rank(mayor) !== "commune_has_endorsed") return "";
  // `readableHistory()` returns "" as soon as no token is usable: without
  // this guard, the printed letter stated "Jean Dupont avait présenté ."
  const cited = readableHistory(clean(mayor.endorsement_history));
  if (!cited) return "";
  const who = proseName(clean(mayor.predecessor));
  // "la municipalité précédente" only holds if the endorser held the mayor's
  // office: a deputy mayor may still be in office today
  const wasMayor = clean(mayor.predecessor_mayor) === "oui";
  let lead: string;
  if (who && wasMayor)
    lead = `sous la municipalité précédente : ${who} avait présenté `;
  else if (who)
    lead = `par le passé : ${who}, élu de votre commune, a présenté `;
  else lead = "par le passé : ";
  return (
    `\nVotre commune a d'ailleurs déjà usé de cette possibilité ${lead}` +
    `${cited}. C'est ce précédent qui nous conduit à vous écrire à ` +
    "vous plutôt qu'à d'autres.\n"
  );
}

// Fields with no acceptable fallback: better not to write than to write
// wrong.
const REQUIRED_FIELDS = ["first_name", "last_name", "commune"];
const REQUIRED_FIELDS_ENDORSER = [
  ...REQUIRED_FIELDS,
  "recent_candidate",
  "recent_year",
];

export interface Options {
  signer?: string;
  personalNote?: string;
  /** Letter date; injectable so the tests stay reproducible. */
  today?: Date;
  /**
   * Whether this campaign telephones the mayors it writes to. OPT-IN, so
   * `false` is the answer when nobody has said.
   *
   * The email asked « accepteriez-vous d'en échanger quelques minutes par
   * téléphone » and the letter announced « nous nous permettrons de vous
   * appeler dans les prochaines semaines » — unconditionally, including for
   * campaigns that had given no telephone number and run no calling. A
   * promise made to five hundred elected officials by a tool, on behalf of
   * people who never made it, and the mayor who waits for that call is the
   * one who paid for it.
   */
  phoneOutreach?: boolean;
}

// The returned keys are the template placeholders — FRENCH, because the
// campaign team writes "{prenom}" and "{commune}" in modeles/*.txt. This
// function is the French-vocabulary ↔ English-columns boundary.
export function fields(
  mayor: Mayor,
  cfg: Campaign,
  opts: Options = {},
): Record<string, string> {
  const {
    signer = "",
    personalNote = "",
    today = new Date(),
    phoneOutreach = false,
  } = opts;
  const required =
    rank(mayor) === "has_endorsed" ? REQUIRED_FIELDS_ENDORSER : REQUIRED_FIELDS;
  const empty = required.filter((k) => !clean(mayor[k]));
  if (empty.length) {
    throw new MissingField(
      `champs vides, message non générable : ${empty.join(", ")} ` +
        `(INSEE ${mayor.insee_code}, ${mayor.commune})`,
    );
  }
  const woman = isWoman(mayor);
  const phoneHistory = readableHistory(clean(mayor.endorsement_history));
  const endorser = rank(mayor) === "has_endorsed";
  // The endorsement placeholders exist ONLY for someone who endorsed, and
  // the discovery ones ONLY for someone who did not. Choosing the template
  // file by rank is not enough on its own: the templates are made to be
  // edited without touching the code, and pasting "En {annee_recente},
  // vous avez présenté {candidat_recent}." into a discovery template
  // printed "En , vous avez présenté ." to 32 866 mayors, in silence.
  // Omitted here, the same paste makes render() throw by name.
  const byRank: Record<string, string> = endorser
    ? {
        candidat_recent: clean(mayor.recent_candidate),
        annee_recente: clean(mayor.recent_year),
        parrainages: endorsementsProse(
          clean(
            mayor.small_candidate_endorsements || mayor.endorsement_history,
          ),
        ),
      }
    : {
        contexte: context(mayor),
        // same guard as context(): without it, the phone script dictated
        // "Sa commune l'a déjà fait sous une précédente municipalité : ."
        // THE SAME HEDGE THE LETTER MAKES, and for the same reason: the
        // predecessor is often not the previous MAYOR — a deputy mayor is
        // still in office, and a 2017 endorsement is two municipal terms
        // back. Stated flat, the volunteer repeats aloud, to the elected
        // official of that very commune, a claim about its political past
        // that the data does not establish and that they can contradict on
        // the spot. `context()` has branched on `predecessor_mayor` since it
        // was written; this line had not.
        contexte_tel:
          rank(mayor) === "commune_has_endorsed" && phoneHistory
            ? ` Sa commune l'a déjà fait ${
                clean(mayor.predecessor_mayor) === "oui"
                  ? "sous une précédente municipalité"
                  : "par le passé"
              } : ${phoneHistory}.`
            : "",
      };
  return {
    ...byRank,
    salutation: mayorTitle(mayor),
    // The SAME words, short: what the phone script speaks and what the
    // envelope carries. Three spellings of one woman's title used to reach
    // her on one contact — « Mme le Maire » on the envelope, « Madame la
    // Maire » in the letter, « Madame le Maire » on the telephone. Whichever
    // she prefers, a tool that cannot keep to one is a tool that chose none.
    salutation_courte: mayorTitle(mayor),
    civilite: woman ? "Mme" : "M.",
    civilite_courte: woman ? "Madame" : "Monsieur",
    seul_e: woman ? "seule" : "seul",
    sollicite_e: woman ? "sollicitée" : "sollicité",
    // The phone script is SPOKEN, and « je peux le/la joindre » is a slash a
    // volunteer has to resolve out loud while the secretary waits. The sex
    // code that decides every other agreement on this page decides this one.
    le_la: woman ? "la" : "le",
    // THE TWO PHONE SENTENCES, and they are empty unless the campaign said
    // it telephones. Written here rather than in the templates for the
    // reason `contexte` above is: a sentence that must sometimes not exist
    // at all cannot be a fixed line of a file, and the engine already drops
    // a paragraph whose only content came out empty.
    //
    // One per channel because they say different things: the email ASKS the
    // mayor, the letter ANNOUNCES a call. The agreement comes from the same
    // `woman` as everything else — a sentence generated in code is still a
    // sentence a mayor reads.
    proposition_telephone: phoneOutreach
      ? "Accepteriez-vous d'en échanger quelques minutes par téléphone, ou " +
        `que ${volunteerText(cfg.candidat)} vous appelle personnellement ?`
      : "",
    relance_telephone: phoneOutreach
      ? "Nous nous permettrons de vous appeler dans les prochaines " +
        `semaines. Si vous préférez ne pas être ${
          woman ? "sollicitée" : "sollicité"
        }, un mot suffira et nous le respecterons.`
      : "",
    prenom: clean(mayor.first_name),
    nom: clean(mayor.last_name),
    commune: clean(mayor.commune),
    // "au service de Ambléon" — 257 of the 1 959 priority communes begin
    // with a vowel or a mute h, so the last sentence a mayor reads is
    // ungrammatical. The elision is a field of its own: the templates are
    // edited by the campaign, and none of them should have to know French
    // orthography.
    commune_de: elidedCommune(clean(mayor.commune)),
    departement: clean(mayor.department),
    email: clean(mayor.email),
    telephone: clean(mayor.phone) || "numéro non renseigné",
    horaires: clean(mayor.town_hall_hours) || "horaires non renseignés",
    adresse: clean(mayor.postal_address),
    code_postal: clean(mayor.postal_code),
    ville: clean(mayor.city),
    date: `${today.getDate()} ${MONTHS[today.getMonth()]} ${today.getFullYear()}`,
    // Configuration values go through the same filter as the volunteer's
    // text. `config/campagne.yaml` itself invites talking about
    // "{placeholders}": writing "équipe de campagne de {candidat}" is the
    // natural move, and the string went out verbatim to mayors.
    candidat: volunteerText(cfg.candidat),
    candidat_description: volunteerText(cfg.candidat_description),
    candidat_description_longue: volunteerText(cfg.candidat_description_longue),
    signataire: volunteerText(signer) || volunteerText(cfg.signataire),
    signataire_qualite: volunteerText(cfg.signataire_qualite),
    contact_tel: volunteerText(cfg.contact_tel),
    contact_email: volunteerText(cfg.contact_email),
    site: volunteerText(cfg.site),
    ville_envoi: volunteerText(cfg.ville_envoi),
    argument_personnel: volunteerText(personalNote),
  };
}

/**
 * "Rodez, le 9 août 2026" — the dateline a posted letter carries above its
 * text. `ville_envoi` appears in no template: this is its only use, and both
 * the mass mailing and the on-screen letter must show the same one.
 */
export function letterHeader(
  mayor: Mayor,
  cfg: Campaign,
  opts: Options = {},
): string {
  return `${cfg.ville_envoi}, le ${fields(mayor, cfg, opts).date}`;
}

/**
 * "de Lyon", "d'Ambléon", "du Mans", "des Sables-d'Olonne". Communes carry
 * their article in their name, and the elision depends on it.
 */
export function elidedCommune(commune: string): string {
  if (!commune) return "";
  // THE POSTAL INVERSION FIRST. Official lists write « Chalesmes (Les) »,
  // « Rochelle (La) »: the article is the same one the rules below elide, it
  // has only been moved to the end. Read left to right, the name began with
  // no article at all and came out « Mairie de Chalesmes (Les) ».
  const inverted = /^(.*?),?\s*\((le|la|les|l['\u2019])\)$/i.exec(commune);
  if (inverted) {
    // the article AS WRITTEN, so the two spellings of one commune come out
    // identical: \u00ab L'Ha\u00ff-les-Roses \u00bb and \u00ab Ha\u00ff-les-Roses (L') \u00bb both address
    // the same town hall, and the second is not the place to lose a capital
    const [, bare, article] = inverted;
    const glued = /^l['\u2019]$/i.test(article);
    return elidedCommune(
      glued ? `${article}${bare.trim()}` : `${article} ${bare.trim()}`,
    );
  }
  const lower = commune.toLowerCase();
  if (lower.startsWith("le ")) return `du ${commune.slice(3)}`;
  if (lower.startsWith("les ")) return `des ${commune.slice(4)}`;
  if (lower.startsWith("la ")) return `de la ${commune.slice(3)}`;
  if (lower.startsWith("l'") || lower.startsWith("l\u2019")) {
    return `de ${commune}`;
  }
  // NOT the h. French communal toponymy is overwhelmingly Germanic,
  // Frankish or Norman in origin, so its h is aspirated: the 665 communes
  // beginning with one cluster in Moselle, Alsace, Nord, Pas-de-Calais and
  // Picardie. "de Havange" is right and "d'Havange" is wrong — eliding
  // them cost 44 letters and 44 emails against about five rare misses the
  // other way ("de Hyères").
  return /^[aeiouyàâäéèêëîïôöùûüœæ]/i.test(commune)
    ? `d'${commune}`
    : `de ${commune}`;
}

export function postalCity(mayor: Mayor): string {
  const raw = String(mayor.city ?? "");
  const hasControl = [...raw].some((c) =>
    isControl(c.codePointAt(0) as number),
  );
  if (hasControl) return clean(mayor.commune);
  return clean(raw) || clean(mayor.commune);
}

export function recipientAddress(mayor: Mayor): string {
  const lines = [
    // the SAME title as the letter it encloses and the script that follows
    // it: « Mme le Maire » here against « Madame la Maire » inside was one
    // person named two ways on one contact
    `${mayorTitle(mayor)} — ${clean(mayor.first_name)} ${clean(mayor.last_name)}`,
    // the first line visible through the envelope window: "Mairie de Le
    // Havre" on 110 of the 1 958 letters
    `Mairie ${elidedCommune(clean(mayor.commune))}`,
  ];
  if (clean(mayor.postal_address)) lines.push(clean(mayor.postal_address));
  lines.push(`${clean(mayor.postal_code)} ${postalCity(mayor)}`.trim());
  return lines.join("\n");
}

const RX_EMAIL = /^[^@\s,;]+@[^@\s,;]+\.[A-Za-z]{2,}$/;

/**
 * The directory sometimes concatenates two addresses in the same field
 * ("a@x.fr;b@y.fr"): as is, that is an invalid address for any sending
 * tool.
 */
export function emailAddresses(mayor: Mayor): {
  valid: string[];
  rejected: string[];
} {
  const valid: string[] = [];
  const rejected: string[] = [];
  for (const a of clean(mayor.email).split(/[;,]/)) {
    const t = a.trim();
    if (!t) continue;
    (RX_EMAIL.test(t) ? valid : rejected).push(t);
  }
  return { valid, rejected };
}

/**
 * Why the letter would not be deliverable, else null. Without a postal
 * code, La Poste does not route: better to know before printing than to
 * discover the return three weeks later.
 */
export function incompleteAddress(mayor: Mayor): string | null {
  if (!/^\d{5}$/.test(clean(mayor.postal_code))) {
    return "code postal manquant ou invalide";
  }
  if (!postalCity(mayor)) return "commune de destination manquante";
  return null;
}

/** Keys still at their template value: they would reach mayors verbatim. */
export function unfilledKeys(cfg: Campaign): string[] {
  return CAMPAIGN_KEYS.filter((k) => {
    // normalised and stripped of zero-width characters: a decomposed "é"
    // (copy-paste from a PDF) or an invisible U+200B made the shipped
    // template pass for a filled value
    // NOT `\s`: JavaScript's leaves U+0085 (NEL) alone while `trim()` folds
    // it, and Go's guard — the other half of this decision — folds it
    // everywhere. The class is written out so both sides fold the same set;
    // widening one for the non-breaking space is what opened the gap here.
    const v = String(cfg[k] ?? "")
      .normalize("NFC")
      .replace(/[\u200b-\u200d\ufeff]/g, "")
      .replace(
        /[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+/g,
        " ",
      )
      .trim();
    // EMPTY and TEMPLATE are two different states, and only the first is a
    // choice. A campaign with no telephone and no website is a campaign, not
    // a misconfiguration; a `contact_tel` still reading « 06 00 00 00 00 »
    // is a number that reaches five hundred mayors verbatim, whether or not
    // the key is optional.
    if (!v) return !OPTIONAL_CAMPAIGN_KEYS.includes(k);
    return (
      TEMPLATE_VALUES.has(v.toLowerCase()) ||
      // the template's three placeholder syntaxes: [qui], {candidat}, <quoi>
      /\[[^\]]+\]/.test(v) ||
      /\{[^}]+\}/.test(v) ||
      /<[^>]+>/.test(v)
    );
  });
}

/**
 * The six files a campaign sends, named ONCE.
 *
 * Three readers used to carry their own copy of this list — the browser
 * inlines them at build time, the mass mailing reads them off disk, and the
 * tests read them again — so a seventh template, or a rename, was three edits
 * with nothing to notice a missed one. It is also what the API answers with
 * when it says which of them a campaign has rewritten, and a name a campaign
 * may store has to be a name that is rendered.
 */
export const TEMPLATE_FILES = [
  "email.txt",
  "email_decouverte.txt",
  "courrier.txt",
  "courrier_decouverte.txt",
  "telephone.txt",
  "telephone_decouverte.txt",
];

/**
 * The longest ONE template may be, in runes.
 *
 * The server applies it (`api/templates.go`, `maxTemplateRunes`, where the
 * number is arithmetic against the body limit); this copy exists because the
 * account-less version accepts templates from a campaign it adopts and has no
 * server to ask. A campaign whose answer carried a megabyte of "template"
 * would fill a volunteer's IndexedDB in one click, and this mode's whole
 * promise is that it holds only what its owner put there.
 *
 * Two copies, held together by a canary — `outils/deploiement.test.ts`, the
 * same dispositif as the password floor one file over.
 */
export const MAX_TEMPLATE_RUNES = 5000;

/**
 * The {placeholders} ONE template file may use, shown to whoever edits it.
 *
 * Derived from `fields()` rather than listed again: a vocabulary written out
 * by hand beside the one that renders is the copy that stops matching, and the
 * screen would then advertise a field the engine refuses — or hide one it
 * accepts. The rank comes from the FILE NAME, exactly as `createEngine`
 * chooses which file to render, which is what keeps the endorsement
 * placeholders out of a discovery template and the discovery context out of a
 * thank-you.
 */
export function placeholderNames(file: string): string[] {
  const sample: Mayor = {
    rank: file.includes("_decouverte") ? "no_signal" : "has_endorsed",
    title: "M.",
    first_name: "x",
    last_name: "x",
    commune: "x",
    recent_candidate: "x",
    recent_year: "2022",
  };
  const cfg: Campaign = Object.fromEntries(CAMPAIGN_KEYS.map((k) => [k, ""]));
  return Object.keys(fields(sample, cfg)).sort();
}

/**
 * The image's templates, then the campaign's, then the team's — LAST WINS,
 * key by key.
 *
 * A campaign rewrites the texts it sends, and a team of that campaign
 * rewrites them again for its own department. What a layer does not mention
 * it INHERITS: a team that customised the email alone keeps following the
 * campaign's letter, including when the campaign changes its mind — freezing
 * the day's version instead would make every later correction invisible to
 * the teams that had touched anything at all. It is the rule
 * `accounts.phone_outreach` already follows one field over, and for the same
 * reason.
 *
 * An EMPTY string is a layer saying nothing, not a template of nothing. The
 * difference matters because it is the shape a textarea sends when the person
 * editing it selects all and deletes: taken literally, that is a campaign
 * whose letter renders as one blank page, five hundred times. « Revenir au
 * texte fourni » and « I have not touched this » are the same act here, and a
 * campaign that genuinely wants to send nothing has no business sending.
 */
export function mergeTemplates(
  base: Templates,
  ...layers: (Templates | null | undefined)[]
): Templates {
  const merged: Templates = { ...base };
  for (const layer of layers) {
    for (const [name, text] of Object.entries(layer ?? {})) {
      if (typeof text === "string" && text.trim() !== "") merged[name] = text;
    }
  }
  return merged;
}

export interface Engine {
  email(
    mayor: Mayor,
    cfg: Campaign,
    opts?: Options,
  ): { subject: string; body: string };
  letter(mayor: Mayor, cfg: Campaign, opts?: Options): string;
  phoneScript(mayor: Mayor, cfg: Campaign, opts?: Options): string;
}

/**
 * Why this SET of templates could not be used, or null — the engine's own
 * refusal, obtained by running it.
 *
 * `api/templates.go` reproduces these rules in Go so a campaign with a server
 * is refused at SAVE rather than at send. The account-less version has no
 * server to ask, and this is what it asks instead: the engine itself, against
 * a mayor who does not exist, at BOTH ranks and on all three channels —
 * because a template is chosen by rank, and checking one rank leaves the other
 * to be discovered by a mayor.
 *
 * `MissingField` is deliberately not reported. That is DATA missing for one
 * mayor, which every real list has some of; it says nothing about the text.
 */
export function invalidTemplate(templates: Templates): string | null {
  const engine = createEngine(templates);
  const cfg: Campaign = Object.fromEntries(
    CAMPAIGN_KEYS.map((k) => [k, `valeur de ${k}`]),
  );
  const sample: Mayor = {
    title: "Mme",
    first_name: "Prénom",
    last_name: "NOM",
    commune: "Commune",
    department: "Département",
    insee_code: "00000",
    recent_candidate: "Candidat",
    recent_year: "2022",
    endorsement_history: "2022: NOM Candidat (A)",
    predecessor: "Prédécesseur NOM",
    predecessor_mayor: "oui",
  };
  for (const rank of ["has_endorsed", "no_signal"]) {
    const mayor = { ...sample, rank };
    for (const render of [engine.email, engine.letter, engine.phoneScript]) {
      try {
        render(mayor, cfg);
      } catch (e) {
        if (e instanceof InvalidTemplate) return e.message;
      }
    }
  }
  return null;
}

/**
 * Binds the engine to a set of templates. "email" -> "email.txt" for a
 * known endorser, "email_decouverte.txt" otherwise: THIS choice is what
 * guarantees nobody is ever thanked for an endorsement they never made.
 * The template file names stay French: they live in modeles/, which the
 * campaign team edits.
 */
export function createEngine(templates: Templates): Engine {
  const templateName = (base: string, mayor: Mayor): string =>
    rank(mayor) === "has_endorsed" ? `${base}.txt` : `${base}_decouverte.txt`;

  function render(name: string, ch: Record<string, string>): string {
    const template = templates[name];
    if (template === undefined) {
      throw new InvalidTemplate(
        `modèle absent : ${name} — connus : ${Object.keys(templates).sort().join(", ")}`,
      );
    }
    // `\w` only covers [A-Za-z0-9_]: "{prénom}", "{ commune }" and
    // "{code-postal}" walked through the guard and went out in the clear.
    // Editing the templates is a goal of the project, and "{prénom}" is
    // what a French speaker writes spontaneously.
    const text = template.replace(/\{([^{}]+)\}/g, (_, raw: string) => {
      const key = raw.trim();
      if (!Object.hasOwn(ch, key)) {
        throw new InvalidTemplate(
          `placeholder inconnu dans le modèle ${name} : {${raw}} — champs ` +
            `disponibles : ${Object.keys(ch).sort().join(", ")}`,
        );
      }
      return ch[key];
    });
    // A SEPARATOR WITH NOTHING ON ONE SIDE IS A SEPARATOR NOBODY WROTE.
    //
    // The four templates sign off with the campaign's contact details joined
    // by « — », and three of those are optional: a team may give no
    // telephone number, run without a website, and not name the town its
    // letters leave from. Substituted naively, a missing one leaves
    // « — contact@… » at the foot of every letter — the orphan paragraph the
    // rule below already refuses, one punctuation mark smaller.
    //
    // The line is rebuilt from its own parts, so a line where nothing came
    // out empty is returned unchanged, byte for byte. A line of prose that
    // happens to use « — » has no empty part and passes through the same
    // way; only a placeholder that resolved to nothing can make one.
    const joined = text
      .split("\n")
      .map((line) =>
        line.includes(" — ")
          ? line
              .split(" — ")
              .filter((part) => part.trim() !== "")
              .join(" — ")
          : line,
      )
      .join("\n");
    // an empty {argument_personnel} must not leave an orphan paragraph
    return (
      joined
        .replaceAll("\r\n", "\n")
        .replaceAll("\r", "\n")
        .replace(/[ \t]+\n/g, "\n")
        .replace(/\n{3,}/g, "\n\n")
        .trim() + "\n"
    );
  }

  return {
    email(mayor, cfg, opts) {
      const name = templateName("email", mayor);
      const text = render(name, fields(mayor, cfg, opts));
      const brk = text.indexOf("\n");
      const first = text.slice(0, brk);
      if (!first.startsWith("OBJET:")) {
        throw new InvalidTemplate(`${name} doit commencer par 'OBJET:'`);
      }
      return {
        subject: first.slice("OBJET:".length).trim(),
        body: text.slice(brk + 1).trim() + "\n",
      };
    },
    letter: (mayor, cfg, opts) =>
      render(templateName("courrier", mayor), fields(mayor, cfg, opts)),
    phoneScript: (mayor, cfg, opts) =>
      render(templateName("telephone", mayor), fields(mayor, cfg, opts)),
  };
}
