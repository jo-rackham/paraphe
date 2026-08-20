import { expect, test } from "@playwright/test";

import { COORDINATION, campaignOrigin, FIRST_CAMPAIGN } from "./config.ts";
import { openTab } from "./helpers.ts";

// Two volunteers on one card. Writing a status TELLS, it does not TAKE — so
// nothing refuses the second reader — but a status is written against the
// state its writer READ, and a card somebody moved since answers 409 rather
// than silently erasing an answer nobody saw. The card then names the TEAM
// that wrote — and the team that HOLDS it — and nobody in either.

const ORIGIN = campaignOrigin(FIRST_CAMPAIGN);

// this file's own source — the shared one's sign-in budget is spent whole
// by the journeys before it (see global-setup, PARAPHE_TRUSTED_PROXIES)
const SOURCE = { "X-Forwarded-For": "192.0.2.17" };
test.use({ extraHTTPHeaders: SOURCE });

const TEAM = "Nord-17";
const IN_TEAM = {
  email: "dix-sept-equipe@premiere.test",
  name: "Théo Équipe",
};
const NATIONAL = {
  email: "dix-sept-national@premiere.test",
  name: "Nina Nationale",
};

test.describe
  .serial("two volunteers on one card", () => {
    let teamPassword = "";
    let nationalPassword = "";

    test("a write against a state that moved is refused, and erases nothing", async ({
      page,
      browser,
    }) => {
      // a team whose name the attribution will carry, one volunteer in it,
      // one in the national scope — request-level setup
      const session = await page.request.post(`${ORIGIN}/api/session`, {
        data: { email: COORDINATION.email, password: COORDINATION.password },
      });
      expect(session.status()).toBe(200);
      const team = await page.request.post(`${ORIGIN}/api/team/group`, {
        data: { name: TEAM, departments: ["Nord"] },
      });
      expect(team.status(), await team.text()).toBe(201);
      const teamId = (await team.json()).id;
      for (const [who, team_id] of [
        [IN_TEAM, teamId],
        [NATIONAL, undefined],
      ] as const) {
        const opened = await page.request.post(`${ORIGIN}/api/team/account`, {
          data: { ...who, role: "volunteer", team_id },
        });
        expect(opened.status(), await opened.text()).toBe(201);
        const { password } = await opened.json();
        if (team_id) teamPassword = password;
        else nationalPassword = password;
      }
      await page.request.delete(`${ORIGIN}/api/session`);

      // the second volunteer's browser, on this file's source too: a context
      // opened by hand inherits nothing from test.use
      const second = await browser.newContext({ extraHTTPHeaders: SOURCE });
      const theirs = await second.newPage();
      try {
        // The team's volunteer HOLDS the card: taken through a batch, which
        // is what writes volunteer and team on the assignment. Without that,
        // « names nobody » would be asserted on a card that carries no name
        // to hide, and the assertion would stay green with the mask removed.
        await theirs.request.post(`${ORIGIN}/api/session`, {
          data: { email: IN_TEAM.email, password: teamPassword },
        });
        await theirs.goto(`${ORIGIN}/`);
        await openTab(theirs, "Mon tableau");
        await theirs.getByRole("button", { name: "Prendre un lot" }).click();
        const mine = theirs.locator("table button.lien");
        await expect(mine.first()).toBeVisible();
        await mine.first().click();
        await expect(
          theirs.getByText("Cette fiche est la vôtre"),
        ).toBeVisible();
        const cardUrl = theirs.url();

        // the national volunteer opens the SAME card, reading the same
        // state — and reads the holding TEAM, never the person
        await page.request.post(`${ORIGIN}/api/session`, {
          data: { email: NATIONAL.email, password: nationalPassword },
        });
        await page.goto(cardUrl);
        await expect(page.getByLabel("Statut")).toBeVisible();
        await expect(
          page.getByText(`Travaillée par l'équipe ${TEAM}`),
        ).toBeVisible();
        await expect(page.getByText(IN_TEAM.name)).toHaveCount(0);

        // the team's volunteer answers first
        await theirs
          .getByLabel("Statut")
          .selectOption({ label: "Email envoyé" });
        await theirs.getByLabel("Note").fill("email parti le 20/08");
        await theirs
          .getByRole("button", { name: "Enregistrer", exact: true })
          .click();
        await expect(theirs.getByText("email parti le 20/08")).toBeVisible();

        // the national volunteer, still reading the state before that
        // write, is refused — in the card's own words
        await page.getByLabel("Statut").selectOption({ label: "Refus" });
        await page.getByLabel("Note").fill("refus au téléphone");
        await page
          .getByRole("button", { name: "Enregistrer", exact: true })
          .click();
        await expect(
          page.getByText(/Cette fiche a changé depuis que vous l'avez ouverte/),
        ).toBeVisible();

        // reloaded, the card shows the write that LANDED: the refused one
        // appended nothing, overwrote nothing
        await page.reload();
        await expect(page.getByLabel("Statut")).toHaveValue("email_sent");
        await expect(page.getByText("refus au téléphone")).toHaveCount(0);

        // the STATUS crossed the team line with its TEAM on it — that is
        // what keeps these two off the same mayor — and nothing else did:
        // no name anywhere, and no note, which stays with the team that
        // wrote it
        await expect(
          page.getByText(`Dernier statut enregistré par l'équipe ${TEAM}.`),
        ).toBeVisible();
        await expect(page.getByText(IN_TEAM.name)).toHaveCount(0);
        await expect(page.getByText("email parti le 20/08")).toHaveCount(0);

        // the writer's own screen carries no attribution line: it names
        // OTHER teams, and theirs is not other
        await theirs.reload();
        await expect(
          theirs.getByText(/Dernier statut enregistré par/),
        ).toHaveCount(0);
      } finally {
        await second.close();
      }
    });
  });
