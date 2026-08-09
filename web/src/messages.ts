// The message engine, bound to the repository's templates.
//
// The templates live in `modeles/` at the root, once: the mass mailing
// reads the same files. Having had two identical copies was only a
// divergence in waiting — a text fixed on one side, sent from the other.

import courrierTxt from "../../modeles/courrier.txt?raw";
import courrierDecouverteTxt from "../../modeles/courrier_decouverte.txt?raw";
import emailTxt from "../../modeles/email.txt?raw";
import emailDecouverteTxt from "../../modeles/email_decouverte.txt?raw";
import telephoneTxt from "../../modeles/telephone.txt?raw";
import telephoneDecouverteTxt from "../../modeles/telephone_decouverte.txt?raw";

import { createEngine } from "../../noyau/messages.ts";

const engine = createEngine({
  "email.txt": emailTxt,
  "email_decouverte.txt": emailDecouverteTxt,
  "courrier.txt": courrierTxt,
  "courrier_decouverte.txt": courrierDecouverteTxt,
  "telephone.txt": telephoneTxt,
  "telephone_decouverte.txt": telephoneDecouverteTxt,
});

export const { email, letter, phoneScript } = engine;

export {
  CAMPAIGN_KEYS, clean, context, emailAddresses, fields, incompleteAddress,
  isWoman, letterHeader, MissingField, endorsementsProse, postalCity, proseName,
  RANKS, rank, readableHistory, recipientAddress, unfilledKeys,
} from "../../noyau/messages.ts";
export type { Campaign, Mayor, Options } from "../../noyau/messages.ts";
