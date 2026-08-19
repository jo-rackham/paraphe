// Pre-filling a campaign, in browser mode only. TWO doors, and they differ
// in what names the campaign — which is what decides whether it is applied
// or offered.
//
// The ORIGIN that served this page (`ownCampaign`) is the default: served by
// `<slug>.<instance>/navigateur/`, this build is that campaign's own browser
// version, and there is nothing to confirm about a campaign whose own server
// wrote the page saying which one it is.
//
// A `?org=<slug>` LINK (`requestedSlug`, `fetchCampaign`) may name any
// campaign of the instance, so it is shown before it is applied.
//
// The problem both solve is small and real: without it, every volunteer of a
// hosted campaign retypes the same nine fields, and a typo in any of them
// goes out to mayors under the campaign's name.
//
// The reason a link carries a SLUG and not a URL is the whole design of that
// second door. A link is free
// to name a campaign; it is never free to name a host — the instance domain
// comes from the DOCUMENT, never from the URL. So the data can only come
// from a campaign that was requested, moderated and approved on that
// instance, and a forged link cannot slip an attacker's contact details into
// messages sent to elected officials. A parameter carrying the configuration
// itself, or a URL to fetch it from, would have exactly that hole.

import { CAMPAIGN_KEYS, unfilledKeys } from "../../noyau/messages.ts";
import { LOGO_MAX_BYTES } from "./common.tsx";
import type { Campaign } from "./types.ts";

export interface Offer {
  slug: string;
  name: string;
  campaign: Campaign;
  /** the campaign's logo, as an absolute URL on the instance's media origin */
  logo: string;
}

/**
 * Downloads a logo and returns it as a data URI.
 *
 * This is the ONLY moment this mode fetches an image, and it happens
 * because the volunteer pressed "reprendre cette campagne". What is stored
 * afterwards is the bytes, never the address: keeping the URL would put a
 * call to the instance in every single page load, and the promise this
 * version makes — nothing leaves your browser, check the network tab — has
 * to survive being checked.
 *
 * Bounded like the upload itself: a media origin that answers with a
 * hundred megabytes must not fill this browser's storage.
 */
export async function inlineLogo(url: string): Promise<string> {
  const response = await fetch(url, { credentials: "omit", mode: "cors" });
  if (!response.ok) {
    throw new Error(`Le logo n'a pas répondu (HTTP ${response.status}).`);
  }
  const blob = await response.blob();
  if (blob.size > LOGO_MAX_BYTES) {
    throw new Error(
      `Le logo pèse ${Math.round(blob.size / 1024)} Ko, la limite est de ` +
        `${LOGO_MAX_BYTES / 1024} Ko.`,
    );
  }
  if (!blob.type.startsWith("image/")) {
    throw new Error(`Ce n'est pas une image (${blob.type || "type absent"}).`);
  }
  return await new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(new Error("Logo illisible."));
    reader.readAsDataURL(blob);
  });
}

/**
 * Same rule as the API's ValidSlug: it is a DNS label, and anything else
 * would build a hostname nobody can reach.
 */
export function validSlug(slug: string): boolean {
  return /^[a-z0-9][a-z0-9-]{0,61}[a-z0-9]$/.test(slug);
}

/**
 * The offer is for a campaign NOBODY has typed into — not merely one that
 * is incomplete. A volunteer who filled eight fields of nine is offered a
 * link that replaced all nine, and `signataire` is the signature at the
 * bottom of every email to a mayor.
 *
 * Exported and used by the screen, so a test of it is a test of what runs.
 */
export const untouchedCampaign = (cfg: Campaign): boolean =>
  unfilledKeys(cfg).length === CAMPAIGN_KEYS.length;

/**
 * The instance a `?org=` may name a campaign of.
 *
 * TWO sources, in this order, and the order is the whole point.
 *
 * The MARKER is injected in memory at startup by the server serving this
 * page (api/pages.go, markBrowserVersion) — the same mechanism, and the same
 * trust, as the mode marker one screen over. It exists because this build is
 * also served BY an instance, under /navigateur/, from an image that carries
 * no domain: baking one would send every other operator's volunteers to
 * fetch campaigns from ours.
 *
 * The BAKED value is the static publication's, where there is no server to
 * inject anything (GitHub Pages, a local file).
 *
 * Neither changes what a LINK may say: the marker comes from the document,
 * the parameter still carries a slug, and `validSlug` refuses anything that
 * could name a host. Empty either way and `?org=` does nothing at all.
 */
const markedInstance = (): string =>
  document
    .querySelector('meta[name="paraphe-instance"]')
    ?.getAttribute("content")
    ?.trim() ?? "";

const bakedInstance = (): string =>
  (import.meta.env.PARAPHE_INSTANCE_DOMAIN ?? "").trim();

export const instanceDomain = (): string => markedInstance() || bakedInstance();

/**
 * Where a campaign of this instance publishes itself.
 *
 * The HOST is `<slug>.<instance>` and nothing else — that is the whole
 * security of the pre-fill. The SCHEME and the PORT are a different
 * question, and the answer depends on which of the two sources named the
 * instance.
 *
 * MARKED: the instance served this very page, so it is reachable exactly as
 * this page was reached. Hardcoding `https` and no port sent an instance
 * running on any other — a local trial on :8047, this project's own
 * end-to-end suite on :8399 — to a host nothing answers at, and the offer
 * failed with « Failed to fetch ».
 *
 * BAKED: a static publication, which knows nothing about where the instance
 * listens. `https`, no port: a public instance carrying campaign data over
 * anything else is not a fallback worth writing.
 */
function publicCampaignUrl(slug: string): string {
  const marked = markedInstance();
  if (marked) {
    const port = window.location.port ? `:${window.location.port}` : "";
    return `${window.location.protocol}//${slug}.${marked}${port}/api/campaign/public`;
  }
  return `https://${slug}.${bakedInstance()}/api/campaign/public`;
}

/**
 * The campaign THIS ORIGIN IS, when an instance is the one serving this
 * build — and it is the default, not an offer.
 *
 * A `?org=` link may name a campaign on another instance, so it is shown
 * before it is applied: a forged one would otherwise put somebody else's
 * contact details under a real candidate's name. NONE of that applies here.
 * The campaign is not named by a link, it is the origin that served the
 * page: whoever could lie about it already wrote the page saying it. So
 * there is nothing to confirm, and confirming cost two clicks and read, to
 * the volunteer who did not make them, as a tool that had failed to fill
 * anything in.
 *
 * ROOT-RELATIVE on purpose. Every other call this build makes goes through
 * Vite's base — `/navigateur/api/…` — which the server answers with HTML
 * deliberately, because that is what keeps this build in browser mode. The
 * real API is at the root of the same origin, and asking it is what tells a
 * campaign's own version from a static publication: on GitHub Pages this
 * path answers HTML too, and the shape check below refuses it.
 *
 * It is the one request this mode makes before a click, and it leaves
 * nothing: it goes to the host that just served this page, carries no
 * credential and no identifier, and says nothing that loading the page did
 * not already say.
 *
 * Null for every "there is no campaign here" — the apex, a static host, an
 * unconfigured campaign (409), a captive portal. Absence is the normal
 * answer, not a failure to report.
 */
export async function ownCampaign(): Promise<Offer | null> {
  try {
    const response = await fetch("/api/campaign/public", {
      credentials: "omit",
      redirect: "error",
    });
    if (!response.ok) return null;
    return readOffer(await response.json());
  } catch {
    return null;
  }
}

/** The slug asked for in the address bar, or null. */
export function requestedSlug(search: string): string | null {
  const slug = new URLSearchParams(search).get("org");
  if (!slug || !instanceDomain() || !validSlug(slug)) return null;
  return slug;
}

/**
 * Fetches the campaign a slug designates. Never `call()` from api.ts: that
 * one is same-origin and prefixed by the published base — this is the one
 * request the browser version makes to somewhere else, and it carries no
 * credentials.
 */
export async function fetchCampaign(slug: string): Promise<Offer> {
  // Checked HERE, where the host is built, not only in `requestedSlug` where
  // it happens to be checked today. This label is interpolated into a
  // hostname: a slug carrying a `@`, a `/` or a dot names a different
  // machine, and « a link may name a campaign, never a host » is a promise
  // one new call site would otherwise quietly withdraw.
  if (!validSlug(slug)) {
    throw new Error(`« ${slug} » ne nomme pas une campagne.`);
  }
  const url = publicCampaignUrl(slug);
  // `redirect: "error"`: the default follows, and CORS is then evaluated on
  // the FINAL response — a redirect would take the answer off the named host.
  const response = await fetch(url, {
    credentials: "omit",
    mode: "cors",
    redirect: "error",
  });
  if (!response.ok) {
    const detail = await response.json().catch(() => null);
    throw new Error(
      detail?.error ??
        `La campagne « ${slug} » n'a pas répondu (HTTP ${response.status}).`,
    );
  }
  return readOffer(await response.json(), slug);
}

/**
 * What an answer has to look like before nine of its values go out in every
 * message to a mayor. ONE reader for the two doors — the campaign a link
 * names, and the campaign this origin is — because the second copy is where
 * they would stop agreeing on what counts as a campaign.
 *
 * It THROWS rather than returning null: the link door owes its reader a
 * sentence, and the origin door treats every refusal as « there is no
 * campaign here », which is its normal state.
 */
export function readOffer(body: unknown, slug?: string): Offer {
  // Answered, but with what? A captive portal returns 200 and HTML, and a
  // half-filled object is worse than none: eight blank rows shown as a
  // campaign, or a value that is not a string, which replaced the whole
  // screen with the error boundary.
  const answer = body as {
    slug?: unknown;
    name?: unknown;
    campaign?: Record<string, string>;
    logo?: { url?: unknown };
  } | null;
  const campaign = answer?.campaign;
  const complete =
    campaign &&
    typeof campaign === "object" &&
    CAMPAIGN_KEYS.every(
      (k) => typeof campaign[k] === "string" && campaign[k].trim(),
    );
  if (!complete) {
    throw new Error(
      "La réponse ne ressemble pas à une campagne complète. Un intermédiaire " +
        "s'est peut-être intercalé — ne l'utilisez pas.",
    );
  }
  // The name this campaign is KNOWN by, and the slug it answers at. Asked
  // through a link the slug is the one asked for, never the one the answer
  // claims: it is the label that built the host, and taking the body's
  // instead would let an answer rename itself.
  const named = typeof answer?.slug === "string" ? answer.slug : "";
  const which = slug ?? named;
  if (!validSlug(which)) {
    throw new Error("Cette réponse ne nomme aucune campagne.");
  }
  // The logo is optional and its absence changes nothing. Checked all the
  // same: `logo.url` is put into an <img>, and whatever answers this route
  // can write anything there. Only an absolute http(s) URL is kept — a
  // `javascript:` or `data:` value would be a string this campaign chose
  // and this browser executed.
  let logo = "";
  const offered = answer?.logo?.url;
  if (typeof offered === "string" && /^https?:\/\//i.test(offered)) {
    logo = offered;
  }
  return {
    slug: which,
    name: String(answer?.name ?? which),
    campaign,
    logo,
  };
}
