// Types shared by both modes.
//
// A mayor card comes either from a CSV or from the API: in both cases its
// columns are text, and many are empty. Hence `Mayor` = a dictionary of
// strings rather than a rigid structure — the CSV gains columns (a new
// signal) without the type having to change.

import type { Mayor } from "../../noyau/messages.ts";

export type { Campaign, Mayor } from "../../noyau/messages.ts";

/** A campaign's logo, as the API describes it. Null: the campaign has none. */
export interface Logo {
  url: string;
  type: string;
}

/**
 * Card as the API returns it: the CSV columns, plus the work state.
 * `team_id` stays out of the type: it is the row's only non-textual field,
 * and the interface has no use for it — the wall is enforced server-side,
 * never in the browser.
 */
export interface MayorCard extends Mayor {
  insee_code: string;
  volunteer?: string | null;
  volunteer_name?: string | null;
  status?: string | null;
}

export interface Note {
  volunteer: string | null;
  status: string;
  note: string;
  ts: string;
}

export interface Account {
  email: string;
  name: string;
  role: "coordination" | "lead" | "volunteer";
  team_id: number | null;
  active: boolean;
  personal_note: string;
  team_name: string | null;
}

export interface Me {
  account: Account;
  departments: string[];
  may_manage: boolean;
}

export interface Status {
  key: string;
  label: string;
  colour: string;
}
export interface Rank {
  key: string;
  label: string;
}

export interface ServerConfig {
  mode: "team";
  campaign: Record<string, string>;
  /**
   * The campaign's logo, or null. The URL is ABSOLUTE and built by the API:
   * it points at the object store's own origin, which the page cannot
   * derive — and which the Content-Security-Policy names explicitly.
   */
  logo: Logo | null;
  batch_size: number;
  unfilled: string[];
  source_url: string;
  statuses: Status[];
  ranks: Rank[];
  /** Present when the instance hosts several campaigns. */
  organisation?: { slug: string; name: string; listed: boolean };
}

/** The apex of a multi-campaign instance: no campaign to describe. */
export interface InstanceConfig {
  mode: "instance";
  base_domain: string;
  source_url: string;
  // URL publique de la version navigateur (sans compte) — vide : pas de lien
  browser_version_url: string;
  campaign_keys: string[];
}

export interface CampaignRequest {
  slug: string;
  name: string;
  requester_email: string;
  requester_name: string;
  message: string;
  campaign?: Record<string, string>;
  /** absent = référencée : l'annuaire est le défaut, la discrétion le choix */
  listed?: boolean;
}

export interface QueuedRequest {
  id: number;
  slug: string;
  name: string;
  requester_email: string;
  requester_name: string;
  message: string;
  state: "pending" | "accepted" | "refused";
  listed: boolean;
  reason: string;
  ts: string;
  decided_at: string;
  decided_by: string;
}

export interface HostedOrganisation {
  id: number;
  slug: string;
  name: string;
  state: string;
  created_at: string;
}

export interface ModerationQueue {
  requests: QueuedRequest[];
  organisations: HostedOrganisation[];
  base_domain: string;
}

export interface Message {
  tone: "ok" | "erreur";
  text: string;
}

export interface Counter {
  key: string;
  n: number;
}

export interface Dashboard {
  stats: Record<string, number>;
  total: number;
  departments_with_promise: Counter[];
  departments_covered: number;
  mine: MayorCard[];
  team: { who: string; n: number; done: number }[];
  departments: string[];
  by_rank: Record<string, number>;
  batch_size: number;
}

export interface Facets {
  departments: string[];
  by_rank: Record<string, number>;
}

export interface MayorsPage {
  total: number;
  rows: MayorCard[];
  /** Opaque cursor on the last row served: a position, not an index. */
  next: string | null;
  rank: string;
}

export interface TeamAccount extends Account {
  created_at: string;
  created_by: string;
  team: string | null;
}

export interface Team {
  id: number;
  name: string;
  departments: string;
  created_at: string;
  members: number;
  reserved: number;
}

export interface TeamData {
  accounts: TeamAccount[];
  teams: Team[];
  departments: string[];
}

/** Local tracking (browser mode): one record per mayor. */
export interface Tracking {
  insee_code: string;
  status: string;
  notes: { ts: string; status: string; note: string }[];
  updated_at: string;
}
