import { expect, test } from "@playwright/test";

import {
  API_ORIGIN,
  campaignOrigin,
  FIRST_CAMPAIGN,
  INSTANCE_ADMIN,
} from "./config.ts";
import { openTab, signIn } from "./helpers.ts";

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
      await expect(page.getByText(REQUESTER)).toBeVisible();

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

    test("the requester signs in on their own subdomain, as coordination", async ({
      page,
    }) => {
      await signIn(page, campaignOrigin(SLUG), REQUESTER, coordinationPassword);
      // « Mon équipe » only shows for someone who may manage accounts
      await openTab(page, "Mon équipe");
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

      await page.getByLabel("Adresse (sous-domaine)").fill("directe");
      await page
        .getByLabel("Nom de la campagne")
        .fill("Campagne ouverte en direct");
      // the coordination account's own two fields, under their group heading
      await page.getByLabel("Nom", { exact: true }).fill("Coordination");
      await page.getByLabel("Adresse email").fill("coord@directe.test");
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
      await openTab(page, "Mon équipe");
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
