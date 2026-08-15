// Campaign configuration for the command-line tools.
//
// Three layers, least to most specific: config/campagne.yaml (versioned
// template), config/campagne.local.yaml (real values, git-ignored),
// PARAPHE_* environment variables. Same contract as the Go API: filling
// the configuration never modifies a git-tracked file, so nothing personal
// goes to the repository.

import { existsSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "yaml";

import { CAMPAIGN_KEYS, unfilledKeys, type Campaign, type Templates }
  from "../noyau/messages.ts";

export const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");

/**
 * The environment variable that overrides each campaign key. Not derivable
 * by uppercasing: the keys are French — they name campagne.yaml entries and
 * the {placeholders} a team edits — while the variables are English, read by
 * whoever operates the service. `noyau/campaign-env.json` holds the same
 * table, and the tests of both languages check theirs against it.
 */
export const CAMPAIGN_ENV: Record<string, string> = {
  candidat: "PARAPHE_CANDIDATE",
  candidat_description: "PARAPHE_CANDIDATE_DESCRIPTION",
  candidat_description_longue: "PARAPHE_CANDIDATE_DESCRIPTION_LONG",
  signataire: "PARAPHE_SIGNATORY",
  signataire_qualite: "PARAPHE_SIGNATORY_ROLE",
  contact_tel: "PARAPHE_CONTACT_PHONE",
  contact_email: "PARAPHE_CONTACT_EMAIL",
  site: "PARAPHE_SITE",
  ville_envoi: "PARAPHE_SENDING_CITY",
};

export interface Config {
  campaign: Campaign;
  batchSize: number;
  unfilled: string[];
}

interface ConfigFile {
  campagne?: Record<string, unknown>;
  app?: { taille_lot?: unknown };
}

/**
 * `strict` refuses values still at the template ("Prénom NOM"): always
 * true for a mass mailing. Letting them through would send placeholders to
 * thousands of mayors: 1,934 emails on the real list.
 */
export function loadConfig({ strict = true } = {}): Config {
  const campaign: Campaign = {};
  let batchSize = 0;

  const base = join(ROOT, "config", "campagne.yaml");
  if (!existsSync(base)) {
    throw new Error(`configuration absente : ${base}`);
  }
  for (const path of [base, join(ROOT, "config", "campagne.local.yaml")]) {
    if (!existsSync(path)) continue;
    const f = parse(readFileSync(path, "utf8")) as ConfigFile;
    for (const [k, v] of Object.entries(f.campagne ?? {})) campaign[k] = String(v);
    if (typeof f.app?.taille_lot === "number") batchSize = f.app.taille_lot;
  }
  for (const k of CAMPAIGN_KEYS) {
    const v = (process.env[CAMPAIGN_ENV[k]] ?? "").trim();
    if (v) campaign[k] = v;
  }
  const batch = (process.env.PARAPHE_BATCH_SIZE ?? "").trim();
  if (batch) {
    const n = Number(batch);
    if (!Number.isInteger(n)) throw new Error(`PARAPHE_BATCH_SIZE = ${batch} : entier attendu`);
    batchSize = n;
  }

  const missing = CAMPAIGN_KEYS.filter((k) => !(campaign[k] ?? "").trim());
  if (batchSize < 1) missing.push("taille_lot (entier ≥ 1)");
  if (missing.length) {
    throw new Error(
      `configuration incomplète (${base} ou variables PARAPHE_*) : `
      + missing.join(", "));
  }

  const unfilled = unfilledKeys(campaign);
  if (unfilled.length && strict) {
    throw new Error(
      "valeurs de gabarit non remplies (elles partiraient telles quelles aux "
      + `maires) : ${unfilled.join(", ")} — voir config/campagne.yaml ou les `
      + "variables PARAPHE_*");
  }
  return { campaign, batchSize, unfilled };
}

const TEMPLATE_NAMES = ["email", "email_decouverte", "courrier",
  "courrier_decouverte", "telephone", "telephone_decouverte"];

/** The repository's templates, editable without touching the code. */
export function loadTemplates(): Templates {
  const templates: Templates = {};
  for (const name of TEMPLATE_NAMES) {
    templates[`${name}.txt`] = readFileSync(join(ROOT, "modeles", `${name}.txt`), "utf8");
  }
  return templates;
}
