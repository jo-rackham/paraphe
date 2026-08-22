import { expect, test } from "@playwright/test";

import { COORDINATION, campaignOrigin, FIRST_CAMPAIGN } from "./config.ts";

// THE BROWSER VERSION FOLLOWS ITS CAMPAIGN — for what the adoption wrote
// and nobody retouched since. A coordination that rewrites its letter AFTER
// volunteers adopted must reach them, or the campaign speaks with two voices
// again, one update behind: reported from production as exactly that. Only
// this path proves the whole chain — the snapshot written at adoption, the
// origin asked again on the next load, and the fresh text rendered on a
// card.
//
// Its own file, on its own source: it opens a coordination session, and the
// shared source the pre-rule files (01-12) live on is already near the
// sign-in ceiling. 15-modeles-equipe.spec.ts leaves the campaign overlay
// empty, which is the state this journey starts from.

const ORIGIN = campaignOrigin(FIRST_CAMPAIGN);

test.use({ extraHTTPHeaders: { "X-Forwarded-For": "192.0.2.20" } });

test.describe
  .serial("the browser version follows its campaign", () => {
    test("a text rewritten after adoption reaches the adopted browser", async ({
      page,
      request,
    }) => {
      // first visit: the adoption copies the campaign's texts — none yet
      await page.goto(`${ORIGIN}/navigateur/`);
      await expect(page.getByText(/repris depuis son site/)).toBeVisible({
        timeout: 20_000,
      });

      // the coordination rewrites the letter; the editing screen has its own
      // journey (12-modeles.spec.ts), the API is the shortest honest path to
      // "the campaign changed while the volunteer was away"
      const opened = await request.post(`${ORIGIN}/api/session`, {
        data: {
          email: COORDINATION.email,
          password: COORDINATION.password,
        },
      });
      expect(opened.ok()).toBeTruthy();
      const saved = await request.post(`${ORIGIN}/api/campaign/templates`, {
        data: {
          templates: {
            "courrier.txt":
              "Texte suivi depuis le site, pour {salutation}.\n\n{signataire}\n",
          },
        },
      });
      expect(saved.ok()).toBeTruthy();

      // the same browser comes back: nothing local was touched, so the
      // rewrite reaches it — and the screen says where it came from
      await page.reload();
      await expect(page.getByText(/mis à jour depuis son site/)).toBeVisible({
        timeout: 20_000,
      });
      await expect(page.locator("table button.lien").first()).toBeVisible({
        timeout: 20_000,
      });
      await page.locator("table button.lien").first().click();
      await page.getByText("📮 Courrier").click();
      expect(await page.locator("pre.lettre").innerText()).toContain(
        "Texte suivi depuis le site",
      );

      // the overlay goes back to empty: this file made the correction, this
      // file removes it
      const cleared = await request.post(`${ORIGIN}/api/campaign/templates`, {
        data: { templates: {} },
      });
      expect(cleared.ok()).toBeTruthy();
    });

    // A CAMPAIGN STILL AT ITS TEMPLATE VALUES SERVES ITS TEXTS. The old 409
    // took them down with the nine fields: a campaign that had rewritten its
    // email but not finished its configuration served nothing at all, and
    // its browser version spoke the image's words while the team version
    // spoke its own — measured on production, reported three times.
    test("serves its texts before the nine fields are filled", async ({
      page,
      request,
    }) => {
      const opened = await request.post(`${ORIGIN}/api/session`, {
        data: {
          email: COORDINATION.email,
          password: COORDINATION.password,
        },
      });
      expect(opened.ok()).toBeTruthy();
      // one required field back at its template value, and a rewritten text
      const config = await (await request.get(`${ORIGIN}/api/config`)).json();
      const fields = config.campaign;
      const saved = await request.post(`${ORIGIN}/api/campaign`, {
        data: { campaign: { ...fields, candidat: "Prénom NOM" } },
      });
      expect(saved.ok()).toBeTruthy();
      const wrote = await request.post(`${ORIGIN}/api/campaign/templates`, {
        data: {
          templates: {
            "courrier.txt":
              "Texte servi avant la configuration, pour {salutation}.\n\n{signataire}\n",
          },
        },
      });
      expect(wrote.ok()).toBeTruthy();

      // a browser that has never seen the campaign: the fields cannot
      // pre-fill — « Campagne non configurée » stands — and the TEXTS land
      await page.goto(`${ORIGIN}/navigateur/`);
      await expect(page.getByText(/mis à jour depuis son site/)).toBeVisible({
        timeout: 20_000,
      });
      await expect(page.getByText("Campagne non configurée")).toBeVisible();

      // the campaign goes back as this test found it
      const restoredFields = await request.post(`${ORIGIN}/api/campaign`, {
        data: { campaign: fields },
      });
      expect(restoredFields.ok()).toBeTruthy();
      const restoredTpl = await request.post(
        `${ORIGIN}/api/campaign/templates`,
        { data: { templates: {} } },
      );
      expect(restoredTpl.ok()).toBeTruthy();
    });
  });
