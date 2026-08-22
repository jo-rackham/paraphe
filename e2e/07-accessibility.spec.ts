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
import { openManagement, openTab, signIn } from "./helpers.ts";

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
  // The check's DATA rides in the message (fg/bg/ratio for a contrast
  // finding): a bare selector sent one hunt through computed styles that
  // were already correct again by the time anyone looked — the measured
  // colours are what named a 150 ms transition racing the scan.
  const readable = violations.map(
    (v) =>
      `${screen} — ${v.id} (${v.impact}): ${v.help}\n` +
      v.nodes
        .map(
          (n) =>
            `    ${n.target.join(" ")} :: ${JSON.stringify(n.any[0]?.data)}`,
        )
        .join("\n"),
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

/** WCAG relative luminance of a `#rrggbb` or computed `rgb(r, g, b)`
 *  colour — the palette resolves through a real property now
 *  (light-dark()), and computed colours come back in rgb() form. */
function luminance(colour: string): number {
  const lin = (c: number) =>
    c / 255 <= 0.04045 ? c / 255 / 12.92 : ((c / 255 + 0.055) / 1.055) ** 2.4;
  const rgb = colour.match(/^rgba?\((\d+),\s*(\d+),\s*(\d+)/);
  const [r, g, b] = rgb
    ? [Number(rgb[1]), Number(rgb[2]), Number(rgb[3])]
    : (() => {
        const n = Number.parseInt(colour.slice(1), 16);
        return [n >> 16, (n >> 8) & 255, n & 255];
      })();
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
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

      // A HISTORY, and one of its lines open for correction. A fresh browser
      // holds no note, so the card scanned above carries none — and the
      // controls a history brings (two buttons per line, a confirmation that
      // replaces one of them, a textarea that replaces the line) are then
      // scanned nowhere at all.
      await page.getByLabel("Statut").selectOption({ label: "Email envoyé" });
      await page
        .getByRole("textbox", { name: "Note", exact: true })
        .fill("noté pour le scan");
      await page.locator(".barre-statut").getByRole("button").click();
      await expect(page.getByText("noté pour le scan")).toBeVisible();
      await checkA11y(page, "browser:fiche avec historique");

      await page.getByRole("button", { name: "Modifier la note 1 du" }).click();
      await expect(page.getByLabel("Texte de la note")).toBeVisible();
      await checkA11y(page, "browser:fiche, note en correction");

      await page.getByRole("button", { name: "Annuler" }).click();
      await page
        .getByRole("button", { name: "Supprimer la note 1 du" })
        .click();
      await expect(page.getByText("Supprimer cette note ?")).toBeVisible();
      await checkA11y(page, "browser:fiche, suppression confirmée");
      await page.getByRole("button", { name: "Annuler" }).click();

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
        page.getByRole("link", { name: "Les maires" }),
      ).toHaveAttribute("aria-current", "page");

      // opening a card unmounts the clicked button: focus must land on the
      // new view's title, not fall back to the top of the document
      await page.locator("table button.lien").first().click();
      await expect(page.locator("main h1")).toBeFocused();
      await expect(page).toHaveTitle(/ — paraphe$/);

      // the result counter is ONE node: a visible line plus an sr-only
      // mirror was read twice, and the two disagreed while the mirror
      // lagged behind its debounce
      await page.getByRole("button", { name: "retour à la liste" }).click();
      await expect(page.getByText(/affiché\(s\) sur/)).toHaveCount(1);
    });

    test("non-text contrast the scanner cannot see", async ({ page }) => {
      // Axe checks text only. The gauge fill against its track and the
      // field border against the field ARE the information (WCAG 1.4.11,
      // 3:1) — read the palette variables and hold the floor, both schemes.
      await openBrowserList(page);
      for (const scheme of ["light", "dark"] as const) {
        await page.emulateMedia({ colorScheme: scheme });
        const vars = await page.evaluate(() => {
          // a custom property read raw comes back UNRESOLVED — for the
          // light-dark() palette that is the function text, not a colour.
          // Resolution happens when the value lands in a real property:
          // the probe does exactly that, per variable, in the live scheme.
          const probe = document.createElement("div");
          document.body.appendChild(probe);
          const v = (name: string) => {
            probe.style.color = `var(${name})`;
            return getComputedStyle(probe).color;
          };
          const out = {
            jaune: v("--jaune"),
            piste: v("--piste"),
            champ: v("--champ"),
            champTrait: v("--champ-trait"),
            alerteFond: v("--alerte-fond"),
            alerteTrait: v("--alerte-trait"),
            alerteErreurTrait: v("--alerte-erreur-trait"),
          };
          probe.remove();
          return out;
        });
        expect(
          contrast(vars.jaune, vars.piste),
          `${scheme}: gauge fill vs track`,
        ).toBeGreaterThanOrEqual(3);
        expect(
          contrast(vars.champTrait, vars.champ),
          `${scheme}: field border vs field`,
        ).toBeGreaterThanOrEqual(3);
        // the alert box border is what says "warning" before the text is
        // read — it went unlisted in round one and read 1.5:1
        expect(
          contrast(vars.alerteTrait, vars.alerteFond),
          `${scheme}: alert border vs alert background`,
        ).toBeGreaterThanOrEqual(3);
        expect(
          contrast(vars.alerteErreurTrait, vars.alerteFond),
          `${scheme}: error bar vs alert background`,
        ).toBeGreaterThanOrEqual(3);
      }
    });

    test("phone width: the list's card layout keeps its semantics", async ({
      page,
    }) => {
      // under 640 px the mayor rows lay out as grid cards, and a changed
      // display is exactly what strips implicit table roles — scan there
      await page.setViewportSize({ width: 375, height: 812 });
      await openBrowserList(page);
      await checkA11y(page, "browser:liste (375 px)");
    });

    test.describe(() => {
      // reducedMotion re-affirmed BESIDE the scheme: the style sampling
      // that diagnosed the button morph saw live 150 ms transitions in this
      // describe while the config-level reduce should have stilled them —
      // whatever the merge rule, stating both here costs nothing
      test.use({ colorScheme: "dark", reducedMotion: "reduce" });
      test("browser mode in dark: the hand-defined palette holds", async ({
        page,
      }) => {
        await openBrowserList(page);
        await checkA11y(page, "browser:liste (sombre)");

        await page.locator("table button.lien").first().click();
        await expect(page.getByText("Pourquoi cette personne")).toBeVisible();
        await checkA11y(page, "browser:fiche (sombre)");

        // and the history's own controls in dark too: `--focus` and the
        // muted-row rule are the two the palette gets wrong one theme at a
        // time
        await page.getByLabel("Statut").selectOption({ label: "Email envoyé" });
        await page
          .getByRole("textbox", { name: "Note", exact: true })
          .fill("noté pour le scan");
        await page.locator(".barre-statut").getByRole("button").click();
        await expect(page.getByText("noté pour le scan")).toBeVisible();
        await page
          .getByRole("button", { name: "Modifier la note 1 du" })
          .click();
        await expect(page.getByLabel("Texte de la note")).toBeVisible();
        await checkA11y(page, "browser:fiche, note en correction (sombre)");
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

      // the public team-request form: a disclosure on this very screen, so
      // the scan above would never reach it — and it is the one screen a
      // person with no account at all fills in
      await page
        .getByRole("button", { name: "Demander à créer une équipe" })
        .click();
      await expect(
        page.getByRole("heading", { name: "Demander à créer une équipe" }),
      ).toBeVisible();
      await checkA11y(page, "team:demande-equipe");
      await page
        .getByRole("button", { name: "Masquer la demande d'équipe" })
        .click();
      // The screen CARRYING ITS ANSWER, not merely the empty form: the
      // message lands in a live region, beside two buttons that go
      // aria-disabled and change one label — none of which the empty form
      // shows.
      await page.getByLabel("Adresse email").fill(COORDINATION.email);
      await page
        .getByRole("button", { name: "Recevoir un lien par email" })
        .click();
      await expect(
        page.getByText(/Si un compte existe à cette adresse/),
      ).toBeVisible();
      await checkA11y(page, "team:connexion (lien demandé)");

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

      await openManagement(page);
      await expect(
        page.getByRole("heading", { name: "Ma campagne" }),
      ).toBeVisible();
      await checkA11y(page, "team:equipe");

      // The card that appears after an access is opened: a live region, a
      // password in a large typeface, an invitation outcome, and a button
      // that unmounts itself. It exists only after a successful write, so
      // the scan above never reached it.
      await page.getByLabel("Nom", { exact: true }).fill("Bénévole A11y");
      await page
        .getByLabel("Adresse email", { exact: true })
        .fill("a11y@premiere.test");
      await page.getByRole("button", { name: "Créer", exact: true }).click();
      await expect(page.getByText(/Mot de passe provisoire/)).toBeVisible();
      await checkA11y(page, "team:equipe (accès ouvert)");

      await openTab(page, "Mon profil");
      await expect(
        page.getByRole("heading", { name: "Mon profil" }),
      ).toBeVisible();
      await checkA11y(page, "team:profil");
    });

    test("instance apex: hosting request and moderation", async ({ page }) => {
      await page.goto(`${API_ORIGIN}/`);
      await expect(
        page.getByRole("heading", {
          name: "Chercher 500 parrainages, méthodiquement",
        }),
      ).toBeVisible();
      await checkA11y(page, "instance:accueil");

      // the hosting form, on its own view
      await page
        .getByRole("button", { name: "Héberger une campagne" })
        .first()
        .click();
      await expect(
        page.getByRole("heading", { name: "Héberger une campagne" }),
      ).toBeVisible();
      await checkA11y(page, "instance:demande");
      await page.getByRole("button", { name: "Retour à l'accueil" }).click();

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

    // The palette is hand-defined in both schemes, so both are scanned in
    // all THREE modes — browser mode alone left the two screens a volunteer
    // spends the campaign on unscanned in the dark.
    test.describe(() => {
      // reducedMotion re-affirmed BESIDE the scheme: the style sampling
      // that diagnosed the button morph saw live 150 ms transitions in this
      // describe while the config-level reduce should have stilled them —
      // whatever the merge rule, stating both here costs nothing
      test.use({ colorScheme: "dark", reducedMotion: "reduce" });

      test("team mode in dark", async ({ page }) => {
        const origin = campaignOrigin(FIRST_CAMPAIGN);
        await page.goto(`${origin}/`);
        await expect(
          page.getByRole("heading", { name: "Connexion" }),
        ).toBeVisible();
        await checkA11y(page, "team:connexion (sombre)");

        await signIn(page, origin, COORDINATION.email, COORDINATION.password);
        await openTab(page, "Mon tableau");
        await expect(
          page.getByRole("heading", { name: "Mon tableau de bord" }),
        ).toBeVisible();
        await checkA11y(page, "team:tableau (sombre)");

        await openTab(page, "Les maires");
        await expect(page.locator("table button.lien").first()).toBeVisible();
        await checkA11y(page, "team:maires (sombre)");

        await page.locator("table button.lien").first().click();
        await expect(page.getByText("Pourquoi cette personne")).toBeVisible();
        await checkA11y(page, "team:fiche (sombre)");

        await openManagement(page);
        await expect(
          page.getByRole("heading", { name: "Ma campagne" }),
        ).toBeVisible();
        await checkA11y(page, "team:equipe (sombre)");
      });

      test("instance apex in dark", async ({ page }) => {
        await page.goto(`${API_ORIGIN}/`);
        await expect(
          page.getByRole("heading", {
            name: "Chercher 500 parrainages, méthodiquement",
          }),
        ).toBeVisible();
        await checkA11y(page, "instance:accueil (sombre)");
      });
    });

    // The healthy path is the one that gets scanned, and the alert that
    // only appears when something breaks is the one carrying a button
    // inside a live region — the shape the doctrine forbids. Reached here
    // by breaking the paginated read on purpose.
    test("team mode: the interrupted-loading alert", async ({ page }) => {
      const origin = campaignOrigin(FIRST_CAMPAIGN);
      await page.goto(`${origin}/`);
      await signIn(page, origin, COORDINATION.email, COORDINATION.password);
      await openTab(page, "Les maires");
      await expect(page.locator("table button.lien").first()).toBeVisible();

      // only the CONTINUATION fails: the first page must have rendered, or
      // the alert is not the one under test
      await page.route(/\/api\/mayors\?.*after=/, (route) => route.abort());
      await page.mouse.wheel(0, 40_000);
      const alerte = page.getByText("Chargement interrompu.");
      await expect(alerte).toBeVisible({ timeout: 15_000 });
      await expect(
        page.getByRole("button", { name: "Réessayer" }),
      ).toBeVisible();
      await checkA11y(page, "team:maires (chargement interrompu)");
    });
  });
