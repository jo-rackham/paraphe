// Client of the JSON API (team mode).
//
// The same application serves both modes: if an API answers behind the
// page, we work together on a shared database; otherwise everything stays
// in the browser. The server answers the question, not a build flag — a
// version built with the wrong flag is exactly the kind of mistake only
// seen in production.

import type {
  CampaignRequest,
  Dashboard,
  Facets,
  InstanceConfig,
  MayorCard,
  MayorsPage,
  Me,
  ModerationQueue,
  Note,
  ServerConfig,
  Team,
  TeamData,
} from "./types.ts";

const ROOT = `${import.meta.env.BASE_URL}api/`;

export class APIError extends Error {
  code: number;

  constructor(code: number, message: string) {
    super(message);
    this.code = code;
  }
}

interface Request {
  method?: string;
  headers?: Record<string, string>;
  body?: unknown;
}

async function call<T>(path: string, options: Request = {}): Promise<T> {
  const headers: Record<string, string> = { ...options.headers };
  if (options.body !== undefined) headers["Content-Type"] = "application/json";
  const resp = await fetch(ROOT + path, {
    credentials: "same-origin",
    ...options,
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  });
  const type = resp.headers.get("content-type") || "";
  if (!type.includes("application/json")) {
    throw new APIError(
      resp.status,
      `Réponse inattendue du serveur (HTTP ${resp.status}). ` +
        "L'API est peut-être arrêtée ou un intermédiaire s'est intercalé.",
    );
  }
  const body = await resp.json();
  if (!resp.ok) {
    // An expired session must bring back the sign-in form. Without this
    // signal, the application looked "signed in", every screen showed an
    // endless "Chargement…", and the volunteer could not understand.
    //
    // Two calls are excluded, because they happen BEFORE any session: the
    // sign-in itself, and "who am I?" at page load. Their 401 is a
    // visitor's normal state, not an expiry — announced wrongly, it
    // greeted every volunteer with "votre session a expiré" on a browser
    // that never had one.
    if (resp.status === 401 && path !== "session" && path !== "me") {
      window.dispatchEvent(new CustomEvent(SESSION_LOST));
    }
    throw new APIError(
      resp.status,
      body.error || `Erreur HTTP ${resp.status}.`,
    );
  }
  return body as T;
}

export const SESSION_LOST = "paraphe:session-lost";

export type Mode =
  | { kind: "team"; config: ServerConfig }
  // the apex of an instance hosting several campaigns: public landing
  // page, hosting request, moderation. No campaign to serve here.
  | { kind: "instance"; config: InstanceConfig }
  | { kind: "browser" }
  | { kind: "outage"; message: string };

/** The API marks the page it serves (see api/main.go, markInterface). */
const teamMarker = (): boolean =>
  document
    .querySelector('meta[name="paraphe-mode"]')
    ?.getAttribute("content") === "team";

/** An unusable configuration is an outage, not a mode. */
function validConfig(cfg: ServerConfig): boolean {
  return (
    cfg?.mode === "team" &&
    Array.isArray(cfg.statuses) &&
    Array.isArray(cfg.ranks) &&
    typeof cfg.campaign === "object"
  );
}

/**
 * Which mode?
 *
 * When the page carries the API's marker, team mode is MANDATORY: an
 * unreachable API is an outage to display, never a reason to switch to
 * browser mode. A silent switch leaves a volunteer working in their
 * browser, on the team's origin, with nothing reaching the server — and
 * their work stays on the computer after they sign out.
 *
 * Without the marker (GitHub Pages, local file, Vite dev server), we ask:
 * a usable answer gives team mode, everything else gives browser mode.
 * This is the only situation where failure counts as an answer.
 */
export async function detectMode(): Promise<Mode> {
  const marked = teamMarker();
  try {
    const cfg = await call<ServerConfig | InstanceConfig>("config");
    if (cfg?.mode === "instance") {
      return { kind: "instance", config: cfg as InstanceConfig };
    }
    if (validConfig(cfg as ServerConfig)) {
      return { kind: "team", config: cfg as ServerConfig };
    }
    if (!marked) return { kind: "browser" };
    return {
      kind: "outage",
      message:
        "Le serveur a répondu quelque chose d'inattendu à la place de " +
        "sa configuration. Prévenez la coordination.",
    };
  } catch (e) {
    if (!marked) return { kind: "browser" };
    const detail = e instanceof APIError ? e.message : String(e);
    return {
      kind: "outage",
      message: `Le serveur de la campagne est injoignable. ${detail}`,
    };
  }
}

export const me = (): Promise<Me> => call("me");
export const signIn = (email: string, password: string): Promise<Me> =>
  call("session", { method: "POST", body: { email, password } });
export const signOut = (): Promise<unknown> =>
  call("session", { method: "DELETE" });
export const savePersonalNote = (
  personalNote: string,
): Promise<{ personal_note: string }> =>
  call("me/personal_note", {
    method: "POST",
    body: { personal_note: personalNote },
  });

export const dashboard = (): Promise<Dashboard> => call("dashboard");
export const facets = (): Promise<Facets> => call("facets");

export interface Criteria {
  q?: string;
  status?: string;
  department?: string;
  rank?: string;
  democracy?: boolean;
  after?: string;
}

export function mayors({
  q,
  status,
  department,
  rank,
  democracy,
  after,
}: Criteria = {}): Promise<MayorsPage> {
  const p = new URLSearchParams();
  if (q) p.set("q", q);
  if (status) p.set("status", status);
  if (department) p.set("department", department);
  if (rank) p.set("rank", rank);
  if (democracy) p.set("democracy", "1");
  if (after) p.set("after", after);
  return call(`mayors?${p}`);
}

export interface Card {
  mayor: MayorCard;
  notes: Note[];
}

export const card = (insee: string): Promise<Card> =>
  call(`mayors/${encodeURIComponent(insee)}`);
export const setStatus = (
  insee: string,
  status: string,
  note: string,
): Promise<Card> =>
  call(`mayors/${encodeURIComponent(insee)}/status`, {
    method: "POST",
    body: { status, note },
  });
export const takeBatch = ({
  department,
  rank,
  democracy,
}: {
  department?: string;
  rank?: string;
  democracy?: boolean;
}): Promise<{ taken: number; message: string }> =>
  call("batch", { method: "POST", body: { department, rank, democracy } });

export const team = (): Promise<TeamData> => call("team");
export const createTeam = (
  name: string,
  departments: string[],
): Promise<Team> =>
  call("team/group", { method: "POST", body: { name, departments } });

export interface NewAccount {
  email: string;
  name: string;
  role?: string;
  team_id?: number;
}

export const createAccount = (
  account: NewAccount,
): Promise<{ email: string; name: string; role: string; password: string }> =>
  call("team/account", { method: "POST", body: account });
export const toggleAccount = (
  email: string,
): Promise<{ email: string; active: boolean }> =>
  call(`team/account/${encodeURIComponent(email)}/active`, {
    method: "POST",
    body: {},
  });

export const exportUrl = () => `${ROOT}export.csv`;

// -- Campaign configuration (coordination) ----------------------------------

export const updateCampaign = (
  campaign: Record<string, string>,
  batchSize?: number,
  listed?: boolean,
): Promise<{
  campaign: Record<string, string>;
  batch_size: number;
  listed: boolean;
  unfilled: string[];
}> =>
  call("campaign", {
    method: "POST",
    body: { campaign, batch_size: batchSize, listed },
  });

// -- Instance landing page (apex) -------------------------------------------

export const requestCampaign = (
  request: CampaignRequest,
): Promise<{ id: number; slug: string; message: string }> =>
  call("request", { method: "POST", body: request });

export const moderationQueue = (): Promise<ModerationQueue> =>
  call("admin/requests");

export const decideRequest = (
  id: number,
  decision: "accepted" | "refused",
  reason = "",
): Promise<{
  id: number;
  slug: string;
  decision: string;
  address?: string;
  coordination?: string;
  password?: string;
}> =>
  call(`admin/requests/${id}`, { method: "POST", body: { decision, reason } });

export const publicCampaigns = (): Promise<{
  campaigns: { slug: string; name: string }[];
  base_domain: string;
}> => call("campaigns");

export const createCampaign = (creation: {
  slug: string;
  name: string;
  coordination_email: string;
  coordination_name: string;
  listed?: boolean;
}): Promise<{
  organisation: number;
  slug: string;
  address: string;
  coordination: string;
  password: string;
}> => call("admin/campaigns", { method: "POST", body: creation });
