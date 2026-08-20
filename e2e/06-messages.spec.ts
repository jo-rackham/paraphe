import { expect, test } from "@playwright/test";

import { COORDINATION, campaignOrigin, FIRST_CAMPAIGN } from "./config.ts";
import { openTab, signIn } from "./helpers.ts";

// The messages the tool writes, read through the screen a volunteer sees.
//
// The editorial rule that matters most lives here: the rank commands the
// template, and nobody may be thanked for an endorsement they never made.
// The unit tests prove the engine renders; only this path proves the card
// in the browser feeds it the right mayor, the right campaign and the
// right volunteer.

const ORIGIN = campaignOrigin(FIRST_CAMPAIGN);
const PERSONAL = "Nous nous étions croisés au congrès des maires ruraux.";

test.describe
  .serial("the written messages", () => {
    test("the volunteer's personal touch enters their emails", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openTab(page, "Mon profil");

      await page
        .locator(".carte", { hasText: "Votre touche personnelle" })
        .locator("textarea")
        .fill(PERSONAL);
      await page
        .locator(".carte", { hasText: "Votre touche personnelle" })
        .getByRole("button", { name: "Enregistrer" })
        .click();
      await expect(
        page.getByText("Votre touche personnelle est enregistrée."),
      ).toBeVisible();

      await openTab(page, "Mon tableau");
      await page.locator("table button.lien").first().click();
      const body = await page.getByLabel("Message").inputValue();
      expect(body).toContain(PERSONAL);
    });

    // GUIDE.md tells volunteers to send from their OWN address, in small
    // batches: it is what keeps the campaign out of the spam folder. A
    // message signed by the campaign's single signatory then arrives from
    // one name under another — the mayor reads a name that is not the
    // sender's.
    test("the email is signed by whoever is actually sending it", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openTab(page, "Mon tableau");
      await page.locator("table button.lien").first().click();

      const body = await page.getByLabel("Message").inputValue();
      const me = await (await page.request.get(`${ORIGIN}/api/me`)).json();
      const cfg = await (await page.request.get(`${ORIGIN}/api/config`)).json();
      // the two must differ, or this test proves nothing
      expect(me.account.name).not.toBe(cfg.campaign.signataire);
      expect(body).toContain(me.account.name);
      expect(body).not.toContain(cfg.campaign.signataire);
    });

    test("a mayor who endorsed is thanked, by name and year", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openTab(page, "Mon tableau");
      await page.locator("table button.lien").first().click();

      await expect(page.getByText("Pourquoi cette personne")).toBeVisible();
      await expect(page.getByText(/a parrainé/)).toBeVisible();
      const body = await page.getByLabel("Message").inputValue();
      expect(body).toContain("vous avez présenté Alex Exemple");
      expect(body).toContain("En 2022");
    });

    test("outside the priority pool, the message switches to discovery", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openTab(page, "Les maires");
      await page.getByLabel("Vivier").selectOption("no_signal");

      // leaving the pool is announced before any card is opened
      await expect(
        page.getByText("Vous êtes sorti du vivier prioritaire"),
      ).toBeVisible();

      await page.locator("table button.lien").first().click();
      await expect(page.getByText("message de découverte")).toBeVisible();

      const subject = await page.getByLabel("Objet").inputValue();
      expect(subject).toContain("votre voix de maire");
      const body = await page.getByLabel("Message").inputValue();
      // the whole point: no thanks for an endorsement that never happened
      expect(body).not.toContain("vous avez présenté");
      expect(body).not.toContain("Alex Exemple");
      expect(body).not.toMatch(/\{[^}]+\}/);
    });

    test("the democratic-theme tag narrows the pool", async ({ page }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openTab(page, "Les maires");
      // the synthetic set tags every fifth endorser: 52 of the 260
      await page.getByText("Thème démocratique").click();
      await expect(page.getByText(/sur 52\./)).toBeVisible();
    });

    // The card advertises a retouchable email. The two fields were
    // uncontrolled: "Copier" read the edited DOM, the mailto link kept the
    // pristine text, dropping everything the volunteer rewrote on the one path
    // that opens their mail client.
    test("what the volunteer rewrites is what the mail client receives", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openTab(page, "Mon tableau");
      await page.locator("table button.lien").first().click();

      const rewritten = "Phrase écrite à la main par le bénévole.";
      await page.getByLabel("Message").fill(rewritten);
      await page.getByLabel("Objet").fill("Objet réécrit");

      const href = await page
        .getByRole("link", { name: /Ouvrir dans ma messagerie/ })
        .getAttribute("href");
      expect(decodeURIComponent(href ?? "")).toContain(rewritten);
      expect(decodeURIComponent(href ?? "")).toContain("Objet réécrit");

      // and addressed to THIS mayor: the link opens a mail client aimed at
      // a named elected official, so the recipient has to be asserted
      const shown = await page
        .locator("p", { hasText: /^Email :/ })
        .first()
        .innerText();
      const address = shown.replace(/^Email\s*:\s*/, "").trim();
      expect(address).toContain("@");
      expect(href).toMatch(
        new RegExp(`^mailto:${encodeURIComponent(address)}\\?`),
      );
    });

    // Team mode has no component test: the draft store is wired in Team.tsx,
    // and removing that wiring left the whole unit suite green. This is the
    // path that holds it — a volunteer who looks something up mid-call must
    // not come back to a regenerated template.
    test("a rewritten email survives a look at the Guide, and the touch reaches it", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      // opened from the browsable list, so this journey depends on no
      // batch another test happened to reserve
      await openTab(page, "Les maires");
      await page.locator("table button.lien").first().click();

      const rewritten = "Je vous écris après notre échange de mardi.";
      await page.getByLabel("Message").fill(rewritten);
      await page
        .getByRole("textbox", { name: "Note", exact: true })
        .fill("il rappelle jeudi");
      await openTab(page, "Guide");
      await openTab(page, "Les maires");
      await page.locator("table button.lien").first().click();
      expect(await page.getByLabel("Message").inputValue()).toBe(rewritten);
      expect(
        await page
          .getByRole("textbox", { name: "Note", exact: true })
          .inputValue(),
      ).toBe("il rappelle jeudi");

      // and a touch written AFTER the card was opened must reach the email
      // the volunteer will actually send — the letter beside it always did
      const touch = "Je suis élue d'une commune de 300 habitants.";
      await openTab(page, "Mon profil");
      await page
        .locator(".carte", { hasText: "Votre touche personnelle" })
        .locator("textarea")
        .fill(touch);
      await page
        .locator(".carte", { hasText: "Votre touche personnelle" })
        .getByRole("button", { name: "Enregistrer" })
        .click();
      await expect(
        page.getByText("Votre touche personnelle est enregistrée."),
      ).toBeVisible();
      await openTab(page, "Les maires");
      await page.locator("table button.lien").first().click();
      expect(await page.getByLabel("Message").inputValue()).toContain(touch);
    });

    test("the letter and the call script are ready to use", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openTab(page, "Mon tableau");
      await page.locator("table button.lien").first().click();

      await page.getByText("📮 Courrier").click();
      const letter = await page.locator("pre.lettre").innerText();
      // the address block comes from the mayor's row, the closing from the
      // campaign: both ends of the letter, one assertion each
      expect(letter).toContain("place de la Fiction");
      expect(letter).toContain("Camille Durand");

      await page.getByText("☎️ Téléphone").click();
      const script = await page
        .locator("details", { hasText: "Téléphone" })
        .locator("pre")
        .innerText();
      expect(script).toContain("lundi 9h-12h"); // when the town hall answers
      expect(script).toContain("01 23 45 67 89");
    });
  });
