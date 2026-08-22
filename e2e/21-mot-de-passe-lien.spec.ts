import { expect, test } from "@playwright/test";

import { COORDINATION, campaignOrigin, FIRST_CAMPAIGN } from "./config.ts";
import {
  emptyInbox,
  linkIn,
  openManagement,
  signIn,
  visit,
  waitForMail,
} from "./helpers.ts";

// « Mot de passe oublié » has to END somewhere: the link opens the session,
// and the session the link opened sets a new password WITHOUT the old one —
// which its holder, by definition, does not have. Before this journey, the
// flow ended on a form demanding the forgotten password, and a lone
// coordination had no way back at all.

// This file's own SOURCE, believed because the harness declares the loopback
// a trusted proxy the way production declares its ingress (global-setup).
// The suite's shared source is near its sign-in ceiling, and this journey
// opens three sessions.
const SOURCE = { "X-Forwarded-For": "192.0.2.21" };
test.use({ extraHTTPHeaders: SOURCE });

const ORIGIN = campaignOrigin(FIRST_CAMPAIGN);

test.describe
  .serial("a forgotten password ends at a chosen one", () => {
    const WHO = { email: "lea.lien@premiere.test", name: "Léa Lien" };
    const CHOSEN = "prairie-fenetre-ruban-77";

    test("the coordination opens the access this journey forgets", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openManagement(page);
      await page.getByLabel("Nom", { exact: true }).fill(WHO.name);
      await page.getByLabel("Adresse email", { exact: true }).fill(WHO.email);
      await page.getByRole("button", { name: "Créer", exact: true }).click();
      // the provisional password shows and is deliberately NOT noted: what
      // follows is the journey of somebody who lost it
      await expect(page.getByText(/Mot de passe provisoire/)).toBeVisible();
    });

    test("a link session chooses a new password, without the old one", async ({
      page,
      browser,
    }) => {
      await emptyInbox();
      await page.goto(`${ORIGIN}/`);
      await page.getByLabel("Adresse email").fill(WHO.email);
      await page
        .getByRole("button", { name: "Recevoir un lien par email" })
        .click();
      await expect(
        page.getByText(/Si un compte existe à cette adresse/),
      ).toBeVisible();

      const opened = await visit(
        browser,
        linkIn(await waitForMail(WHO.email)),
        SOURCE,
      );
      const p = opened.page;
      // the door is announced where the reader lands: without this sentence
      // the waiver exists and nobody finds it
      await expect(p.getByText(/sans avoir à donner l'ancien/)).toBeVisible();

      await p.getByRole("link", { name: "Mon profil" }).click();
      await expect(
        p.getByText(/l'ancien mot de passe n'est pas demandé/),
      ).toBeVisible();
      await expect(p.getByLabel("Mot de passe actuel")).toHaveCount(0);
      await p.getByLabel("Nouveau mot de passe", { exact: true }).fill(CHOSEN);
      await p.getByLabel("Répétez le nouveau mot de passe").fill(CHOSEN);
      await p.getByRole("button", { name: "Changer mon mot de passe" }).click();
      await expect(p.getByText(/Votre mot de passe est changé/)).toBeVisible();
      // the session was re-minted at the password door: the form asks for
      // the current one again, and STAYS usable — a stale via_link here
      // would send an empty current the server now answers 403
      await expect(p.getByLabel("Mot de passe actuel")).toBeVisible();
      await opened.context.close();

      // the chosen password opens the account, through the ordinary door
      await signIn(page, ORIGIN, WHO.email, CHOSEN);
    });
  });
