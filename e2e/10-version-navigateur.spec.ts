import { expect, test } from "@playwright/test";

import { COORDINATION, campaignOrigin, FIRST_CAMPAIGN } from "./config.ts";
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

    test("it proposes the campaign, and nothing before the volunteer asks", async ({
      page,
    }) => {
      await page.goto(`${ORIGIN}/navigateur/?org=${FIRST_CAMPAIGN}`);

      // The offer is announced, not fetched: a link shared publicly must not
      // ring its instance at every page load.
      await expect(
        page.getByText("Ce lien propose une campagne"),
      ).toBeVisible();
      await expect(page.getByText(CANDIDATE)).toHaveCount(0);

      await page
        .getByRole("button", { name: "Voir cette proposition" })
        .click();

      // What a mayor will read, shown before anything is written.
      await expect(
        page.getByRole("heading", { name: /Reprendre la campagne/ }),
      ).toBeVisible();
      await expect(page.getByText(CANDIDATE).first()).toBeVisible();
    });

    test("accepting it fills the messages this browser will send", async ({
      page,
    }) => {
      await page.goto(`${ORIGIN}/navigateur/?org=${FIRST_CAMPAIGN}`);
      await page
        .getByRole("button", { name: "Voir cette proposition" })
        .click();
      await page
        .getByRole("button", { name: "Reprendre cette campagne" })
        .click();
      await expect(page.getByText(/reprise\./)).toBeVisible();
      // the banner that told the volunteer to send nothing yet is gone
      await expect(page.getByText("Campagne non configurée")).toHaveCount(0);

      // The proof is the message itself: written from the campaign that
      // travelled and from the mayor's own row, with no placeholder left.
      await page.getByRole("button", { name: "Les maires" }).click();
      await expect(page.locator("table button.lien").first()).toBeVisible({
        timeout: 20_000,
      });
      await page.locator("table button.lien").first().click();
      const body = await page.getByLabel("Message").inputValue();
      expect(body).toContain(CANDIDATE);
      expect(body).not.toMatch(/\{[^}]+\}/);
    });

    test("refusing it takes the offer out of the address bar", async ({
      page,
    }) => {
      await page.goto(`${ORIGIN}/navigateur/?org=${FIRST_CAMPAIGN}`);
      await page
        .getByRole("button", { name: "Voir cette proposition" })
        .click();
      await page
        .getByRole("button", { name: "Non, je remplis moi-même" })
        .click();

      // …or every reload would bring back an offer already declined
      await expect(page).toHaveURL(`${ORIGIN}/navigateur/`);
      await page.reload();
      await expect(page.getByText("Ce lien propose une campagne")).toHaveCount(
        0,
      );
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
