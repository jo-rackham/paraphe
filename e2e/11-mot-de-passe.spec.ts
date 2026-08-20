import { expect, test } from "@playwright/test";

import { COORDINATION, campaignOrigin, FIRST_CAMPAIGN } from "./config.ts";
import { openManagement, openTab, signIn } from "./helpers.ts";

// Choosing one's own password, and drawing one for somebody who lost theirs.
//
// Kept to what only a real browser can show, and no page load more: every
// visit spends from a per-source ceiling the whole suite shares — one
// loopback address for sixty-odd journeys — and a spec that signs in
// generously is one that makes its NEIGHBOURS fail. What is proved here:
//
//   - the change through the interface, and the session that made it living
//     on — an old password refused afterwards is proved in Go, where it
//     costs no page load;
//   - the OTHER browser losing the account, which is the point of the
//     feature and cannot be asserted from a handler;
//   - a manager drawing a password for somebody who lost theirs.
//
// The rest — the floor, the refusals, the reset's team and role filters —
// is in api/password_change_test.go, where it costs no page load.

const ORIGIN = campaignOrigin(FIRST_CAMPAIGN);
const CHOSEN = "colline-verger-tilleul-42";
const OWNER = { email: "mot-de-passe@premiere.test", name: "Perrine Passe" };

async function changePassword(
  page: import("@playwright/test").Page,
  current: string,
  next: string,
) {
  await openTab(page, "Mon profil");
  await page.getByLabel("Mot de passe actuel").fill(current);
  await page.getByLabel("Nouveau mot de passe", { exact: true }).fill(next);
  await page.getByLabel("Répétez le nouveau mot de passe").fill(next);
  await page.getByRole("button", { name: "Changer mon mot de passe" }).click();
  await expect(page.getByText(/Vos autres sessions/)).toBeVisible();
}

test.describe
  .serial("a password its owner chooses", () => {
    let given = "";

    test("its owner replaces the one they were given", async ({ page }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openManagement(page);
      await page.getByLabel("Nom", { exact: true }).fill(OWNER.name);
      await page.getByLabel("Adresse email", { exact: true }).fill(OWNER.email);
      await page.getByRole("button", { name: "Créer", exact: true }).click();
      const shown = page.locator(".carte.alerte .grand-tel");
      await expect(shown).toBeVisible();
      given = (await shown.innerText()).trim();

      // …and the person it was opened for uses it, then replaces it. The
      // sign-out is not decoration: `signIn` starts from the sign-in screen,
      // and a page still holding the coordination's session shows the
      // application instead.
      await page.getByRole("button", { name: "déconnexion" }).click();
      await signIn(page, ORIGIN, OWNER.email, given);
      await changePassword(page, given, CHOSEN);

      // the session that made the change SURVIVES: signing somebody out of
      // their own session is how they conclude it failed and try again
      await openTab(page, "Les maires");
      await expect(page.locator("table button.lien").first()).toBeVisible();
    });

    // The reason this feature is not just a form. Two browsers hold the same
    // account; one changes the password and the OTHER is not that account any
    // more — which is exactly what somebody who thinks their password leaked
    // is asking for.
    test("the other browser loses the account", async ({ page, browser }) => {
      const other = await browser.newContext();
      const theirs = await other.newPage();
      try {
        await signIn(theirs, ORIGIN, OWNER.email, CHOSEN);

        // PAST THE SECOND BOUNDARY, deliberately. The instant of the change
        // is stored truncated to the second and a token carries Unix
        // seconds, so a session opened inside that same second is not
        // « before » the change and survives — the grace that keeps the
        // caller's OWN fresh cookie alive, and that costs an attacker
        // nothing they can aim at. Without this wait the journey passes or
        // fails on where the two sign-ins happen to land.
        await theirs.waitForTimeout(1200);

        await signIn(page, ORIGIN, OWNER.email, CHOSEN);
        await changePassword(page, CHOSEN, `${CHOSEN}-encore`);

        // The other browser ACTS, and is told the session is over.
        //
        // « Les maires » and not « Mon profil »: the second is a tab switch
        // and nothing else — the screen renders from what the page already
        // holds and no request leaves, so the browser learns nothing. There
        // is no push channel here, so a revoked session falls at its NEXT
        // REQUEST. A tab left untouched keeps showing a screen it can no
        // longer write from, and says so the moment it tries.
        await theirs.getByRole("button", { name: "Les maires" }).click();
        await expect(
          theirs.getByRole("heading", { name: "Connexion" }),
        ).toBeVisible({ timeout: 15_000 });
        // …and it is told WHY, in the server's own words rather than the
        // generic « votre session a expiré »: this session did not run out,
        // it was ended, and somebody watching a screen empty itself deserves
        // the difference.
        await expect(
          theirs.getByText("Le mot de passe de ce compte a changé."),
        ).toBeVisible();
      } finally {
        await other.close();
      }
    });

    // The door the sign-in screen has always promised — « s'il est perdu, il
    // faut en regénérer un » — and that no button offered until now.
    test("a manager draws a new one for somebody who lost theirs", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openManagement(page);

      await page
        .locator("tr", { hasText: OWNER.email })
        .getByRole("button", { name: "nouveau mot de passe" })
        .click();

      const card = page.locator(".carte.alerte", {
        hasText: "Nouveau mot de passe pour",
      });
      await expect(card).toBeVisible();
      const drawn = (await card.locator(".grand-tel").innerText()).trim();
      expect(drawn.length).toBeGreaterThan(10);
      // what the act DID, said on the card: whoever held the old one is out
      await expect(card).toContainText("sessions ouvertes avec l'ancien");

      // the drawn password is what opens the account now — which is the
      // whole of what a manager promises when they read it out
      await page.getByRole("button", { name: "déconnexion" }).click();
      await signIn(page, ORIGIN, OWNER.email, drawn);
    });

    // A typo in the CURRENT password answers 403 and never 401: this
    // interface reads a 401 from an authenticated route as « your session is
    // gone » and throws the volunteer back to the sign-in form — out of a
    // live session, work and all, for one mistyped field. The old password
    // still opening afterwards is proved in Go, where it costs no page load.
    test.describe(() => {
      // its own source: the journeys above live on the suite's shared one,
      // which is spent to the edge — see global-setup, PARAPHE_TRUSTED_PROXIES
      test.use({ extraHTTPHeaders: { "X-Forwarded-For": "192.0.2.11" } });

      test("a wrong current password refuses without ending the session", async ({
        page,
      }) => {
        await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
        await openTab(page, "Mon profil");
        const form = page.locator("form", {
          hasText: "Changer mon mot de passe",
        });
        await form.getByLabel("Mot de passe actuel").fill("pas-le-bon-du-tout");
        await form
          .getByLabel("Nouveau mot de passe", { exact: true })
          .fill(CHOSEN);
        await form.getByLabel("Répétez le nouveau mot de passe").fill(CHOSEN);
        await form
          .getByRole("button", { name: "Changer mon mot de passe" })
          .click();

        // the refusal, in the form's own slot beside the field it answers —
        // and the session LIVES: still signed in, a fresh request still served
        await expect(
          form.getByText("Mot de passe actuel incorrect."),
        ).toBeVisible();
        await expect(
          page.getByRole("button", { name: "déconnexion" }),
        ).toBeVisible();
        await openTab(page, "Les maires");
        await expect(page.locator("table button.lien").first()).toBeVisible();
      });
    });
  });
