import { expect, type Page, test } from "@playwright/test";

import { COORDINATION, campaignOrigin, FIRST_CAMPAIGN } from "./config.ts";
import { openManagement, openTab, signIn } from "./helpers.ts";

// What a campaign's coordination corrects and refuses: a team request turned
// down, a team renamed and re-drawn after creation, a role moved between the
// three, an access switched back on. 05-teams drives the happy paths; these
// are the doors that close or move, covered in Go only until now.

const ORIGIN = campaignOrigin(FIRST_CAMPAIGN);

// this file's own source — the shared one's sign-in budget is spent whole
// by the journeys before it (see global-setup, PARAPHE_TRUSTED_PROXIES)
test.use({ extraHTTPHeaders: { "X-Forwarded-For": "192.0.2.14" } });

const TEAM = { asked: "Équipe Quatorze", corrected: "Équipe Grand Est" };
const LEAD = { email: "quatorze-ref@premiere.test", name: "Renaud Quatorze" };
const PROMOTED = {
  email: "quatorze-promu@premiere.test",
  name: "Paula Promue",
};
const DORMANT = {
  email: "quatorze-dormant@premiere.test",
  name: "Dora Dormante",
};

/** Signs the shared cookie jar in as coordination, for request-level setup. */
async function coordinationSession(page: Page) {
  const r = await page.request.post(`${ORIGIN}/api/session`, {
    data: { email: COORDINATION.email, password: COORDINATION.password },
  });
  expect(r.status(), await r.text()).toBe(200);
}

/** Opens an access through the API and returns its one-time password. */
async function openAccess(
  page: Page,
  account: { email: string; name: string; role?: string; team_id?: number },
): Promise<string> {
  const r = await page.request.post(`${ORIGIN}/api/team/account`, {
    data: account,
  });
  expect(r.status(), await r.text()).toBe(201);
  return (await r.json()).password;
}

test.describe
  .serial("what the coordination corrects and refuses", () => {
    let teamId = 0;
    let leadPassword = "";
    let promotedPassword = "";

    test("a refused team request opens nothing", async ({ page }) => {
      // the public form, straight at the API: the FORM's own journey lives
      // in 05-teams, this one is about the decision behind it
      const filed = await page.request.post(`${ORIGIN}/api/team/request`, {
        data: {
          name: "Comité fantôme",
          departments: ["Somme"],
          requester_name: "Fanny Fantôme",
          requester_email: "fantome@premiere.test",
          message: "Une demande que la coordination va refuser.",
        },
      });
      expect(filed.status(), await filed.text()).toBe(201);

      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openManagement(page);
      const queued = page.locator(".carte", { hasText: "Comité fantôme" });
      await expect(queued).toContainText("fantome@premiere.test");
      // no « J'ai vérifié » tick: a refusal sends nothing to the address
      await queued.getByRole("button", { name: "Refuser" }).click();

      // the request joins the decided table, and that is ALL that exists
      await expect(
        page.locator("tr", { hasText: "Comité fantôme" }),
      ).toContainText("Refusée");

      // no access — the return code first
      const signin = await page.request.post(`${ORIGIN}/api/session`, {
        data: { email: "fantome@premiere.test", password: "peu importe" },
      });
      expect(signin.status()).toBe(401);
      // and no team: « Les équipes locales » does not carry it
      const teams = page.locator("table").last();
      await expect(
        teams.locator("tr", { hasText: "Comité fantôme" }),
      ).toHaveCount(0);
    });

    test("a team's batches come from the perimeter it was given", async ({
      page,
    }) => {
      await coordinationSession(page);
      const created = await page.request.post(`${ORIGIN}/api/team/group`, {
        data: { name: TEAM.asked, departments: ["Moselle"] },
      });
      expect(created.status(), await created.text()).toBe(201);
      teamId = (await created.json()).id;
      leadPassword = await openAccess(page, {
        ...LEAD,
        role: "lead",
        team_id: teamId,
      });

      // the lead draws a batch: every card of it is inside the perimeter
      await page.request.delete(`${ORIGIN}/api/session`);
      await signIn(page, ORIGIN, LEAD.email, leadPassword);
      await openTab(page, "Mon tableau");
      await page.getByRole("button", { name: "Prendre un lot" }).click();
      const mine = page.locator("table.maires tbody tr");
      await expect(mine.first()).toBeVisible();
      for (const row of await mine.all()) {
        await expect(row).toContainText("Moselle");
      }
    });

    test("the coordination corrects the name and the perimeter, and the batches follow", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openManagement(page);

      // once the row is in edit mode its name lives in an input's VALUE, so
      // every control is reached by its accessible name instead of row text
      await page
        .getByRole("button", { name: `modifier ${TEAM.asked}` })
        .click();
      await page
        .getByRole("textbox", { name: `Nom de l'équipe ${TEAM.asked}` })
        .fill(TEAM.corrected);
      await page
        .getByRole("listbox", { name: `Départements de ${TEAM.asked}` })
        .selectOption(["Vosges"]);
      await page
        .getByRole("button", { name: `enregistrer ${TEAM.asked}` })
        .click();

      // the LAST table — « Les équipes locales » — because the lead's row in
      // the accounts table above now carries the corrected name too
      const corrected = page
        .locator("table")
        .last()
        .locator("tr", { hasText: TEAM.corrected });
      await expect(corrected).toBeVisible();
      await expect(corrected).toContainText("Vosges");
      await expect(corrected).not.toContainText("Moselle");

      // the lead's NEXT batch draws from the corrected perimeter — the
      // Moselle cards already assigned stay theirs, which is why the
      // assertion is about what the new batch adds
      await page.getByRole("button", { name: "déconnexion" }).click();
      await signIn(page, ORIGIN, LEAD.email, leadPassword);
      await openTab(page, "Mon tableau");
      await page.getByRole("button", { name: "Prendre un lot" }).click();
      await expect(
        page.locator("table.maires tbody tr", { hasText: "Vosges" }).first(),
      ).toBeVisible();
    });

    test("a volunteer promoted to lead opens accesses, and steps back down", async ({
      page,
    }) => {
      // the request-level sign-in fills the context's cookie jar, so the
      // page that opens next is already the coordination's
      await coordinationSession(page);
      promotedPassword = await openAccess(page, {
        ...PROMOTED,
        role: "volunteer",
        team_id: teamId,
      });

      await page.goto(`${ORIGIN}/`);
      await expect(
        page.getByRole("button", { name: "déconnexion" }),
      ).toBeVisible();
      await openManagement(page);
      const roleOf = page.getByRole("combobox", {
        name: `Rôle de ${PROMOTED.name}`,
      });
      await roleOf.selectOption({ label: "Référent" });
      // the select's VALUE, not the row's text: the three labels are always
      // in the DOM as options, so text would match whatever the role is
      await expect(roleOf).toHaveValue("lead");

      // the promoted person's screen now carries the management tab, under
      // the name a référent gets, with a lead's powers and no more
      await page.getByRole("button", { name: "déconnexion" }).click();
      await signIn(page, ORIGIN, PROMOTED.email, promotedPassword);
      await openManagement(page);
      await expect(
        page.getByText("vous ouvrez des accès bénévoles"),
      ).toBeVisible();

      // demoted, the tab is gone from a fresh session
      await page.getByRole("button", { name: "déconnexion" }).click();
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openManagement(page);
      const demoted = page.getByRole("combobox", {
        name: `Rôle de ${PROMOTED.name}`,
      });
      await demoted.selectOption({ label: "Bénévole" });
      await expect(demoted).toHaveValue("volunteer");

      await page.getByRole("button", { name: "déconnexion" }).click();
      await signIn(page, ORIGIN, PROMOTED.email, promotedPassword);
      await expect(
        page.getByRole("button", { name: /^(Ma campagne|Mon équipe)$/ }),
      ).toHaveCount(0);
    });

    test("a reactivated access opens again", async ({ page }) => {
      await coordinationSession(page);
      const password = await openAccess(page, {
        ...DORMANT,
        role: "volunteer",
      });

      await page.goto(`${ORIGIN}/`);
      await expect(
        page.getByRole("button", { name: "déconnexion" }),
      ).toBeVisible();
      await openManagement(page);
      const row = page.locator("tr", { hasText: DORMANT.email });
      await row.getByRole("button", { name: "désactiver" }).click();
      await expect(row).toContainText("(désactivé)");

      // closed — the return code, not the absence of a screen
      const shut = await page.request.post(`${ORIGIN}/api/session`, {
        data: { email: DORMANT.email, password },
      });
      expect(shut.status()).toBe(401);

      await row.getByRole("button", { name: "réactiver" }).click();
      await expect(row).not.toContainText("(désactivé)");

      // …and the SAME password opens it: reactivation gives back the access
      // that was suspended, it does not mint a new one
      await page.getByRole("button", { name: "déconnexion" }).click();
      await signIn(page, ORIGIN, DORMANT.email, password);
    });
  });
