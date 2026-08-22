import { expect, type Page, test } from "@playwright/test";

import {
  API_ORIGIN,
  COORDINATION,
  campaignOrigin,
  FIRST_CAMPAIGN,
} from "./config.ts";
import { openFirstCard, openManagement, openTab, signIn } from "./helpers.ts";

// A campaign is not its candidate: it has a name of its own, a say over the
// instance's public directory, and a default answer to « appelons-nous les
// maires » that each volunteer may override. All three live on « Ma
// campagne » and none had a journey.

const ORIGIN = campaignOrigin(FIRST_CAMPAIGN);
const CAMPAIGN_NAME = "Campagne Première";

// this file's own source — the shared one's sign-in budget is spent whole
// by the journeys before it (see global-setup, PARAPHE_TRUSTED_PROXIES)
test.use({ extraHTTPHeaders: { "X-Forwarded-For": "192.0.2.16" } });

/** Saves « Ma campagne » and waits for the confirmation the banner reads. */
async function saveCampaign(page: Page) {
  await page.getByRole("button", { name: "Enregistrer la campagne" }).click();
  await expect(page.getByText(/Campagne enregistrée/)).toBeVisible();
  // and the word BESIDE the button: the banner lives at the top of a long
  // form, off-screen from where the press happened
  await expect(page.getByText("Enregistré.", { exact: true })).toBeVisible();
}

/** The email of the first card on this account's dashboard. */
const emailOfACard = async (page: Page): Promise<string> => {
  await openFirstCard(page);
  return page.getByLabel("Message").inputValue();
};

test.describe
  .serial("the campaign's own name, directory entry and phone default", () => {
    test("named, the campaign appears in the instance's directory", async ({
      page,
    }) => {
      // 01-instance asserted the OPPOSITE while the campaign was unnamed:
      // the directory shows nothing it cannot name. This is the other half.
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openManagement(page);
      await page.getByLabel("Nom de la campagne").fill(CAMPAIGN_NAME);
      await saveCampaign(page);

      await page.goto(`${API_ORIGIN}/`);
      await expect(
        page.getByRole("heading", { name: "Les campagnes hébergées" }),
      ).toBeVisible();
      await expect(
        page.getByRole("link", { name: CAMPAIGN_NAME }),
      ).toBeVisible();
    });

    test("unlisted, it leaves the directory and keeps answering", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openManagement(page);
      await page
        .getByLabel("Référencer la campagne dans l'annuaire public")
        .uncheck();
      await saveCampaign(page);

      await page.goto(`${API_ORIGIN}/`);
      await expect(
        page.getByRole("heading", { name: "Les campagnes hébergées" }),
      ).toBeVisible();
      await expect(page.getByRole("link", { name: CAMPAIGN_NAME })).toHaveCount(
        0,
      );
      // discretion, not disappearance: whoever knows the address still enters
      const door = await page.request.get(`${ORIGIN}/api/config`);
      expect(door.status()).toBe(200);

      // back in the directory: the toggle works both ways, and the runs
      // after this one read the state this file leaves. The session opened
      // at the top of this test is still in the jar — the apex visit did
      // not spend it — so the campaign's origin reopens signed in.
      await page.goto(`${ORIGIN}/`);
      await expect(
        page.getByRole("button", { name: "déconnexion" }),
      ).toBeVisible();
      await openManagement(page);
      await page
        .getByLabel("Référencer la campagne dans l'annuaire public")
        .check();
      await saveCampaign(page);
      await page.goto(`${API_ORIGIN}/`);
      await expect(
        page.getByRole("link", { name: CAMPAIGN_NAME }),
      ).toBeVisible();
    });

    test("the campaign's phone opt-in enters the generated messages", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);

      // opt-in is the default OFF: no message promises a call the campaign
      // never said it would make
      expect(await emailOfACard(page)).not.toContain(
        "quelques minutes par téléphone",
      );

      await openManagement(page);
      await page
        .getByLabel("Cette campagne appelle les maires qu'elle contacte")
        .check();
      await saveCampaign(page);

      // the email now ASKS, the letter ANNOUNCES — the two sentences the
      // engine writes only for a campaign that opted in
      expect(await emailOfACard(page)).toContain(
        "quelques minutes par téléphone",
      );
      await page.getByText("📮 Courrier").click();
      await expect(page.locator("pre.lettre")).toContainText(
        "Nous nous permettrons de vous appeler",
      );
    });

    test("a volunteer's own answer overrides the campaign's", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      // saved is asserted on the SERVER's state, not on the banner: the
      // shell's message survives tab changes, so the second save would read
      // the first one's sentence while its own write is still in flight
      const saveProfile = async (stored: boolean | null) => {
        await page
          .locator(".carte", { hasText: "Votre touche personnelle" })
          .getByRole("button", { name: "Enregistrer" })
          .click();
        await expect
          .poll(
            async () =>
              (await (await page.request.get(`${ORIGIN}/api/me`)).json())
                .account.phone_outreach,
          )
          .toBe(stored);
      };
      await openTab(page, "Mon profil");
      await page
        .getByLabel("Appels téléphoniques")
        .selectOption({ label: "Je n'appelle pas" });
      await saveProfile(false);

      // the campaign says « j'appelle », this account says no: no message of
      // THEIRS promises a call
      expect(await emailOfACard(page)).not.toContain(
        "quelques minutes par téléphone",
      );

      // back to following the campaign, and the campaign back to its
      // default: this file's changes stop at this file
      await openTab(page, "Mon profil");
      await page
        .getByLabel("Appels téléphoniques")
        .selectOption({ label: "Comme la campagne (j'appelle)" });
      await saveProfile(null);
      await openManagement(page);
      await page
        .getByLabel("Cette campagne appelle les maires qu'elle contacte")
        .uncheck();
      await saveCampaign(page);
      expect(await emailOfACard(page)).not.toContain(
        "quelques minutes par téléphone",
      );
    });
  });
