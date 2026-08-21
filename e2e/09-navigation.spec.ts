import { expect, test } from "@playwright/test";

import { COORDINATION, campaignOrigin, FIRST_CAMPAIGN } from "./config.ts";
import { openTab, signIn } from "./helpers.ts";

// « PRÉCÉDENT » — the gesture a volunteer makes without thinking, on a phone,
// in the middle of a card.
//
// It used to leave the application: the whole interface was one URL, so the
// browser's own back button walked out of the site, taking a rewritten email
// and a half-typed call note with it. Only a real browser can prove this —
// unit tests can dispatch a popstate, they cannot press the button.

const ORIGIN = campaignOrigin(FIRST_CAMPAIGN);
const NOTE = "Rappeler après le conseil municipal de jeudi.";

test.describe
  .serial("browser navigation", () => {
    test("the address bar names the screen, and précédent walks the app", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      // the home is the base itself: one address per screen, and the guide
      // is what the base shows
      await expect(page).toHaveURL(/\/$/);

      await openTab(page, "Les maires");
      await expect(page).toHaveURL(/\/maires$/);
      await openTab(page, "Mon tableau");
      await expect(page).toHaveURL(/\/tableau$/);

      await page.goBack();
      await expect(
        page.getByRole("heading", { name: "Les maires" }),
      ).toBeVisible();
      await expect(page).toHaveURL(/\/maires$/);

      await page.goForward();
      await expect(page).toHaveURL(/\/tableau$/);
    });

    test("a card has an address, and précédent from it keeps the note", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openTab(page, "Les maires");
      // a row opens by its commune: that is the button the list gives
      const first = page.locator("tbody tr").first().locator("button").first();
      await first.click();

      // the card's own address, which is what makes it shareable
      await expect(page).toHaveURL(/\/maires\/\d[\dAB]\d{3}$/);
      const cardUrl = page.url();

      // Work in progress: the thing the old behaviour threw away.
      const note = page.getByLabel(/note/i).first();
      await note.fill(NOTE);

      await page.goBack();
      await expect(
        page.getByRole("heading", { name: "Les maires" }),
      ).toBeVisible();

      await page.goForward();
      await expect(page).toHaveURL(cardUrl);
      await expect(
        page.getByLabel(/note/i).first(),
        "the note was lost crossing a history move: the draft is kept by a " +
          "ref on the screen above the card, and this is what says so",
      ).toHaveValue(NOTE);
    });

    test("a card link opens that card for whoever receives it", async ({
      page,
      context,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openTab(page, "Les maires");
      const row = page.locator("tbody tr").first().locator("button").first();
      const commune = (await row.innerText()).trim();
      await row.click();
      await expect(page).toHaveURL(/\/maires\/\d[\dAB]\d{3}$/);
      const shared = page.url();

      // a second tab, the session shared through the context's cookie jar:
      // exactly what a colleague does with a pasted link
      const other = await context.newPage();
      await other.goto(shared);
      await expect(other.getByText(commune).first()).toBeVisible();
      await other.close();
    });

    // The nav tabs are LINKS carrying each view's address: a plain click
    // stays a view change with no reload, and a modified click is the
    // browser's — which only a real browser can prove, because opening a
    // tab is not an event a unit test can observe. Driven on the
    // account-less build: the nav is the same component in every mode, and
    // this way the journey spends nothing from the shared sign-in budget.
    test("ctrl+clic opens a tab in a new one, plain click stays here", async ({
      page,
      context,
    }) => {
      await page.goto(`${ORIGIN}/navigateur/`);
      await expect(
        page.getByRole("link", { name: "Guide", exact: true }),
      ).toBeVisible({ timeout: 20_000 });
      const opened = context.waitForEvent("page");
      await page
        .getByRole("link", { name: "Guide", exact: true })
        .click({ modifiers: ["ControlOrMeta"] });
      const other = await opened;
      await expect(other).toHaveURL(/\/navigateur\/guide$/);
      await other.close();
      // …and the first tab never moved: the modified click was the
      // browser's alone
      await expect(page).toHaveURL(/\/navigateur\/$/);
    });

    test("an address nobody serves lands on a screen, never on a blank page", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      // The server answers index.html for any extension-less path, and the
      // interface decides: an unknown view is the home, not an empty tree.
      await page.goto(`${ORIGIN}/vue-qui-nexiste-pas`);
      await expect(page.getByRole("heading", { name: "Guide" })).toBeVisible();
    });
  });
