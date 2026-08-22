// The server answers every test suite makes up, in ONE place.
//
// Five files were each carrying their own copy of `ServerConfig`, and adding
// one field to it meant five edits — the last one, `magic_link`, took four.
// The shape is the API's contract with the interface: it belongs somewhere a
// single change can reach.

import { CAMPAIGN_KEYS } from "../../../noyau/messages.ts";
import type { InstanceConfig, Me, ServerConfig } from "../types.ts";

/** A campaign whose nine values are all filled: no "not configured" banner. */
export const teamConfig = (over: Partial<ServerConfig> = {}): ServerConfig => ({
  mode: "team",
  campaign: Object.fromEntries(CAMPAIGN_KEYS.map((k) => [k, `valeur de ${k}`])),
  batch_size: 10,
  unfilled: [],
  source_url: "",
  magic_link: false,
  browser_version_url: "",
  base_domain: "",
  departments: [],
  logo: null,
  statuses: [{ key: "to_contact", label: "À contacter", colour: "#eee" }],
  ranks: [{ key: "has_endorsed", label: "A parrainé" }],
  ...over,
});

/** The apex of a multi-campaign instance: no campaign to describe. */
export const instanceConfig = (
  over: Partial<InstanceConfig> = {},
): InstanceConfig => ({
  mode: "instance",
  base_domain: "paraphe.fr",
  source_url: "",
  browser_version_url: "",
  magic_link: false,
  campaign_keys: [...CAMPAIGN_KEYS],
  ...over,
});

/** Somebody signed in. The name is a parameter because the guard that
 *  matters compares ADDRESSES: this project's thesis is that names repeat. */
export const who = (
  email: string,
  name: string,
  departments: string[] = [],
): Me => ({
  account: {
    email,
    name,
    role: "volunteer",
    team_id: null,
    active: true,
    personal_note: "",
    team_name: null,
  },
  departments,
  may_manage: false,
});
