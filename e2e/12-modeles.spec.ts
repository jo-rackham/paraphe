import { expect, type Page, test } from "@playwright/test";

import { COORDINATION, campaignOrigin, FIRST_CAMPAIGN } from "./config.ts";
import { openFirstCard, openManagement, signIn } from "./helpers.ts";

// A campaign rewrites the texts it sends, and the card a volunteer opens is
// where that has to show. The unit tests prove the layering and the Go tests
// prove the refusals; only this path proves that what a coordination typed
// into « Ma campagne » is what comes out of the engine on the next screen.

const ORIGIN = campaignOrigin(FIRST_CAMPAIGN);
const OWN_LETTER =
  "Notre texte à nous, pour {salutation} de {commune_de}.\n" +
  "\n" +
  "{signataire}, {signataire_qualite}\n";

const editor = (page: Page) =>
  page.locator(".carte", { hasText: "Les modèles de messages" });

const choose = async (page: Page, file: string) => {
  await editor(page).getByLabel("Modèle").selectOption(file);
};

const box = (page: Page) => editor(page).getByLabel(/^Texte/);

const save = async (page: Page) => {
  await editor(page).getByRole("button", { name: "Enregistrer" }).click();
};

/**
 * The printed letter of the first card on the dashboard.
 *
 * The batch is taken by `openFirstCard` if this account holds none, so the
 * file does not depend on which earlier journey happened to reserve one. It
 * DOES still depend on the campaign being configured, which `02-campaign`
 * does — the same order `06-messages` relies on, and the reason both read a
 * card rather than an error message.
 */
const letterOfACard = async (page: Page): Promise<string> => {
  await openFirstCard(page);
  await page.getByText("📮 Courrier").click();
  return page.locator("pre.lettre").innerText();
};

test.describe
  .serial("a campaign's own message templates", () => {
    test("what the coordination writes is what the card renders", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);

      // the letter the image ships, before anything is rewritten
      expect(await letterOfACard(page)).not.toContain("Notre texte à nous");

      await openManagement(page);
      await choose(page, "courrier.txt");
      // EMPTY, showing the inherited text as a PLACEHOLDER: a box pre-filled
      // with it becomes a frozen copy the moment anybody presses Enregistrer,
      // and every later correction stops arriving
      await expect(box(page)).toHaveValue("");
      expect(await box(page).getAttribute("placeholder")).toContain(
        "{salutation}",
      );

      await box(page).fill(OWN_LETTER);
      await save(page);
      await expect(page.getByText(/Modèles enregistrés/)).toBeVisible();

      // read back from the SERVER, and this line is not decoration: the card
      // below renders from `me`, which this screen has just updated locally,
      // so it would show the new letter even if nothing had been stored
      const me = await (await page.request.get(`${ORIGIN}/api/me`)).json();
      expect(me.templates.campaign["courrier.txt"]).toBe(OWN_LETTER);

      const letter = await letterOfACard(page);
      expect(letter).toContain("Notre texte à nous");
      // the placeholders are FILLED, not copied through
      expect(letter).not.toContain("{commune_de}");
      expect(letter).toMatch(/Notre texte à nous, pour (Madame|Monsieur) l/);
    });

    // The campaign rewrote the LETTER only. Every other channel still comes
    // from the image — that is what a sparse overlay means, and it is what
    // lets a later release improve the texts nobody touched.
    test("the channels it did not touch still come from the image", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await letterOfACard(page);
      const email = await page.getByLabel("Message").inputValue();
      expect(email).not.toContain("Notre texte à nous");
      expect(email.length).toBeGreaterThan(100);
    });

    // REFUSED AT SAVE, in front of the person who typed it — not at send,
    // where it is 1 960 letters not printed and nobody able to fix them.
    test("a template the engine would refuse is refused on the spot", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openManagement(page);
      await choose(page, "courrier_decouverte.txt");
      // the project's cardinal mistake: thanking, in the template written to
      // somebody with no endorsement on record
      await box(page).fill(
        "En {annee_recente}, vous avez présenté {candidat_recent}.\n",
      );
      await save(page);
      // named, and naming the audience: told only that the placeholder is
      // unknown, whoever pasted it looks for a typo in a word spelt correctly.
      // IN THE CARD'S OWN SLOT, beside the button that was pressed: the page
      // banner lives at the top of a long screen and misses the eye of
      // whoever is scrolled down here — a refusal shown nowhere visible was
      // reported as « nothing happens ».
      await expect(
        editor(page).locator('[role="alert"]', { hasText: "annee_recente" }),
      ).toBeVisible();

      // and NOTHING was stored — read back from a fresh load, not inferred
      // from the refusal
      await page.reload();
      await openManagement(page);
      await choose(page, "courrier_decouverte.txt");
      await expect(box(page)).toHaveValue("");
    });

    // THE ACCOUNT-LESS VERSION SPEAKS THE CAMPAIGN'S WORDS TOO.
    //
    // Without this a campaign that had rewritten its letter had two voices:
    // one to the volunteers with an account, one to the volunteers without,
    // and nothing on either screen saying which. Driven on the campaign's OWN
    // origin, where the account-less build fills itself in with no click.
    test("the browser version adopts the campaign's own texts", async ({
      page,
    }) => {
      await page.goto(`${ORIGIN}/navigateur/`);
      // it is the account-less build: no mode marker, on the very origin
      // that serves an API
      await expect(page.locator('meta[name="paraphe-mode"]')).toHaveCount(0);
      await expect(page.getByText(/repris depuis son site/)).toBeVisible({
        timeout: 20_000,
      });

      await expect(page.locator("table button.lien").first()).toBeVisible({
        timeout: 20_000,
      });
      await page.locator("table button.lien").first().click();
      await page.getByText("📮 Courrier").click();
      const letter = await page.locator("pre.lettre").innerText();
      expect(letter).toContain("Notre texte à nous");
      // rendered, not copied through
      expect(letter).not.toMatch(/\{[^}]+\}/);

      // …and the volunteer can read and change them, which is what the
      // adoption screen promises. The campaign's text is the PLACEHOLDER of
      // an empty box, exactly as team mode shows an inherited text: filled
      // in as the value it would be a frozen copy, and the campaign's next
      // correction would stop reaching this browser.
      await page.getByRole("link", { name: "Ma campagne" }).click();
      await choose(page, "courrier.txt");
      await expect(box(page)).toHaveValue("");
      expect(await box(page).getAttribute("placeholder")).toContain(
        "Notre texte à nous",
      );
      await expect(
        editor(page).getByText("vide : suit le texte de la campagne"),
      ).toBeVisible();
    });

    // « Revenir au texte fourni » puts the campaign back on the image's text
    // and keeps it there: the override is REMOVED, not emptied.
    test("the campaign can go back to the shipped text", async ({ page }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openManagement(page);
      await choose(page, "courrier.txt");
      await expect(box(page)).toHaveValue(OWN_LETTER);
      await editor(page)
        .getByRole("button", { name: "Revenir au texte" })
        .click();
      await save(page);
      await expect(page.getByText(/aucun texte personnalisé/)).toBeVisible();

      const letter = await letterOfACard(page);
      expect(letter).not.toContain("Notre texte à nous");
      expect(letter.length).toBeGreaterThan(100);
    });
  });
