import { expect, test } from "@playwright/test";

import { STATIC_ORIGIN } from "./config.ts";

// The lists of the account-less version: the priority list it loads on its
// own, the full base behind « Changer de liste », and the synthetic set for
// trying the tool. What must hold across every swap is the TRACKING: it is
// keyed by INSEE code, so changing lists must never touch it.

test.describe
  .serial("the browser version's lists", () => {
    test("the full base replaces the light list, and the tracking survives", async ({
      page,
    }) => {
      await page.goto(`${STATIC_ORIGIN}/`);
      // the light list downloads on its own at first visit
      const first = page.locator("table button.lien").first();
      await expect(first).toBeVisible({ timeout: 20_000 });

      // work recorded on the light list, before any swap — and the card's
      // own address kept, so the same mayor is reopened through no list at
      // all: a row's name is ambiguous across 1 060 rows, an INSEE is not
      await first.click();
      await page.getByLabel("Statut").selectOption({ label: "Email envoyé" });
      await page
        .getByRole("textbox", { name: "Note", exact: true })
        .fill("noté avant le changement de liste");
      await page
        .getByRole("button", { name: "Enregistrer", exact: true })
        .click();
      await expect(
        page.getByText("noté avant le changement de liste"),
      ).toBeVisible();
      const cardUrl = page.url();

      await page.getByRole("button", { name: "Mes données" }).click();
      await expect(
        page.getByText(/Chargée actuellement : liste prioritaire/),
      ).toBeVisible();
      // the faux-jeu's priority list: its 260 endorsers and nothing else
      await expect(page.getByText("260 maires chargés.")).toBeVisible();

      await page.getByRole("button", { name: "Base complète" }).click();
      // …and the full base is every rank, including the 620 with no signal
      await expect(page.getByText("1060 maires chargés.")).toBeVisible({
        timeout: 20_000,
      });
      await expect(
        page.getByText(/Chargée actuellement : base complète/),
      ).toBeVisible();

      // the same mayor, reopened by address, still carries the status and
      // the note: the tracking is indexed by INSEE code
      await page.goto(cardUrl);
      await expect(page.getByLabel("Statut")).toHaveValue("email_sent");
      await expect(
        page.getByText("noté avant le changement de liste"),
      ).toBeVisible();
    });

    test("the synthetic set loads for trying the tool, and says it is fiction", async ({
      page,
    }) => {
      await page.goto(`${STATIC_ORIGIN}/`);
      await expect(page.locator("table button.lien").first()).toBeVisible({
        timeout: 20_000,
      });
      await page.getByRole("button", { name: "Mes données" }).click();
      await page.getByRole("button", { name: "Données fictives" }).click();

      // said in capitals, twice: the message and the loaded-list line — a
      // list of invented mayors must never read as the real one
      await expect(page.getByText(/maires FICTIFS chargés/)).toBeVisible();
      await expect(
        page.getByText(/Chargée actuellement : un jeu de données FICTIVES/),
      ).toBeVisible();

      // its cards open like any other's
      await page.getByRole("button", { name: "Les maires" }).click();
      await expect(page.locator("table button.lien").first()).toBeVisible();

      // …and the way back to the real list works both ways
      await page.getByRole("button", { name: "Mes données" }).click();
      await page.getByRole("button", { name: "Liste prioritaire" }).click();
      await expect(
        page.getByText(/Chargée actuellement : liste prioritaire/),
      ).toBeVisible({ timeout: 20_000 });
      await expect(page.getByText("260 maires chargés.")).toBeVisible();
    });
  });
