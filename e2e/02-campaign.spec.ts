import { expect, test } from "@playwright/test";

import { COORDINATION, campaignOrigin, FIRST_CAMPAIGN } from "./config.ts";
import { openTab, signIn } from "./helpers.ts";

// The volunteer's own path, end to end: configure the campaign, reserve a
// batch, open a card, read the message the tool wrote, record the outcome.
//
// Every step crosses the whole stack — PostgreSQL, the Go API, the built
// interface — which is what makes this suite worth its weight: a column
// renamed on one side and not the other shows up here, and nowhere else.

const ORIGIN = campaignOrigin(FIRST_CAMPAIGN);
const CANDIDATE = "Camille Durand";

test.describe
  .serial("a campaign at work", () => {
    test("its coordination fills in the campaign", async ({ page }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openTab(page, "Mon équipe");

      const exact = { exact: true };
      await page.getByLabel("Son nom", exact).fill(CANDIDATE);
      await page
        .getByLabel("Qui c'est, en une ligne", exact)
        .fill("candidate écologiste, médecin");
      await page
        .getByLabel("Sa présentation en deux ou trois phrases", exact)
        .fill("Je suis médecin. Je porte la santé environnementale.");
      await page.getByLabel("Votre nom", exact).fill("Alex Coordination");
      await page
        .getByLabel("En quelle qualité", exact)
        .fill("équipe de campagne");
      await page.getByLabel("Téléphone", exact).fill("06 12 34 56 78");
      await page.getByLabel("Email", exact).fill("contact@premiere.test");
      await page
        .getByLabel("Site de la campagne", exact)
        .fill("https://premiere.test");
      await page.getByLabel("Ville d'où vous écrivez", exact).fill("Lyon");
      await page
        .getByRole("button", { name: "Enregistrer la campagne" })
        .click();

      await expect(
        page.getByText(/Les messages sont prêts à partir/),
      ).toBeVisible();
      // and the « campaign not configured » banner is gone: it is the banner
      // that tells volunteers not to send anything yet
      await expect(page.getByText("Campagne non configurée")).toHaveCount(0);
    });

    test("a volunteer reserves a batch nobody else will get", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openTab(page, "Mon tableau");

      await expect(
        page.getByRole("heading", { name: "Mes maires (0)" }),
      ).toBeVisible();
      await page.getByRole("button", { name: "Prendre un lot" }).click();
      await expect(
        page.getByRole("heading", { name: /Mes maires \([1-9]/ }),
      ).toBeVisible();
    });

    test("a card carries the mayor's own data and a written message", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openTab(page, "Mon tableau");

      const firstTown = page.locator("table button.lien").first();
      const town = (await firstTown.innerText()).trim();
      await firstTown.click();

      // the card names the mayor and the town it came from: the row travelled
      // whole, from the database to the screen
      await expect(page.getByRole("heading", { level: 1 })).toContainText(/\w/);
      await expect(
        page.getByText(town, { exact: false }).first(),
      ).toBeVisible();

      // the message is generated from the campaign configuration AND from the
      // mayor's row: both sides of the data path in a single assertion
      const body = await page.getByLabel("Message").inputValue();
      expect(body).toContain(CANDIDATE);
      expect(body).not.toMatch(/\{[^}]+\}/); // no placeholder left unfilled
    });

    test("recording the outcome is shared, and shows in the history", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openTab(page, "Mon tableau");
      await page.locator("table button.lien").first().click();

      await page.getByLabel("Statut").selectOption({ label: "Email envoyé" });
      await page.getByLabel("Note").fill("écrit ce matin, réponse promise");
      await page.getByRole("button", { name: "Enregistrer" }).click();

      await expect(
        page.getByRole("heading", { name: "Historique" }),
      ).toBeVisible();
      await expect(
        page.getByText("écrit ce matin, réponse promise"),
      ).toBeVisible();

      // and it survives a reload: the work lives on the server, not in the tab
      await page.reload();
      await openTab(page, "Mon tableau");
      await expect(page.getByText("Email envoyé").first()).toBeVisible();
    });

    test("the export carries the working columns", async ({ page }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      const csv = await page.request.get(`${ORIGIN}/api/export.csv`);
      expect(csv.status()).toBe(200);
      expect(csv.headers()["content-type"]).toContain("text/csv");

      const text = await csv.text();
      // the BOM is what makes Excel and LibreOffice read it as UTF-8
      expect(text.startsWith("\uFEFF")).toBe(true);
      const header = text.split("\n")[0];
      for (const column of ["insee_code", "commune", "volunteer", "status"]) {
        expect(header, `column ${column} missing from the export`).toContain(
          column,
        );
      }
      expect(text).toContain(COORDINATION.email);
    });
  });
