import { expect, test } from "@playwright/test";

import {
  API_ORIGIN,
  campaignOrigin,
  FIRST_CAMPAIGN,
  INSTANCE_ADMIN,
} from "./config.ts";
import {
  linkIn,
  openManagement,
  openTab,
  signIn,
  visit,
  waitForMail,
} from "./helpers.ts";

// One journey, told in order: a stranger asks for a campaign, an instance
// administrator decides, and only then does the campaign exist.
//
// The steps depend on each other, hence `serial`: a request cannot be
// moderated before it is filed, and running them in parallel would have two
// administrators open the same campaign twice.

test.describe
  .serial("hosting a campaign", () => {
    const SLUG = "seconde";
    const REQUESTER = "porteur@seconde.test";
    let coordinationPassword = "";

    test("the apex offers hosting, and records a request without granting it", async ({
      page,
    }) => {
      await page.goto(`${API_ORIGIN}/`);
      // the landing explains the tool; the form lives on its own view
      await expect(
        page.getByRole("heading", {
          name: "Chercher 500 parrainages, méthodiquement",
        }),
      ).toBeVisible();
      await page
        .getByRole("button", { name: "Héberger une campagne" })
        .first()
        .click();
      await expect(
        page.getByRole("heading", { name: "Héberger une campagne" }),
      ).toBeVisible();

      await page.getByLabel("Adresse souhaitée").fill(SLUG);
      await page.getByLabel("Nom de la campagne").fill("Campagne Seconde");
      await page.getByLabel("Votre nom").fill("Alex Porteur");
      await page.getByLabel("Votre adresse email").fill(REQUESTER);
      await page
        .getByLabel("En quelques mots, la campagne")
        .fill("Nous présentons une candidature peu médiatisée.");
      await page.getByRole("button", { name: "Envoyer la demande" }).click();

      await expect(
        page.getByRole("heading", { name: "Demande enregistrée" }),
      ).toBeVisible();

      // Nothing has been created. This is the whole point of moderating:
      // otherwise the first abuse is squatting a candidate's name, and the
      // squatted campaign has no recourse — the subdomain is already taken.
      const config = await page.request.get(
        `${campaignOrigin(SLUG)}/api/config`,
      );
      expect(config.status()).toBe(404);
    });

    test("the same address cannot be requested twice while it is pending", async ({
      page,
    }) => {
      await page.goto(`${API_ORIGIN}/`);
      await page
        .getByRole("button", { name: "Héberger une campagne" })
        .first()
        .click();
      await page.getByLabel("Adresse souhaitée").fill(SLUG);
      await page.getByLabel("Nom de la campagne").fill("Un autre qui la veut");
      await page.getByLabel("Votre nom").fill("Quelqu'un");
      await page.getByLabel("Votre adresse email").fill("autre@exemple.test");
      await page.getByRole("button", { name: "Envoyer la demande" }).click();

      await expect(page.getByText(/attend une réponse/)).toBeVisible();
    });

    test("moderation opens the campaign and hands over its coordination once", async ({
      page,
    }) => {
      await page.goto(`${API_ORIGIN}/`);
      await page.getByRole("button", { name: "Se connecter" }).click();
      await page.getByLabel("Adresse email").fill(INSTANCE_ADMIN.email);
      await page.getByLabel("Mot de passe").fill(INSTANCE_ADMIN.password);
      await page.getByRole("button", { name: "Se connecter" }).click();

      await expect(
        page.getByRole("heading", { name: "Demandes d'hébergement" }),
      ).toBeVisible();
      // scoped to the card, as the team queue's own journey does: the
      // address is now on the card AND in the confirmation the button waits
      // for, so a page-wide match resolves to two
      const queued = page.locator(".carte", { hasText: SLUG });
      await expect(queued).toContainText(REQUESTER);

      // approving SENDS a session link to the address a stranger typed: the
      // button is inert until the administrator confirms having read it
      await page.getByLabel(/J'ai vérifié/).check();
      await page.getByRole("button", { name: "Ouvrir la campagne" }).click();
      const opened = page.getByRole("heading", {
        name: `Campagne ouverte : ${SLUG}.localhost`,
      });
      await expect(opened).toBeVisible();

      // shown once and stored nowhere in clear: the administrator passes it on
      coordinationPassword = (
        await page.locator(".carte code").first().innerText()
      ).trim();
      expect(coordinationPassword.length).toBeGreaterThan(8);
    });

    // The invitation the approval above sent, opened the way its recipient
    // opens it. It is the most special-cased of the three this application
    // mints: written on the APEX, inside the transaction that creates a
    // campaign which did not exist a moment before, with its link built from
    // a slug rather than from the Host the administrator was on.
    //
    // Driven HERE rather than beside the other link journeys, and that is not
    // tidiness: the public form is bounded to three per hour per source, so a
    // spec filing its own request to read its own inbox spends a ceiling the
    // suite needs — measured, it took the fourth and read a 429 instead of a
    // confirmation. The approval this journey already performs sends exactly
    // the message to open.
    test("the invitation that approval sent opens the campaign", async ({
      browser,
    }) => {
      const link = linkIn(await waitForMail(REQUESTER));
      // the NEW campaign's subdomain, prefixed to the configured apex — not
      // the apex the decision was taken on, where the token belongs to no
      // campaign and the visit can only end on a sign-in screen
      expect(link.startsWith(`${campaignOrigin(SLUG)}/connexion#jeton=`)).toBe(
        true,
      );

      const opened = await visit(browser, link);
      await expect(
        opened.page.getByRole("button", { name: "déconnexion" }),
      ).toBeVisible();
      await expect(opened.page.getByText("Alex Porteur")).toBeVisible();
      await opened.context.close();
    });

    test("the requester signs in on their own subdomain, as coordination", async ({
      page,
    }) => {
      await signIn(page, campaignOrigin(SLUG), REQUESTER, coordinationPassword);
      // the management screen only shows for someone who may manage
      // accounts, and it is called « Ma campagne » for a coordination
      await openManagement(page);
      await expect(
        page.getByRole("heading", { name: "La campagne" }),
      ).toBeVisible();
    });

    test("the administration creates a campaign directly, without a request", async ({
      page,
    }) => {
      await page.goto(`${API_ORIGIN}/`);
      await page.getByRole("button", { name: "Se connecter" }).click();
      await page.getByLabel("Adresse email").fill(INSTANCE_ADMIN.email);
      await page.getByLabel("Mot de passe").fill(INSTANCE_ADMIN.password);
      await page.getByRole("button", { name: "Se connecter" }).click();
      await expect(
        page.getByRole("heading", { name: "Créer une campagne" }),
      ).toBeVisible();

      // Scoped to the form, which carries an accessible name of its own: the
      // screen holds a second one — opening an access on an existing campaign
      // — and it has a « Nom » and an « Adresse email » too. Unscoped, these
      // resolve to two elements and the journey fails on the ambiguity rather
      // than on anything being wrong.
      const creation = page.getByRole("form", { name: "Créer une campagne" });
      await creation.getByLabel("Adresse (sous-domaine)").fill("directe");
      await creation
        .getByLabel("Nom de la campagne")
        .fill("Campagne ouverte en direct");
      // the coordination account's own two fields, under their group heading
      await creation.getByLabel("Nom", { exact: true }).fill("Coordination");
      await creation.getByLabel("Adresse email").fill("coord@directe.test");
      await page.getByRole("button", { name: "Créer la campagne" }).click();

      // same one-time password card as an approval, whatever the door
      await expect(
        page.getByRole("heading", {
          name: "Campagne ouverte : directe.localhost",
        }),
      ).toBeVisible();
      const password = (
        await page.locator(".carte code").first().innerText()
      ).trim();
      expect(password.length).toBeGreaterThan(8);

      // the campaign answers, and its coordination enters with that password
      await signIn(
        page,
        campaignOrigin("directe"),
        "coord@directe.test",
        password,
      );
      await openManagement(page);
      await expect(
        page.getByRole("heading", { name: "La campagne" }),
      ).toBeVisible();
    });

    test("the home lists the hosted campaigns, searchable", async ({
      page,
    }) => {
      await page.goto(`${API_ORIGIN}/`);
      await expect(
        page.getByRole("heading", { name: "Les campagnes hébergées" }),
      ).toBeVisible();
      // every campaign this journey opened is findable
      await expect(
        page.getByRole("link", { name: "Campagne Seconde" }),
      ).toBeVisible();
      await expect(
        page.getByRole("link", { name: "Campagne ouverte en direct" }),
      ).toBeVisible();
      // the bootstrap campaign is still named by the shipped template
      // (« Prénom NOM ») : that is no identity to advertise, and its
      // address must not appear until its coordination names it
      await expect(page.getByText(`${FIRST_CAMPAIGN}.localhost`)).toHaveCount(
        0,
      );
      // typing narrows the list as you go
      await page.getByLabel("Rechercher une campagne").fill("direct");
      await expect(
        page.getByRole("link", { name: "Campagne Seconde" }),
      ).toBeHidden();
      await expect(
        page.getByRole("link", { name: "Campagne ouverte en direct" }),
      ).toBeVisible();
      // the shared counter: ONE node whose text settles 400 ms after the
      // last keystroke, so a screen reader hears the result and not the
      // stream of numbers on the way to it
      await expect(page.getByText("1 affiché(s) sur 2.")).toBeVisible();
    });

    test("the instance administrator never sees a campaign's work", async ({
      page,
    }) => {
      await page.goto(`${API_ORIGIN}/`);
      await page.getByRole("button", { name: "Se connecter" }).click();
      await page.getByLabel("Adresse email").fill(INSTANCE_ADMIN.email);
      await page.getByLabel("Mot de passe").fill(INSTANCE_ADMIN.password);
      await page.getByRole("button", { name: "Se connecter" }).click();
      await expect(
        page.getByRole("heading", { name: "Demandes d'hébergement" }),
      ).toBeVisible();

      // the moderation session carries no rights inside any campaign
      for (const origin of [
        campaignOrigin(SLUG),
        campaignOrigin(FIRST_CAMPAIGN),
      ]) {
        const board = await page.request.get(`${origin}/api/dashboard`);
        expect(
          board.status(),
          `${origin} answered the instance administrator`,
        ).toBe(401);
      }
    });
  });
