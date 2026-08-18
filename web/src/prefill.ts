// Pre-filling a campaign from ?org=<slug>, in browser mode only.
//
// The problem it solves is small and real: without it, every volunteer of a
// hosted campaign retypes the same nine fields, and a typo in any of them
// goes out to mayors under the campaign's name.
//
// The reason it is a SLUG and not a URL is the whole design. A link is free
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

export const instanceDomain = (): string =>
  markedInstance() || (import.meta.env.PARAPHE_INSTANCE_DOMAIN ?? "").trim();

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
  const baked = (import.meta.env.PARAPHE_INSTANCE_DOMAIN ?? "").trim();
  return `https://${slug}.${baked}/api/campaign/public`;
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
  // The logo is optional and its absence changes nothing. Checked all the
  // same: `logo.url` is put into an <img>, and a captive portal answering
  // this route can write whatever it likes there. Only an absolute http(s)
  // URL is kept — a `javascript:` or `data:` value would be a string this
  // campaign chose and this browser executed.
  let logo = "";
  const offered = body?.logo?.url;
  if (typeof offered === "string" && offered !== "") {
    if (/^https?:\/\//i.test(offered)) {
      logo = offered;
    }
  }
  return { slug, name: String(body.name ?? slug), campaign, logo };
}
