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
  isWoman,
  letterHeader,
  MissingField,
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
