import { expect, test } from "@playwright/test";

import { STATIC_ORIGIN } from "./config.ts";

// The published application, with nothing behind it — the GitHub Pages case.
//
// It reads the lists straight from the CSV files sitting beside it, so this is
// where a renamed column shows up as an empty table rather than as an error.
// And it must never pretend to be the team application: the work would go into
// a browser database nobody backs up.

const CANDIDATE = "Ariane Fictive";

test.describe
  .serial("browser mode", () => {
    test("loads its list from the published CSV", async ({ page }) => {
      await page.goto(`${STATIC_ORIGIN}/`);
      await expect(page.getByText("version navigateur")).toBeVisible();

      // the light list downloads on its own at first visit
      const rows = page.locator("table button.lien");
      await expect(rows.first()).toBeVisible({ timeout: 20_000 });
      expect(await rows.count()).toBeGreaterThan(0);
    });

    test("a card is built from the CSV columns", async ({ page }) => {
      await page.goto(`${STATIC_ORIGIN}/`);
      const first = page.locator("table button.lien").first();
      await expect(first).toBeVisible({ timeout: 20_000 });
      const town = (await first.innerText()).trim();
      expect(town.length).toBeGreaterThan(0);
      await first.click();

      // « Pourquoi cette personne » is written from the mayor's own columns:
      // empty here would mean the CSV headers and the code have drifted apart
      await expect(page.getByText("Pourquoi cette personne")).toBeVisible();
      await expect(page.getByLabel("Message")).toBeVisible();
    });

    // Kept to what the volunteer sees. Asserting on IndexedDB itself was
    // tried and dropped: read through a second connection, the store answers
    // empty for a moment after a write that has already reached the screen —
    // a race in the observation, not in the application. And a page reload is
    // no better here: Playwright's contexts are ephemeral, so once the browser
    // has served the preceding tests, a reload comes back to an empty database.
    test("what is recorded shows on the list it came from", async ({
      page,
    }) => {
      await page.goto(`${STATIC_ORIGIN}/`);
      const first = page.locator("table button.lien").first();
      await expect(first).toBeVisible({ timeout: 20_000 });
      const town = (await first.innerText()).trim();
      await first.click();

      await page.getByLabel("Statut").selectOption({ label: "Email envoyé" });
      await page
        .getByRole("textbox", { name: "Note", exact: true })
        .fill("noté hors ligne");
      await page
        .getByRole("button", { name: "Enregistrer", exact: true })
        .click();
      await expect(page.getByText("noté hors ligne")).toBeVisible();

      // the arrow is decorative (aria-hidden): not part of the button's name
      await page.getByRole("button", { name: "retour à la liste" }).click();
      // Matched on the EXACT town: the synthetic towns are numbered, and
      // « Sainte-Fiction-1 » is a prefix of « Sainte-Fiction-10 ».
      const link = page.getByRole("button", { name: town, exact: true });
      const row = page.locator("table tr").filter({ has: link }).first();
      await expect(row).toContainText("Email envoyé");
    });

    // The same two acts as the account version, against IndexedDB instead of
    // PostgreSQL — and the same rule about the head, written twice because
    // the two worlds share no code there. Everything here was written in this
    // browser, so there is no author to tell from a colleague and nothing to
    // refuse: the buttons are on every line.
    test("a note is corrected and removed, and the card follows", async ({
      page,
    }) => {
      await page.goto(`${STATIC_ORIGIN}/`);
      const first = page.locator("table button.lien").first();
      await expect(first).toBeVisible({ timeout: 20_000 });
      await first.click();

      await page.getByLabel("Statut").selectOption({ label: "Email envoyé" });
      await page
        .getByRole("textbox", { name: "Note", exact: true })
        .fill("courriel parti");
      await page
        .getByRole("button", { name: "Enregistrer", exact: true })
        .click();
      await expect(page.getByText("courriel parti")).toBeVisible();
      // …and the SAVE is finished, which the history appearing does not say:
      // the line is drawn from state written inside the awaited call, while
      // the handler goes on to clear this field. Typing into it before that
      // lands loses what was typed — measured, and it recorded an empty note.
      await expect(
        page.getByRole("textbox", { name: "Note", exact: true }),
      ).toHaveValue("");

      await page.getByLabel("Statut").selectOption({ label: "À rappeler" });
      await page
        .getByRole("textbox", { name: "Note", exact: true })
        .fill("aple lundi");
      await page
        .getByRole("button", { name: "Enregistrer", exact: true })
        .click();
      await expect(page.getByText("aple lundi")).toBeVisible();
      await expect(
        page.getByRole("textbox", { name: "Note", exact: true }),
      ).toHaveValue("");

      // « la note 1 » is the most recent: the history is newest first, and
      // two outcomes of the same minute share a date, so the position is what
      // names a row.
      await page.getByRole("button", { name: "Modifier la note 1 du" }).click();
      await page.getByLabel("Texte de la note").fill("rappeler lundi");
      await page.getByRole("button", { name: "Enregistrer la note" }).click();
      await expect(page.getByText("rappeler lundi")).toBeVisible();
      await expect(page.getByText(/modifiée le/).first()).toBeVisible();
      // the editor closes on success — and until it does, its row carries no
      // « Supprimer », so the next click would land on the row BELOW
      await expect(page.getByLabel("Texte de la note")).toHaveCount(0);

      await page
        .getByRole("button", { name: "Supprimer la note 1 du" })
        .click();
      await expect(page.getByText("Supprimer cette note ?")).toBeVisible();
      await page.getByRole("button", { name: "Confirmer" }).click();
      await expect(page.getByText("rappeler lundi")).toHaveCount(0);
      // the card goes back to what the history now says, and not to the
      // status it was announcing
      await expect(page.getByLabel("Statut")).toHaveValue("email_sent");
      await expect(page.getByText("courriel parti")).toBeVisible();
    });

    test("never claims to be the team application", async ({ page }) => {
      await page.goto(`${STATIC_ORIGIN}/`);
      // no mode marker, and therefore no promise that work reaches a server
      await expect(page.locator('meta[name="paraphe-mode"]')).toHaveCount(0);
      await expect(
        page.getByText("aucune donnée ne quitte ce navigateur"),
      ).toBeVisible();
    });

    test("the department filter narrows the list to one department", async ({
      page,
    }) => {
      await page.goto(`${STATIC_ORIGIN}/`);
      await expect(page.locator("table button.lien").first()).toBeVisible({
        timeout: 20_000,
      });

      const others = (
        await page.getByLabel("Département").locator("option").allInnerTexts()
      ).filter((d) => !d.includes("Aveyron") && !d.includes("—"));
      // the fixture spans ten departments: rejecting one of them proves
      // little, so every other one must be gone
      expect(others.length).toBeGreaterThan(3);

      await page.getByLabel("Département").selectOption("Aveyron");
      const rows = page.locator("table tbody tr");
      expect(await rows.count()).toBeGreaterThan(0);
      for (const other of others) {
        await expect(page.locator("table tbody")).not.toContainText(other);
      }
    });

    test("the campaign filled in the browser feeds the messages", async ({
      page,
    }) => {
      await page.goto(`${STATIC_ORIGIN}/`);
      await expect(page.locator("table button.lien").first()).toBeVisible({
        timeout: 20_000,
      });
      await expect(page.getByText("Campagne non configurée")).toBeVisible();

      await page.getByRole("button", { name: "Ma campagne" }).click();
      const fields: [string, string][] = [
        ["Son nom", CANDIDATE],
        ["Qui c'est, en une ligne", "candidate indépendante, institutrice"],
        [
          "Sa présentation en deux ou trois phrases",
          "Je suis institutrice. Je porte l'école rurale.",
        ],
        ["Votre nom", "Paul Bénévole"],
        ["En quelle qualité", "équipe de campagne"],
        ["Téléphone", "06 98 76 54 32"],
        ["Email", "contact@fictive.test"],
        ["Site de la campagne", "https://fictive.test"],
        ["Ville d'où vous écrivez", "Aurillac"],
        [
          "Votre touche personnelle (insérée dans vos emails)",
          "Je suis moi-même élu municipal.",
        ],
      ];
      for (const [label, value] of fields) {
        await page.getByLabel(label, { exact: true }).fill(value);
      }
      await page
        .getByRole("button", { name: "Enregistrer", exact: true })
        .click();
      await expect(
        page.getByText("Campagne enregistrée dans ce navigateur."),
      ).toBeVisible();
      // the banner that told the volunteer not to send anything is gone
      await expect(page.getByText("Campagne non configurée")).toHaveCount(0);

      await page.getByRole("button", { name: "Les maires" }).click();
      await page.locator("table button.lien").first().click();
      const body = await page.getByLabel("Message").inputValue();
      expect(body).toContain(CANDIDATE);
      expect(body).toContain("Je suis moi-même élu municipal.");
      expect(body).not.toMatch(/\{[^}]+\}/);
    });
    test("a backup leaves, the browser is wiped, the backup comes back whole", async ({
      page,
    }) => {
      await page.goto(`${STATIC_ORIGIN}/`);
      const first = page.locator("table button.lien").first();
      await expect(first).toBeVisible({ timeout: 20_000 });
      const town = (await first.innerText()).trim();
      await first.click();
      await page.getByLabel("Statut").selectOption({ label: "Email envoyé" });
      await page
        .getByRole("textbox", { name: "Note", exact: true })
        .fill("avant la sauvegarde");
      await page
        .getByRole("button", { name: "Enregistrer", exact: true })
        .click();
      await expect(page.getByText("avant la sauvegarde")).toBeVisible();

      // Self-contained on purpose: Playwright hands each test a fresh
      // browser, so the campaign configured by the previous one is not
      // here. The backup must carry BOTH the tracking and the campaign.
      await page.getByRole("button", { name: "Ma campagne" }).click();
      await page.getByLabel("Son nom", { exact: true }).fill(CANDIDATE);
      await page
        .getByRole("button", { name: "Enregistrer", exact: true })
        .click();
      await expect(
        page.getByText("Campagne enregistrée dans ce navigateur."),
      ).toBeVisible();

      await page.getByRole("button", { name: "Mes données" }).click();
      const receiving = page.waitForEvent("download");
      // the arrow is decorative (aria-hidden): not part of the button's name
      await page.getByRole("button", { name: "Exporter (JSON)" }).click();
      const backup = await (await receiving).path();

      // wiping is what a volunteer does at the end of a campaign — the
      // backup is the only way their work survives it
      page.on("dialog", (d) => d.accept());
      await page.getByRole("button", { name: "Effacer ce navigateur" }).click();
      await expect(
        page.getByText("Tout a été effacé de ce navigateur."),
      ).toBeVisible();
      await page.getByRole("button", { name: "Les maires" }).click();
      await expect(page.getByText("Aucune liste chargée")).toBeVisible();

      // RELOAD before restoring. Erasing and importing in the same page
      // session is the one path where the settings store stays empty: the
      // first-visit download writes which list is loaded, so after any
      // reload — and every real restore has one — the store is no longer
      // empty and an all-or-nothing guard silently stops protecting.
      await page.reload();
      await expect(page.locator("table button.lien").first()).toBeVisible({
        timeout: 20_000,
      });

      await page.getByRole("button", { name: "Mes données" }).click();
      await page
        .locator('input[accept=".json,application/json"]')
        .setInputFiles(backup);
      await expect(page.getByText(/Import :/)).toBeVisible();

      await page.getByRole("button", { name: "Les maires" }).click();
      const link = page.getByRole("button", { name: town, exact: true });
      const row = page.locator("table tr").filter({ has: link }).first();
      await expect(row).toContainText("Email envoyé");

      // The tracking is not the whole backup. "Fusionner" is checked by
      // default. Skipping the settings would bring the campaign back empty
      // after a wipe, reverting every message to the shipped template with a
      // report that says nothing.
      await page.getByRole("button", { name: "Ma campagne" }).click();
      await expect(page.getByLabel("Son nom", { exact: true })).toHaveValue(
        CANDIDATE,
      );
    });
  });
