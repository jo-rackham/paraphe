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
  Logo,
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
    // The calls that OPEN a session are excluded, because they happen before
    // there is one. Their 401 is a visitor's normal state, not an expiry —
    // announced wrongly, it greeted every volunteer with "votre session a
    // expiré" on a browser that never had one, and it drowned out the one
    // sentence a spent sign-in link has to deliver.
    if (resp.status === 401 && !BEFORE_ANY_SESSION.has(path)) {
      window.dispatchEvent(new CustomEvent(SESSION_LOST));
    }
    throw new APIError(
      resp.status,
      body.error || `Erreur HTTP ${resp.status}.`,
    );
  }
  return body as T;
}

/** The calls a visitor makes before holding a session: signing in, asking
 *  for a link, redeeming one, and "who am I?" at page load. */
const BEFORE_ANY_SESSION = new Set([
  "session",
  "session/link",
  "session/link/redeem",
  "me",
]);

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

/**
 * Ask for a sign-in link. The answer is the SAME whether or not an account
 * bears this address — displaying it as-is is what keeps this screen from
 * becoming a roster of the campaign's volunteers.
 */
export const requestLink = (email: string): Promise<{ message: string }> =>
  call("session/link", { method: "POST", body: { email } });

/**
 * Exchange a link's token for a session. A POST, and the token in the BODY:
 * it arrived in the URL's fragment, which no server ever sees, and putting
 * it back in a path would write it into every access log on the way.
 */
export const redeemLink = (token: string): Promise<Me> =>
  call("session/link/redeem", { method: "POST", body: { token } });

/**
 * The token a sign-in link left in the address bar, taken and ERASED.
 *
 * Called ONCE, at boot (main.tsx), before anything decides which mode to
 * render: erasing it inside the screen that consumes it left it in the URL
 * on every path that never reaches that screen.
 *
 * Erased whenever the key is there, whatever its value, and a reload must
 * not replay a token that opens exactly one session. What ELSE the fragment
 * carried is put back: this application uses no other fragment parameter
 * today, and eating one silently is how the next one would be lost.
 *
 * Every path sets `pending`, the one that finds nothing included: leaving it
 * as it was makes a second call hand back the token of the first.
 */
export function takeLinkToken(): string | null {
  pending = null;
  const params = new URLSearchParams(window.location.hash.replace(/^#/, ""));
  if (!params.has("jeton")) return null;
  const token = params.get("jeton");
  params.delete("jeton");
  const url = new URL(window.location.href);
  url.hash = params.toString();
  try {
    window.history.replaceState({}, "", url);
  } catch {
    // A history that refuses does not take the page down with it — the same
    // line the theme's storage holds. The token is handed over anyway: it is
    // about to be spent, so the choice is between one spent token left in an
    // address bar and a blank page with a LIVE one still in it.
  }
  pending = token || null;
  takenAt = Date.now();
  return pending;
}

/**
 * The token, handed over EXACTLY ONCE.
 *
 * It is held here rather than in a prop, and that is the whole point: a prop
 * lives as long as the tab. Somebody who lands on the outage screen with a
 * link, walks away, and leaves the tab open hands their session to whoever
 * presses « Réessayer » next — Team mounts afresh with the same prop and
 * redeems it. React's StrictMode does the same thing twice in development,
 * for the same reason.
 *
 * Once consumed it is gone, so a second mount asks the server for nothing
 * and the link has to be clicked again.
 *
 * And it is good for THIS VISIT, which is what the age bound says. Dropping
 * it per screen means naming every screen that cannot use it, and the list
 * was short by one: the LOADING screen, where the mode is not known yet, is
 * not a screen that cannot use the token — it is a screen that does not know
 * — so it was not on the list, and a page that hangs there holds a live
 * token for as long as it hangs. Somebody walks away, somebody else sits
 * down, the campaign answers at last, and the session opens for the second
 * person.
 *
 * Two minutes, which no load reaches: one that slow has already failed to
 * the outage screen. The cost of being wrong is one click on a link that is
 * still in its owner's inbox.
 */
let pending: string | null = null;
let takenAt = 0;

const VISIT = 2 * 60 * 1000;

export function consumeLinkToken(): string | null {
  const token = pending;
  pending = null;
  if (token === null || Date.now() - takenAt > VISIT) return null;
  return token;
}

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
): Promise<{
  email: string;
  name: string;
  role: string;
  password: string;
  /** L'invitation est partie (envoi synchrone : le résultat est connu). */
  invitation_sent: boolean;
  invitation_error?: string;
}> => call("team/account", { method: "POST", body: account });
export const toggleAccount = (
  email: string,
): Promise<{ email: string; active: boolean }> =>
  call(`team/account/${encodeURIComponent(email)}/active`, {
    method: "POST",
    body: {},
  });

/** Coordination only: moves an account to another campaign role. Promoting
 * to coordination unbinds the team server-side. */
export const changeRole = (
  email: string,
  role: string,
): Promise<{ email: string; role: string }> =>
  call(`team/account/${encodeURIComponent(email)}/role`, {
    method: "POST",
    body: { role },
  });

// -- Asking a campaign to open a local team ---------------------------------

/** Public: no session, and it creates nothing until the coordination accepts. */
export const requestTeam = (request: {
  name: string;
  departments: string[];
  requester_name: string;
  requester_email: string;
  message: string;
}): Promise<{ id: number; name: string; message: string }> =>
  call("team/request", { method: "POST", body: request });

/**
 * Coordination only. `name` and `departments` are what it actually opens:
 * the person who filled the form knows their department, not the campaign's
 * map, and correcting a perimeter must not mean refusing the request.
 */
export const decideTeamRequest = (
  id: number,
  decision: "accepted" | "refused",
  {
    reason = "",
    name,
    departments,
  }: { reason?: string; name?: string; departments?: string[] } = {},
): Promise<{
  id: number;
  decision: string;
  team?: number;
  name?: string;
  departments?: string[];
  lead?: string;
  password?: string;
}> =>
  call(`team/requests/${id}`, {
    method: "POST",
    body: { decision, reason, name, departments },
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

// The logo is its own call: an image does not fit in what a campaign body
// leaves under the request ceiling, and replacing one should not require
// resaving nine fields.
export const uploadLogo = (dataUri: string): Promise<{ logo: Logo }> =>
  call("campaign/logo", { method: "POST", body: { data_uri: dataUri } });

export const removeLogo = (): Promise<{ logo: null }> =>
  call("campaign/logo", { method: "DELETE" });

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
  invitation_sent?: boolean;
  invitation_error?: string;
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
  invitation_sent: boolean;
  invitation_error?: string;
}> => call("admin/campaigns", { method: "POST", body: creation });
