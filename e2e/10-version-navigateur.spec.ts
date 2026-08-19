import { expect, test } from "@playwright/test";

import {
  API_ORIGIN,
  COORDINATION,
  campaignOrigin,
  FIRST_CAMPAIGN,
} from "./config.ts";
import { signIn } from "./helpers.ts";

// The door out of the team version, on the campaign's own address.
//
// A visitor with no account lands on the sign-in screen; from there one link
// opens the account-less version WITH this campaign already proposed. Two
// things have to hold at once for that to be true, and only a real browser
// on the real binary can show both:
//
//   - the second build, served by this very instance under /navigateur/,
//     falls into BROWSER mode — no marker, and its /api paths answer HTML;
//   - `?org=` resolves, which it can only do because the instance injects
//     its own domain into that page at startup. The build is compiled with
//     none (global-setup passes an empty PARAPHE_BASE_DOMAIN, exactly as the
//     published image is), so a green journey here is a statement about the
//     injection and nothing else.
//
// The campaign was filled in by 02-campaign.spec.ts; a campaign still at its
// template values pre-fills nothing, deliberately (409).

const ORIGIN = campaignOrigin(FIRST_CAMPAIGN);
const CANDIDATE = "Camille Durand";

test.describe
  .serial("the account-less version, offered by the campaign", () => {
    test("the sign-in screen leads to it, carrying the campaign", async ({
      page,
    }) => {
      await page.goto(`${ORIGIN}/`);

      const open = page.getByRole("link", {
        name: "Ouvrir la version navigateur",
      });
      await expect(open).toBeVisible();
      // the parameter is the whole point: without it the volunteer arrives
      // on an empty configuration and retypes the campaign by hand
      await expect(open).toHaveAttribute(
        "href",
        `/navigateur/?org=${FIRST_CAMPAIGN}`,
      );
      // and the screen says what it costs, on the same card
      await expect(page.getByText("rien n'est coordonné")).toBeVisible();

      await open.click();
      await expect(page).toHaveURL(
        `${ORIGIN}/navigateur/?org=${FIRST_CAMPAIGN}`,
      );

      // BROWSER mode, on the origin that serves an API. The marker is what
      // decides it, and its absence here is the whole feature.
      await expect(
        page.getByText("aucune donnée ne quitte ce navigateur"),
      ).toBeVisible();
      await expect(page.locator('meta[name="paraphe-mode"]')).toHaveCount(0);
    });

    // NO LINK, NO OFFER, NO CLICKS. Opened at a campaign's own address,
    // this build is that campaign's account-less version: whoever lands
    // there wants its texts, and asking them to accept the texts of the
    // site they are standing on is a question with one answer. It read, to
    // the person who met it, as a tool that had failed to substitute
    // anything — the example values are written to look exactly like
    // placeholders.
    test("a campaign's own version fills itself in", async ({ page }) => {
      // Depends on 02-campaign.spec.ts having filled the campaign in, like
      // everything else in this file: run alone, this spec meets a campaign
      // at its template values, which pre-fills nothing on purpose (409).
      await page.goto(`${ORIGIN}/navigateur/`);

      // the campaign's own texts, with nothing clicked
      await expect(page.getByText(/repris depuis son site/)).toBeVisible({
        timeout: 20_000,
      });
      await expect(page.getByText("Campagne non configurée")).toHaveCount(0);
      await expect(page.getByText("Ce lien propose une campagne")).toHaveCount(
        0,
      );

      // and the proof is the message: written from the campaign that came
      // across and from the mayor's own row, with no placeholder left
      await expect(page.locator("table button.lien").first()).toBeVisible({
        timeout: 20_000,
      });
      await page.locator("table button.lien").first().click();
      const body = await page.getByLabel("Message").inputValue();
      expect(body).toContain(CANDIDATE);
      expect(body).not.toMatch(/\{[^}]+\}/);
    });

    // The apex serves no campaign, so there is none to be: a static
    // publication is in the same position, and neither must adopt anything.
    test("the apex adopts nothing", async ({ page }) => {
      await page.goto(`${API_ORIGIN}/navigateur/`);
      await expect(page.getByText("Campagne non configurée")).toBeVisible({
        timeout: 20_000,
      });
      await expect(page.getByText(/repris depuis son site/)).toHaveCount(0);
    });

    // THE LINK FROM THE SIGN-IN SCREEN, which names this very campaign.
    //
    // It used to open an offer: « Voir cette proposition », then « Reprendre
    // cette campagne ». Two clicks to accept the texts of the site the
    // visitor is standing on — a question with one answer, and read by the
    // person who met it as a tool that had substituted nothing. The link now
    // lands on a version already filled in.
    //
    // The OFFER is not gone; it moved to the case it was written for, which
    // this instance cannot reach: a `?org=` naming ANOTHER campaign is a
    // cross-origin fetch, and `connect-src 'self'` refuses it here by
    // design. Its screen — the values shown before anything is written,
    // accepting, refusing, and the parameter leaving the address bar — is
    // driven in web/src/Browser.test.tsx, where a static publication can be
    // modelled without an instance.
    test("the link naming this campaign needs no clicks at all", async ({
      page,
    }) => {
      await page.goto(`${ORIGIN}/navigateur/?org=${FIRST_CAMPAIGN}`);

      await expect(page.getByText(/repris depuis son site/)).toBeVisible({
        timeout: 20_000,
      });
      await expect(page.getByText("Ce lien propose une campagne")).toHaveCount(
        0,
      );
      await expect(page.getByText("Campagne non configurée")).toHaveCount(0);

      // The proof is the message itself: written from the campaign that came
      // across and from the mayor's own row, with no placeholder left.
      await expect(page.locator("table button.lien").first()).toBeVisible({
        timeout: 20_000,
      });
      await page.locator("table button.lien").first().click();
      const body = await page.getByLabel("Message").inputValue();
      expect(body).toContain(CANDIDATE);
      expect(body).not.toMatch(/\{[^}]+\}/);
    });

    test("a signed-in volunteer still finds it, in the footer", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      const away = page.getByRole("link", {
        name: "Travailler sans compte, dans mon navigateur",
      });
      await expect(away).toHaveAttribute(
        "href",
        `/navigateur/?org=${FIRST_CAMPAIGN}`,
      );
    });
  });
