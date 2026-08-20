// Types shared by both modes.
//
// A mayor card comes either from a CSV or from the API: in both cases its
// columns are text, and many are empty. Hence `Mayor` = a dictionary of
// strings rather than a rigid structure — the CSV gains columns (a new
// signal) without the type having to change.

import type { Mayor, Templates } from "../../noyau/messages.ts";

export type { Campaign, Mayor, Templates } from "../../noyau/messages.ts";

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
  /**
   * The TEAM that last wrote the status — never the person. A status is read
   * by every team of the campaign, and that is what keeps two of them off the
   * same mayor; a name of another team's is not.
   *
   * An identifier as TEXT, like every other column of a card: `"0"` is the
   * national scope, a real answer held by the accounts that carry no team,
   * and it has no row in `teams` hence no name. Absent means a card statused
   * before the column existed. The two are different answers and the screen
   * says different things about them.
   */
  updated_by_team?: string | null;
  updated_by_team_name?: string | null;
  /**
   * The team currently WORKING the card, beside the one that last wrote a
   * status. Informative and nothing else: no card of a campaign is refused
   * to a team of it, and this is what tells a volunteer somebody is already
   * there instead of the card not being in the list.
   *
   * It is also what replaces the person when the card is not this team's:
   * `volunteer` and `volunteer_name` come back null in that case, because a
   * name never crosses a team where a team name does.
   *
   * TWO fields, for the reason `updated_by_team` is two: `team_name` is null
   * for the NATIONAL scope, which is a real scope with no row in `teams`, so
   * a screen reading the name alone showed a card the coordination had taken
   * as free. `taken_by` is the answer, as TEXT — null is nobody, `"0"` is the
   * national scope, a number is the team `team_name` names.
   */
  taken_by?: string | null;
  team_name?: string | null;
}

export interface Note {
  volunteer: string | null;
  status: string;
  note: string;
  ts: string;
  /**
   * The row's own identifier, in team mode — what the edit and delete routes
   * need to name one. Browser mode has none: a note there is named by its
   * position in the record that holds it.
   */
  id?: number;
  /**
   * Whether the reader WROTE this line. A boolean and never the address it is
   * computed from: what crosses between colleagues of one campaign is the
   * card, never the person. It is what puts « Modifier » on a line and takes
   * it off the next — the server is what refuses.
   */
  mine?: boolean;
  /** When its author last corrected the words; absent = never. */
  edited_at?: string | null;
}

export interface Account {
  email: string;
  name: string;
  role: "coordination" | "lead" | "volunteer";
  team_id: number | null;
  active: boolean;
  personal_note: string;
  team_name: string | null;
  /**
   * This volunteer's own answer to « do I telephone the mayors I write to »,
   * or null for « whatever the campaign does » — which is what somebody who
   * never opened the setting means, and it keeps tracking the campaign
   * rather than freezing at its value the day the account was made.
   */
  phone_outreach?: boolean | null;
}

export interface Me {
  account: Account;
  departments: string[];
  may_manage: boolean;
  /**
   * The message templates that apply to THIS account, in two layers over the
   * ones the image carries: its campaign's, then its team's.
   *
   * Two layers and not one resolved set, because the screen that edits them
   * has to tell them apart — « revenir au texte de la campagne » is a button
   * nobody can aim at a merged text. `mergeTemplates` is what resolves them,
   * and it lives in noyau/ beside the engine, so this end and the mass
   * mailing resolve them the same way.
   *
   * Optional: an API one release behind carries none, and a campaign that has
   * written no template carries two empty objects.
   */
  templates?: { campaign: Templates; team: Templates };
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
  /** Departments of the common mayor list: the perimeter a team can ask for. */
  departments: string[];
  /** L'instance sait envoyer un email : la connexion par lien est offerte. */
  magic_link: boolean;
  /**
   * The account-less browser version, carrying THIS campaign (`?org=<slug>`)
   * when the instance has a subdomain space for the pre-fill to resolve in.
   * Empty: this instance offers none, and no link is shown.
   */
  browser_version_url: string;
  /** Present when the instance hosts several campaigns. */
  organisation?: { slug: string; name: string; listed: boolean };
  /**
   * Whether the campaign telephones the mayors it writes to — its DEFAULT.
   * A volunteer who has answered for themselves (Account.phone_outreach)
   * overrides it; one who has not follows it as it changes.
   */
  phone_outreach?: boolean;
}

/** The apex of a multi-campaign instance: no campaign to describe. */
export interface InstanceConfig {
  mode: "instance";
  base_domain: string;
  source_url: string;
  // URL publique de la version navigateur (sans compte) — vide : pas de lien
  browser_version_url: string;
  campaign_keys: string[];
  /** L'instance sait envoyer un email : la connexion par lien est offerte. */
  magic_link: boolean;
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

/** A request to open a local team, as its campaign's coordination reads it. */
export interface TeamRequest {
  id: number;
  name: string;
  /** `;`-joined, empty = the whole country */
  departments: string;
  requester_email: string;
  requester_name: string;
  message: string;
  state: "pending" | "accepted" | "refused";
  reason: string;
  ts: string;
  decided_at: string;
  decided_by: string;
}

export interface TeamData {
  accounts: TeamAccount[];
  teams: Team[];
  departments: string[];
  /** Empty for a lead: the moderation queue is the coordination's. */
  requests: TeamRequest[];
}

/** Local tracking (browser mode): one record per mayor. */
export interface Tracking {
  insee_code: string;
  status: string;
  notes: { ts: string; status: string; note: string; edited_at?: string }[];
  updated_at: string;
}
