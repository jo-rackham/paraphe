import AxeBuilder from "@axe-core/playwright";
import { expect, type Page, test } from "@playwright/test";

import {
  API_ORIGIN,
  COORDINATION,
  campaignOrigin,
  FIRST_CAMPAIGN,
  INSTANCE_ADMIN,
  STATIC_ORIGIN,
} from "./config.ts";
import { openTab, signIn } from "./helpers.ts";

// Every screen of the three modes, scanned by axe against WCAG A + AA.
//
// This is the only automated accessibility check that can exist: contrast
// is a property of RENDERED colours, which jsdom never computes, and the
// ARIA tree is a property of the real browser. A violation here is a
// regression a volunteer with a screen reader — or a mayor's secretary on
// an old laptop, zoomed to 200 % — pays for in silence.

const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];

/** Fails with one readable line per violation, not a JSON dump. */
async function checkA11y(page: Page, screen: string) {
  const { violations } = await new AxeBuilder({ page })
    .withTags(TAGS)
    .analyze();
  const readable = violations.map(
    (v) =>
      `${screen} — ${v.id} (${v.impact}): ${v.help}\n` +
      v.nodes.map((n) => `    ${n.target.join(" ")}`).join("\n"),
  );
  expect(readable).toEqual([]);

  // Axe does not enforce this one: an interactive control inside a live
  // region is re-announced on every mutation (status implies aria-atomic),
  // so the project's doctrine is text-only regions, controls beside them.
  const interactive = await page
    .locator(
      '[role="alert"] button, [role="alert"] a, ' +
        '[role="status"] button, [role="status"] a',
    )
    .count();
  expect(interactive, `${screen}: interactive control in a live region`).toBe(
    0,
  );
}

/** WCAG relative luminance of a `#rrggbb` colour. */
function luminance(hex: string): number {
  const lin = (c: number) =>
    c / 255 <= 0.04045 ? c / 255 / 12.92 : ((c / 255 + 0.055) / 1.055) ** 2.4;
  const n = Number.parseInt(hex.slice(1), 16);
  return (
    0.2126 * lin(n >> 16) + 0.7152 * lin((n >> 8) & 255) + 0.0722 * lin(n & 255)
  );
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

/** Browser mode's list, loaded — the state every journey starts from. */
async function openBrowserList(page: Page) {
  await page.goto(`${STATIC_ORIGIN}/`);
  await expect(page.locator("table button.lien").first()).toBeVisible({
    timeout: 20_000,
  });
}

test.describe
  .serial("accessibility", () => {
    test("browser mode: list, card, guide, data, campaign", async ({
      page,
    }) => {
      await openBrowserList(page);
      await checkA11y(page, "browser:liste");

      await page.locator("table button.lien").first().click();
      await expect(page.getByText("Pourquoi cette personne")).toBeVisible();
      await checkA11y(page, "browser:fiche");

      await openTab(page, "Guide");
      await checkA11y(page, "browser:guide");

      await openTab(page, "Mes données");
      await checkA11y(page, "browser:donnees");

      await openTab(page, "Ma campagne");
      await checkA11y(page, "browser:campagne");
    });

    test("keyboard: skip link first, focus follows the view", async ({
      page,
    }) => {
      await openBrowserList(page);

      // the first Tab reaches the skip link, and Enter lands in the content
      await page.keyboard.press("Tab");
      await expect(
        page.getByRole("link", { name: "Aller au contenu" }),
      ).toBeFocused();
      await page.keyboard.press("Enter");
      await expect(page.locator("main")).toBeFocused();

      // the active tab is stated, not only coloured
      await expect(
        page.getByRole("button", { name: "Les maires" }),
      ).toHaveAttribute("aria-current", "page");

      // opening a card unmounts the clicked button: focus must land on the
      // new view's title, not fall back to the top of the document
      await page.locator("table button.lien").first().click();
      await expect(page.locator("main h1")).toBeFocused();
      await expect(page).toHaveTitle(/ — paraphe$/);
    });

    test("non-text contrast the scanner cannot see", async ({ page }) => {
      // Axe checks text only. The gauge fill against its track and the
      // field border against the field ARE the information (WCAG 1.4.11,
      // 3:1) — read the palette variables and hold the floor, both schemes.
      await openBrowserList(page);
      for (const scheme of ["light", "dark"] as const) {
        await page.emulateMedia({ colorScheme: scheme });
        const vars = await page.evaluate(() => {
          const s = getComputedStyle(document.documentElement);
          const v = (name: string) => s.getPropertyValue(name).trim();
          return {
            jaune: v("--jaune"),
            piste: v("--piste"),
            champ: v("--champ"),
            champTrait: v("--champ-trait"),
          };
        });
        expect(
          contrast(vars.jaune, vars.piste),
          `${scheme}: gauge fill vs track`,
        ).toBeGreaterThanOrEqual(3);
        expect(
          contrast(vars.champTrait, vars.champ),
          `${scheme}: field border vs field`,
        ).toBeGreaterThanOrEqual(3);
      }
    });

    test.describe(() => {
      test.use({ colorScheme: "dark" });
      test("browser mode in dark: the hand-defined palette holds", async ({
        page,
      }) => {
        await openBrowserList(page);
        await checkA11y(page, "browser:liste (sombre)");

        await page.locator("table button.lien").first().click();
        await expect(page.getByText("Pourquoi cette personne")).toBeVisible();
        await checkA11y(page, "browser:fiche (sombre)");
      });
    });

    test("team mode: sign-in, guide, dashboard, list, card, team, profile", async ({
      page,
    }) => {
      const origin = campaignOrigin(FIRST_CAMPAIGN);
      await page.goto(`${origin}/`);
      await expect(
        page.getByRole("heading", { name: "Connexion" }),
      ).toBeVisible();
      await checkA11y(page, "team:connexion");

      await signIn(page, origin, COORDINATION.email, COORDINATION.password);
      await checkA11y(page, "team:guide");

      await openTab(page, "Mon tableau");
      await expect(
        page.getByRole("heading", { name: "Mon tableau de bord" }),
      ).toBeVisible();
      await checkA11y(page, "team:tableau");

      await openTab(page, "Les maires");
      await expect(page.locator("table button.lien").first()).toBeVisible();
      await checkA11y(page, "team:maires");

      await page.locator("table button.lien").first().click();
      await expect(page.getByText("Pourquoi cette personne")).toBeVisible();
      await checkA11y(page, "team:fiche");

      await openTab(page, "Mon équipe");
      await expect(
        page.getByRole("heading", { name: "Mon équipe" }),
      ).toBeVisible();
      await checkA11y(page, "team:equipe");

      await openTab(page, "Mon profil");
      await expect(
        page.getByRole("heading", { name: "Mon profil" }),
      ).toBeVisible();
      await checkA11y(page, "team:profil");
    });

    test("instance apex: hosting request and moderation", async ({ page }) => {
      await page.goto(`${API_ORIGIN}/`);
      await expect(
        page.getByRole("heading", { name: "Héberger une campagne" }),
      ).toBeVisible();
      await checkA11y(page, "instance:accueil");

      await page.getByRole("button", { name: "Se connecter" }).click();
      await expect(
        page.getByRole("heading", { name: "Administration de l'instance" }),
      ).toBeVisible();
      await checkA11y(page, "instance:connexion");

      await page.getByLabel("Adresse email").fill(INSTANCE_ADMIN.email);
      await page.getByLabel("Mot de passe").fill(INSTANCE_ADMIN.password);
      await page.getByRole("button", { name: "Se connecter" }).click();
      await expect(
        page.getByRole("heading", { name: "Demandes d'hébergement" }),
      ).toBeVisible();
      await checkA11y(page, "instance:moderation");
    });
  });
