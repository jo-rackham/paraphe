import { type Browser, expect, test } from "@playwright/test";

import {
  API_ORIGIN,
  COORDINATION,
  campaignOrigin,
  FIRST_CAMPAIGN,
  INSTANCE_ADMIN,
  SINK_ORIGIN,
} from "./config.ts";
import { signIn } from "./helpers.ts";

// Signing in by email, driven the whole way: the browser asks for a link,
// the message leaves through a real socket, and the URL it carries opens a
// session in a browser that has never seen a password.
//
// This is the one part of the feature that cannot be proved inside the
// application. Everything up to `mailer.Send` is covered by fakes in
// api/link_test.go; what happens AFTER — does a message actually go out,
// does its link actually work — is exactly what a fake cannot say, and
// exactly the way to ship a link that never arrives.

const ORIGIN = campaignOrigin(FIRST_CAMPAIGN);

interface Received {
  recipients: string[];
  headers: string;
  body: string;
}

async function inbox(): Promise<Received[]> {
  const response = await fetch(SINK_ORIGIN);
  return (await response.json()) as Received[];
}

async function emptyInbox() {
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
async function waitForMail(to: string): Promise<Received> {
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

function linkIn(mail: Received): string {
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
async function visit(browser: Browser, link: string) {
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto(link);
  return { page, context };
}

test.describe
  .serial("signing in by email", () => {
    test("a link asked for on the screen opens the session", async ({
      page,
      browser,
    }) => {
      await emptyInbox();
      await page.goto(`${ORIGIN}/`);
      await page.getByLabel("Adresse email").fill(COORDINATION.email);
      await page
        .getByRole("button", { name: "Recevoir un lien par email" })
        .click();
      // the server's own sentence, which says nothing about whether this
      // address names an account
      await expect(
        page.getByText(/Si un compte existe à cette adresse/),
      ).toBeVisible();

      const mail = await waitForMail(COORDINATION.email);
      const link = linkIn(mail);
      // it points at THIS campaign's subdomain, built from the configured
      // origin and not from the Host the browser sent
      expect(link.startsWith(`${ORIGIN}/connexion#jeton=`)).toBe(true);

      const first = await visit(browser, link);
      await expect(
        first.page.getByRole("button", { name: "déconnexion" }),
      ).toBeVisible();
      // and the token is gone from the address bar, so a reload does not
      // replay a link that is already spent — nor leave it in the history
      expect(first.page.url()).not.toContain("jeton=");
      await first.context.close();

      // second use: the same URL, now in the hands of whoever else read
      // that inbox
      const second = await visit(browser, link);
      await expect(
        second.page.getByText(/Ce lien n'est plus valable/),
      ).toBeVisible();
      await expect(
        second.page.getByRole("button", { name: "déconnexion" }),
      ).toHaveCount(0);
      await second.context.close();
    });

    test("an address nobody bears is answered the same, and nothing goes out", async ({
      page,
    }) => {
      await emptyInbox();
      await page.goto(`${ORIGIN}/`);
      await page.getByLabel("Adresse email").fill("personne@exemple.invalid");
      await page
        .getByRole("button", { name: "Recevoir un lien par email" })
        .click();
      await expect(
        page.getByText(/Si un compte existe à cette adresse/),
      ).toBeVisible();

      // the send is detached: give it every chance to have happened
      await page.waitForTimeout(1500);
      expect(await inbox()).toHaveLength(0);
    });

    test("opening an access sends its invitation, and the invitation opens it", async ({
      page,
      browser,
    }) => {
      await emptyInbox();
      const invited = "nouvelle.benevole@premiere.test";
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await page
        .getByRole("button", { name: "Mon équipe", exact: true })
        .click();

      await page.getByLabel("Nom", { exact: true }).fill("Nouvelle Bénévole");
      await page.getByLabel("Adresse email", { exact: true }).fill(invited);
      await page.getByRole("button", { name: "Créer", exact: true }).click();
      await expect(
        page.getByText(/Une invitation vient de partir/),
      ).toBeVisible();
      // the password is STILL on screen: a relay can be down tomorrow, and
      // reading it out is the path that always worked
      await expect(page.getByText(/Mot de passe provisoire/)).toBeVisible();

      const invitee = await visit(browser, linkIn(await waitForMail(invited)));
      await expect(
        invitee.page.getByRole("button", { name: "déconnexion" }),
      ).toBeVisible();
      await expect(invitee.page.getByText("Nouvelle Bénévole")).toBeVisible();
      await invitee.context.close();
    });

    // The invitation nobody drove, and it is the most special-cased of the
    // three: it is minted on the APEX, inside the transaction that creates a
    // campaign which did not exist a moment before, and its link is built
    // from a slug rather than from the Host the administrator is on. Every
    // other invitation is written by a campaign, for that campaign, from
    // inside it.
    //
    // What it must prove is the whole chain in one go — the message leaves,
    // it points at the NEW subdomain and not at the apex the approval ran
    // on, and the token opens the coordination account the approval created.
    // The journey next door signs that account in by PASSWORD, so a link
    // that never worked would leave that assertion green.
    test("approving a hosting request sends an invitation that opens the campaign", async ({
      page,
      browser,
    }) => {
      await emptyInbox();
      const slug = "tierce";
      const requester = "porteur@tierce.test";

      await page.goto(`${API_ORIGIN}/`);
      await page
        .getByRole("button", { name: "Héberger une campagne" })
        .first()
        .click();
      await page.getByLabel("Adresse souhaitée").fill(slug);
      await page.getByLabel("Nom de la campagne").fill("Campagne Tierce");
      await page.getByLabel("Votre nom").fill("Alex Tierce");
      await page.getByLabel("Votre adresse email").fill(requester);
      await page.getByRole("button", { name: "Envoyer la demande" }).click();
      await expect(
        page.getByRole("heading", { name: "Demande enregistrée" }),
      ).toBeVisible();

      await page.goto(`${API_ORIGIN}/`);
      await page.getByRole("button", { name: "Se connecter" }).click();
      await page.getByLabel("Adresse email").fill(INSTANCE_ADMIN.email);
      await page.getByLabel("Mot de passe").fill(INSTANCE_ADMIN.password);
      await page.getByRole("button", { name: "Se connecter" }).click();

      // scoped to the card: the queue holds whatever earlier journeys left,
      // and an unscoped checkbox would arm somebody else's decision
      const queued = page.locator(".carte", { hasText: slug });
      await expect(queued).toContainText(requester);
      await queued.getByLabel(/J'ai vérifié/).check();
      await queued.getByRole("button", { name: "Ouvrir la campagne" }).click();
      await expect(
        page.getByRole("heading", {
          name: `Campagne ouverte : ${slug}.localhost`,
        }),
      ).toBeVisible();

      const link = linkIn(await waitForMail(requester));
      // the NEW campaign's subdomain, prefixed to the configured apex — not
      // the apex itself, where the token belongs to no campaign and the
      // visit can only end on a sign-in screen
      expect(link.startsWith(`${campaignOrigin(slug)}/connexion#jeton=`)).toBe(
        true,
      );

      const opened = await visit(browser, link);
      await expect(
        opened.page.getByRole("button", { name: "déconnexion" }),
      ).toBeVisible();
      await expect(opened.page.getByText("Alex Tierce")).toBeVisible();
      await opened.context.close();
    });
  });
