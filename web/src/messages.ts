// The message engine, bound to the repository's templates.
//
// The templates live in `modeles/` at the root, once: the mass mailing
// reads the same files. Two identical copies would only be a
// divergence in waiting — a text fixed on one side, sent from the other.

import courrierTxt from "../../modeles/courrier.txt?raw";
import courrierDecouverteTxt from "../../modeles/courrier_decouverte.txt?raw";
import emailTxt from "../../modeles/email.txt?raw";
import emailDecouverteTxt from "../../modeles/email_decouverte.txt?raw";
import telephoneTxt from "../../modeles/telephone.txt?raw";
import telephoneDecouverteTxt from "../../modeles/telephone_decouverte.txt?raw";

import {
  createEngine,
  type Engine,
  mergeTemplates,
  type Templates,
} from "../../noyau/messages.ts";

/**
 * The templates the IMAGE carries — what every campaign sends until it writes
 * its own, and what it keeps inheriting for the ones it never touches.
 */
export const SHIPPED_TEMPLATES: Templates = {
  "email.txt": emailTxt,
  "email_decouverte.txt": emailDecouverteTxt,
  "courrier.txt": courrierTxt,
  "courrier_decouverte.txt": courrierDecouverteTxt,
  "telephone.txt": telephoneTxt,
  "telephone_decouverte.txt": telephoneDecouverteTxt,
};

const engine = createEngine(SHIPPED_TEMPLATES);

/**
 * The six templates as the SCREENS name them — the editor's selector, and the
 * card panels that say which one they render.
 *
 * ONE list, because the trap it closes is a vocabulary split: the editor said
 * « Email — maire sans parrainage connu » while the card said nothing at all,
 * and a volunteer who customised that file, came back to a selector reset to
 * the first « Email », read the default text as their work lost — then saved
 * their rewrite into the OTHER file, and the card kept rendering the first
 * version. Both screens now speak these exact words.
 */
export const CHANNELS: { file: string; label: string; audience: string }[] = [
  { file: "email.txt", label: "Email", audience: "maire qui a déjà parrainé" },
  {
    file: "email_decouverte.txt",
    label: "Email",
    audience: "maire sans parrainage connu",
  },
  {
    file: "courrier.txt",
    label: "Courrier",
    audience: "maire qui a déjà parrainé",
  },
  {
    file: "courrier_decouverte.txt",
    label: "Courrier",
    audience: "maire sans parrainage connu",
  },
  {
    file: "telephone.txt",
    label: "Script téléphone",
    audience: "maire qui a déjà parrainé",
  },
  {
    file: "telephone_decouverte.txt",
    label: "Script téléphone",
    audience: "maire sans parrainage connu",
  },
];

/**
 * The template file a channel renders for a mayor of this rank — the same
 * choice `createEngine` makes, spelt once for the screens: "has_endorsed"
 * thanks an endorser, every other rank gets the discovery file.
 */
export function channelFile(base: string, rank: string): string {
  return rank === "has_endorsed" ? `${base}.txt` : `${base}_decouverte.txt`;
}

/**
 * Whether any layer rewrites this file — the editor's own « (personnalisé) »
 * marker, answered with `mergeTemplates`' reading of a layer: an empty string
 * is a layer saying nothing, not a template of nothing.
 */
export function customized(
  file: string,
  layers: (Templates | null | undefined)[],
): boolean {
  return layers.some((l) => (l?.[file] ?? "").trim() !== "");
}

/**
 * The engine THIS volunteer's messages come out of: the image's texts, then
 * their campaign's, then their team's.
 *
 * MEMOISED ON THE LAYERS, because `Fiche` renders on every keystroke in the
 * note field and rebuilding six templates each time would be six string
 * copies per character. Keyed by identity, which is what React gives a value
 * read out of a state object — and the cache holds ONE entry, since a session
 * has one campaign and one team.
 *
 * Absent layers give back the module engine unchanged, so browser mode and
 * every test that renders a card keep the exact object they had.
 */
let lastLayers: (Templates | null | undefined)[] = [];
let lastEngine = engine;
export function engineFor(...layers: (Templates | null | undefined)[]): Engine {
  const empty = layers.every((l) => !l || Object.keys(l).length === 0);
  if (empty) return engine;
  if (
    layers.length === lastLayers.length &&
    layers.every((l, i) => l === lastLayers[i])
  ) {
    return lastEngine;
  }
  lastLayers = layers;
  lastEngine = createEngine(mergeTemplates(SHIPPED_TEMPLATES, ...layers));
  return lastEngine;
}

export const { email, letter, phoneScript } = engine;

export type {
  Campaign,
  Engine,
  Mayor,
  Options,
  Templates,
} from "../../noyau/messages.ts";
export {
  CAMPAIGN_KEYS,
  clean,
  context,
  emailAddresses,
  endorsementsProse,
  fields,
  incompleteAddress,
  invalidTemplate,
  isWoman,
  letterHeader,
  MAX_TEMPLATE_RUNES,
  MissingField,
  mergeTemplates,
  PERSONAL_CAMPAIGN_KEYS,
  placeholderNames,
  postalCity,
  proseName,
  RANKS,
  rank,
  readableHistory,
  recipientAddress,
  TEMPLATE_FILES,
  unfilledKeys,
} from "../../noyau/messages.ts";
