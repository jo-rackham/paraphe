// Pre-filling a campaign from ?org=<slug>, in browser mode only.
//
// The problem it solves is small and real: without it, every volunteer of a
// hosted campaign retypes the same nine fields, and a typo in any of them
// goes out to mayors under the campaign's name.
//
// The reason it is a SLUG and not a URL is the whole design. A link is free
// to name a campaign; it is never free to name a host — the instance domain
// is baked at build time. So the data can only come from a campaign that
// was requested, moderated and approved on that instance, and a forged
// link cannot slip an attacker's contact details into messages sent to
// elected officials. A parameter carrying the configuration itself, or a
// URL to fetch it from, would have exactly that hole.

import { CAMPAIGN_KEYS, unfilledKeys } from "../../noyau/messages.ts";
import type { Campaign } from "./types.ts";

export interface Offer {
  slug: string;
  name: string;
  campaign: Campaign;
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

export const instanceDomain = (): string =>
  (import.meta.env.PARAPHE_INSTANCE_DOMAIN ?? "").trim();

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
  const url = `https://${slug}.${instanceDomain()}/api/campaign/public`;
  // `redirect: "error"`: the default follows, and CORS is then evaluated on
  // the FINAL response — a redirect would take the answer off the baked host.
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
  const body = await response.json();
  // Answered, but with what? A captive portal returns 200 and HTML, and a
  // half-filled object is worse than none: eight blank rows shown as a
  // campaign, or a value that is not a string, which replaced the whole
  // screen with the error boundary.
  const campaign = body?.campaign;
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
  return { slug, name: String(body.name ?? slug), campaign };
}
