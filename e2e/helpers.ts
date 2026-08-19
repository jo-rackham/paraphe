import { type Browser, expect, type Page } from "@playwright/test";

import { SINK_ORIGIN } from "./config.ts";

/** Signs in and waits for the application to be past the login form. */
export async function signIn(
  page: Page,
  origin: string,
  email: string,
  password: string,
) {
  await page.goto(`${origin}/`);
  await page.getByLabel("Adresse email").fill(email);
  await page.getByLabel("Mot de passe").fill(password);
  await page.getByRole("button", { name: "Se connecter" }).click();
  await expect(page.getByRole("button", { name: "déconnexion" })).toBeVisible();
}

/** Moves to one of the campaign tabs. */
export async function openTab(page: Page, name: string) {
  await page.getByRole("button", { name, exact: true }).click();
}

/**
 * The management screen, whose tab is NAMED FOR THE ROLE that opens it:
 * « Ma campagne » for a coordination, which has no team and holds the
 * campaign, « Mon équipe » for a référent, who leads one.
 *
 * Either name, because a journey about logos or invitations is not about
 * what the tab is called. The names themselves are pinned where they belong
 * — `web/src/Team.test.tsx`, which drives both roles — and by the heading
 * assertions in the accessibility sweep.
 */
export async function openManagement(page: Page) {
  await page
    .getByRole("button", { name: /^(Ma campagne|Mon équipe)$/ })
    .click();
}

// The throwaway relay, read back. Shared because two journeys open a link
// that arrived by email — one asked for from a screen, one minted by an
// approval on the apex — and the public forms behind them are plafonnés per
// source: a spec that files its own request just to read its own inbox
// spends a ceiling the suite needs elsewhere.

export interface Received {
  recipients: string[];
  headers: string;
  body: string;
}

export async function inbox(): Promise<Received[]> {
  const response = await fetch(SINK_ORIGIN);
  return (await response.json()) as Received[];
}

export async function emptyInbox() {
  await fetch(SINK_ORIGIN, { method: "DELETE" });
}

/** Waits for a message to reach the sink: the send is detached from the
 *  request that asked for it, on purpose.
 *
 *  The LAST match, not the first. A send is detached, so one from an earlier
 *  journey can land after the inbox was emptied and before this one leaves —
 *  and its token is still live for fifteen minutes, so redeeming it would
 *  succeed and the journey would pass without the send it is testing ever
 *  having happened. */
export async function waitForMail(to: string): Promise<Received> {
  const deadline = Date.now() + 15_000;
  for (;;) {
    const found = (await inbox()).findLast((m) => m.recipients.includes(to));
    if (found) return found;
    if (Date.now() > deadline) {
      throw new Error(`no message reached ${to}`);
    }
    await new Promise((r) => setTimeout(r, 200));
  }
}

export function linkIn(mail: Received): string {
  const url = /(http:\/\/\S+#jeton=\S+)/.exec(mail.body);
  if (!url) throw new Error(`no sign-in link in:\n${mail.body}`);
  return url[1];
}

/**
 * Opens a link in a browser that has never seen this campaign — which is
 * what a recipient's browser is, and what makes the visit a real document
 * load. Reusing the page would be a FRAGMENT navigation from the URL the
 * first visit scrubbed: same document, no reload, nothing re-read.
 */
export async function visit(browser: Browser, link: string) {
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto(link);
  return { page, context };
}
