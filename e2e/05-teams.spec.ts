import { expect, test } from "@playwright/test";

import { COORDINATION, campaignOrigin, FIRST_CAMPAIGN } from "./config.ts";
import { openTab, signIn } from "./helpers.ts";

// Local teams: a coordination draws a geographic perimeter, a lead opens
// volunteer accesses inside it, and the walls hold — a team neither draws
// nor reads outside its departments, and a closed access stays closed.
//
// The perimeter is enforced server-side (team_id on assignments, checked in
// every route), so each wall is probed the way it would actually be hit:
// through the interface first, then straight at the API.

const ORIGIN = campaignOrigin(FIRST_CAMPAIGN);
const TEAM = "Équipe Aveyron";
const LEAD = { email: "referente@premiere.test", name: "Renée Référente" };
const VOLUNTEER = { email: "benevole@premiere.test", name: "Bastien Bénévole" };

test.describe
  .serial("local teams", () => {
    let leadPassword = "";
    let volunteerPassword = "";

    test("coordination draws a team and its perimeter", async ({ page }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openTab(page, "Mon équipe");

      await page.getByLabel("Nom de l'équipe").fill(TEAM);
      await page
        .getByLabel("Départements (plusieurs possibles)")
        .selectOption("Aveyron");
      await page.getByRole("button", { name: "Créer l'équipe" }).click();

      const row = page.locator("tr", { hasText: TEAM });
      await expect(row).toContainText("Aveyron");
    });

    test("coordination opens a lead access inside that team", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openTab(page, "Mon équipe");

      await page.getByLabel("Nom", { exact: true }).fill(LEAD.name);
      await page.getByLabel("Adresse email").fill(LEAD.email);
      await page.getByLabel("Rôle").selectOption({ label: "Référent" });
      // getByLabel cannot be exact here: a label wrapping a <select> counts
      // the options' text in its own, so only the ARIA name is "Équipe"
      await page
        .getByRole("combobox", { name: "Équipe", exact: true })
        .selectOption({ label: TEAM });
      await page.getByRole("button", { name: "Créer", exact: true }).click();

      // the password is shown once, to be passed on by voice — never stored
      await expect(page.getByText("affiché une seule fois")).toBeVisible();
      leadPassword = (await page.locator(".grand-tel").innerText()).trim();
      expect(leadPassword.length).toBeGreaterThan(8);
    });

    test("the lead opens volunteer accesses, without choosing roles", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, LEAD.email, leadPassword);
      await openTab(page, "Mon équipe");

      // a lead is offered no role and no team: whoever they create is a
      // volunteer of THEIR team, and nothing else
      await expect(
        page.getByText("vous ouvrez des accès bénévoles"),
      ).toBeVisible();
      await expect(page.getByLabel("Rôle")).toHaveCount(0);

      await page.getByLabel("Nom", { exact: true }).fill(VOLUNTEER.name);
      await page.getByLabel("Adresse email").fill(VOLUNTEER.email);
      await page.getByRole("button", { name: "Créer", exact: true }).click();

      await expect(page.getByText("affiché une seule fois")).toBeVisible();
      volunteerPassword = (await page.locator(".grand-tel").innerText()).trim();

      // French, not the data model: a volunteer reading "Rôle : volunteer"
      // is being shown a column name
      const row = page.locator("tr", { hasText: VOLUNTEER.email });
      await expect(row).toContainText("Bénévole");
      await expect(row).not.toContainText("volunteer");
      await expect(row).toContainText(TEAM);
    });

    test("a team only draws from its own departments", async ({ page }) => {
      await signIn(page, ORIGIN, VOLUNTEER.email, volunteerPassword);
      await openTab(page, "Mon tableau");

      // the interface itself does not offer what the wall would refuse
      const options = page.getByLabel("Département").locator("option");
      await expect(options.filter({ hasText: "Aveyron" })).toHaveCount(1);
      expect((await options.allInnerTexts()).join(" ")).not.toContain("Cantal");

      await page.getByRole("button", { name: "Prendre un lot" }).click();
      await expect(
        page.getByRole("heading", { name: /Mes maires \([1-9]/ }),
      ).toBeVisible();

      const board = await (
        await page.request.get(`${ORIGIN}/api/dashboard`)
      ).json();
      expect(board.mine.length).toBeGreaterThan(0);
      for (const m of board.mine) {
        expect(
          m.department,
          `${m.insee_code} drawn outside the perimeter`,
        ).toBe("Aveyron");
      }

      // and asking the API directly, the way the interface never would
      const outside = await page.request.post(`${ORIGIN}/api/batch`, {
        data: { department: "Cantal" },
      });
      expect(outside.status()).toBe(403);
      expect((await outside.json()).error).toContain("périmètre");
    });

    test("a card reserved outside the team is refused, not just hidden", async ({
      page,
      playwright,
    }) => {
      // the national batch reserved by the coordination in an earlier test,
      // read through its own session
      const coordination = await playwright.request.newContext();
      const signedIn = await coordination.post(`${ORIGIN}/api/session`, {
        data: { email: COORDINATION.email, password: COORDINATION.password },
      });
      expect(signedIn.status()).toBe(200);
      const board = await (
        await coordination.get(`${ORIGIN}/api/dashboard`)
      ).json();
      const foreign = board.mine[0].insee_code;
      await coordination.dispose();

      await signIn(page, ORIGIN, VOLUNTEER.email, volunteerPassword);
      const card = await page.request.get(`${ORIGIN}/api/mayors/${foreign}`);
      expect(card.status()).toBe(403);
      expect((await card.json()).error).toContain(
        "réservée par une autre équipe",
      );
    });

    test("a deactivated access no longer opens", async ({ page }) => {
      await signIn(page, ORIGIN, LEAD.email, leadPassword);
      await openTab(page, "Mon équipe");
      await page
        .locator("tr", { hasText: VOLUNTEER.email })
        .getByRole("button", { name: "désactiver" })
        .click();
      await expect(
        page.locator("tr", { hasText: VOLUNTEER.email }),
      ).toContainText("réactiver");

      await page.getByRole("button", { name: "déconnexion" }).click();
      // the sign-in form mounts during the transition: filling a controlled
      // input that is about to be replaced loses the value silently
      await expect(
        page.getByRole("heading", { name: "Connexion" }),
      ).toBeVisible();
      await page.getByLabel("Adresse email").fill(VOLUNTEER.email);
      await expect(page.getByLabel("Adresse email")).toHaveValue(
        VOLUNTEER.email,
      );
      await page.getByLabel("Mot de passe").fill(volunteerPassword);
      await page.getByRole("button", { name: "Se connecter" }).click();

      // the SAME sentence a wrong password gets: reached only once the
      // password verified, "deactivated" would confirm to whoever typed it
      // that the credential is live — and a deactivated account is one an
      // incident just took away from somebody
      await expect(
        page.getByText("Adresse ou mot de passe incorrect"),
      ).toBeVisible();
      await expect(
        page.getByRole("button", { name: "déconnexion" }),
      ).toHaveCount(0);
    });
  });
