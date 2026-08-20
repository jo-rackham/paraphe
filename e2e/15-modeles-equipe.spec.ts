import { expect, type Page, test } from "@playwright/test";

import { COORDINATION, campaignOrigin, FIRST_CAMPAIGN } from "./config.ts";
import { openFirstCard, openManagement, signIn } from "./helpers.ts";

// The SECOND overlay: a team's texts over its campaign's. 12-modeles proves
// the campaign layer against the image; this file proves that a référent's
// rewrite reaches their team and nobody else, that an untouched channel keeps
// FOLLOWING the campaign as it edits, and that emptying the box hands the
// team back to the campaign's text.

const ORIGIN = campaignOrigin(FIRST_CAMPAIGN);

// this file's own source — the shared one's sign-in budget is spent whole
// by the journeys before it (see global-setup, PARAPHE_TRUSTED_PROXIES)
test.use({ extraHTTPHeaders: { "X-Forwarded-For": "192.0.2.15" } });

const TEAM = "Quinze";
const LEAD = { email: "quinze-ref@premiere.test", name: "Quentin Référent" };
const VOLUNTEER = {
  email: "quinze-benevole@premiere.test",
  name: "Vince Bénévole",
};
const TEAM_EMAIL =
  "OBJET: Le mot de l'équipe Quinze\n" +
  "\n" +
  "Texte de l'équipe Quinze, pour {salutation} de {commune_de}.\n" +
  "\n" +
  "{signataire}, {signataire_qualite}\n";
const CAMPAIGN_LETTER =
  "Lettre corrigée par la campagne, pour {salutation}.\n" +
  "\n" +
  "{signataire}, {signataire_qualite}\n";

const editor = (page: Page) =>
  page.locator(".carte", { hasText: "Les modèles de messages" });
const box = (page: Page) => editor(page).getByLabel(/^Texte/);

const choose = async (page: Page, file: string) => {
  await editor(page).getByLabel("Modèle").selectOption(file);
};

const save = async (page: Page) => {
  await editor(page)
    .getByRole("button", { name: "Enregistrer les modèles" })
    .click();
};

/** Opens the email box of the first card on this account's dashboard. */
const emailOfACard = async (page: Page): Promise<string> => {
  await openFirstCard(page);
  return page.getByLabel("Message").inputValue();
};

test.describe
  .serial("a team's own message templates", () => {
    let leadPassword = "";
    let volunteerPassword = "";

    test("what the référent writes reaches their team's cards, and only theirs", async ({
      page,
    }) => {
      // the team, its lead and one volunteer in it — request-level setup,
      // the flows themselves are 05-teams' journeys
      const session = await page.request.post(`${ORIGIN}/api/session`, {
        data: { email: COORDINATION.email, password: COORDINATION.password },
      });
      expect(session.status()).toBe(200);
      const team = await page.request.post(`${ORIGIN}/api/team/group`, {
        data: { name: TEAM, departments: ["Somme"] },
      });
      expect(team.status(), await team.text()).toBe(201);
      const teamId = (await team.json()).id;
      for (const [who, role] of [
        [LEAD, "lead"],
        [VOLUNTEER, "volunteer"],
      ] as const) {
        const opened = await page.request.post(`${ORIGIN}/api/team/account`, {
          data: { ...who, role, team_id: teamId },
        });
        expect(opened.status(), await opened.text()).toBe(201);
        const { password } = await opened.json();
        if (role === "lead") leadPassword = password;
        else volunteerPassword = password;
      }
      await page.request.delete(`${ORIGIN}/api/session`);

      await signIn(page, ORIGIN, LEAD.email, leadPassword);
      await openManagement(page);
      await choose(page, "email.txt");
      // EMPTY, the inherited text as a PLACEHOLDER — filled in, it would be
      // a frozen copy — and the label says whose text an empty box follows
      await expect(box(page)).toHaveValue("");
      await expect(
        editor(page).getByText("(vide : suit le texte de la campagne)"),
      ).toBeVisible();
      expect(await box(page).getAttribute("placeholder")).toContain("OBJET:");

      await box(page).fill(TEAM_EMAIL);
      await save(page);
      await expect(
        page.getByText(/Modèles enregistrés \(1 texte/),
      ).toBeVisible();

      // stored SERVER-side, in the team layer and not the campaign's — read
      // back rather than trusted to the screen that just saved it
      const me = await (await page.request.get(`${ORIGIN}/api/me`)).json();
      expect(me.templates.team["email.txt"]).toBe(TEAM_EMAIL);
      expect(me.templates.campaign["email.txt"]).toBeUndefined();

      // the team's volunteer gets the team's text, placeholders filled
      await page.getByRole("button", { name: "déconnexion" }).click();
      await signIn(page, ORIGIN, VOLUNTEER.email, volunteerPassword);
      const email = await emailOfACard(page);
      expect(email).toContain("Texte de l'équipe Quinze");
      expect(email).not.toContain("{commune_de}");
    });

    test("outside the team, the campaign's text stands", async ({ page }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      const email = await emailOfACard(page);
      expect(email).not.toContain("Texte de l'équipe Quinze");
      expect(email.length).toBeGreaterThan(100);
    });

    test("a channel the team left empty follows the campaign as it edits", async ({
      page,
    }) => {
      // the coordination corrects ITS letter — after the team exists, after
      // its overlay is stored: what this proves is inheritance staying LIVE
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openManagement(page);
      await choose(page, "courrier.txt");
      await box(page).fill(CAMPAIGN_LETTER);
      await save(page);
      await expect(page.getByText(/Modèles enregistrés/)).toBeVisible();

      // the team never touched the letter: its volunteer's card renders the
      // campaign's correction
      await page.getByRole("button", { name: "déconnexion" }).click();
      await signIn(page, ORIGIN, VOLUNTEER.email, volunteerPassword);
      await openFirstCard(page);
      await page.getByText("📮 Courrier").click();
      const letter = await page.locator("pre.lettre").innerText();
      expect(letter).toContain("Lettre corrigée par la campagne");

      // and the référent's editor shows that correction as what an empty
      // box now follows
      await page.getByRole("button", { name: "déconnexion" }).click();
      await signIn(page, ORIGIN, LEAD.email, leadPassword);
      await openManagement(page);
      await choose(page, "courrier.txt");
      await expect(box(page)).toHaveValue("");
      expect(await box(page).getAttribute("placeholder")).toContain(
        "Lettre corrigée par la campagne",
      );
    });

    test("emptied, the team's text goes back to following the campaign", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, LEAD.email, leadPassword);
      await openManagement(page);
      await choose(page, "email.txt");
      await expect(box(page)).toHaveValue(TEAM_EMAIL);
      await editor(page)
        .getByRole("button", { name: "Revenir au texte de la campagne" })
        .click();
      await save(page);
      await expect(page.getByText(/aucun texte personnalisé/)).toBeVisible();

      // the volunteer's next card speaks the campaign's words again
      await page.getByRole("button", { name: "déconnexion" }).click();
      await signIn(page, ORIGIN, VOLUNTEER.email, volunteerPassword);
      const email = await emailOfACard(page);
      expect(email).not.toContain("Texte de l'équipe Quinze");

      // leave the campaign's letter as the image ships it: this file made
      // the correction, this file removes it
      await page.getByRole("button", { name: "déconnexion" }).click();
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openManagement(page);
      await choose(page, "courrier.txt");
      await editor(page)
        .getByRole("button", { name: "Revenir au texte fourni" })
        .click();
      await save(page);
      await expect(page.getByText(/aucun texte personnalisé/)).toBeVisible();
    });
  });
