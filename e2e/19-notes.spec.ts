import { expect, test } from "@playwright/test";

import { COORDINATION, campaignOrigin, FIRST_CAMPAIGN } from "./config.ts";
import { openTab, signIn } from "./helpers.ts";

// Correcting a note, and removing one.
//
// A note used to be DEFINITIVE: a typo taken during a call, a note recorded
// on the wrong commune, a word with no business in a register the whole
// campaign reads — and the only remedy was an UPDATE typed against
// production. Driven here through the whole stack, because what this journey
// proves lives on both sides of it: the mark saying a line was corrected is a
// column, the card rolling back to what the history then says is a second
// statement in the same transaction, and the select following that roll-back
// is a derivation in the browser.

const ORIGIN = campaignOrigin(FIRST_CAMPAIGN);

// this file's own source — the shared one's sign-in budget is spent whole
// by the journeys before it (see global-setup, PARAPHE_TRUSTED_PROXIES)
test.use({ extraHTTPHeaders: { "X-Forwarded-For": "192.0.2.19" } });

test.describe
  .serial("a note is not definitive", () => {
    test("its author corrects it, removes it, and the card follows", async ({
      page,
    }) => {
      await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
      await openTab(page, "Mon tableau");
      await page.locator("table button.lien").first().click();
      // The card's own address, kept for the reload at the end. « Mes
      // maires » is ordered BY STATUS, so the row that is first changes under
      // every write this journey makes: coming back through `.first()` would
      // open a different mayor and assert on the wrong history.
      const card = page.url();

      // Two outcomes of its own, so the history has a head AND a line under
      // it whatever the preceding journeys left on this card — which is what
      // makes the roll-back something to see.
      await page.getByLabel("Statut").selectOption({ label: "Email envoyé" });
      await page
        .getByRole("textbox", { name: "Note", exact: true })
        .fill("courriel parti ce matin");
      await page.getByRole("button", { name: "Enregistrer" }).click();
      await expect(page.getByText("courriel parti ce matin")).toBeVisible();
      // …and the SAVE is finished, which the history appearing does not say:
      // the line is drawn from state written INSIDE the awaited call, while
      // the handler goes on to clear this field. Typing into it before that
      // lands loses what was typed — measured, and it recorded an empty note
      // under the previous status.
      await expect(
        page.getByRole("textbox", { name: "Note", exact: true }),
      ).toHaveValue("");

      await page.getByLabel("Statut").selectOption({ label: "À rappeler" });
      // The pick SURVIVES the card the previous save handed back. Caught up
      // from the prop rather than derived from it, that arrival overwrote it:
      // the outcome chosen here was recorded as the previous one, and the
      // screen said « Enregistré. »
      await expect(page.getByLabel("Statut")).toHaveValue("to_call_back");
      await page
        .getByRole("textbox", { name: "Note", exact: true })
        .fill("aple lundi");
      await page.getByRole("button", { name: "Enregistrer" }).click();
      await expect(page.getByText("aple lundi")).toBeVisible();
      await expect(
        page.getByRole("textbox", { name: "Note", exact: true }),
      ).toHaveValue("");

      // corrected: the words move, the status the line recorded does not.
      // « la note 1 » is the most recent — the history is newest first, and
      // two outcomes of the same minute share a date, so the position is what
      // names a row.
      await page.getByRole("button", { name: "Modifier la note 1 du" }).click();
      await page.getByLabel("Texte de la note").fill("rappeler lundi");
      await page.getByRole("button", { name: "Enregistrer la note" }).click();
      await expect(page.getByText("rappeler lundi")).toBeVisible();
      await expect(page.getByText(/modifiée le/).first()).toBeVisible();
      // the editor closes one commit AFTER the card comes back, and until it
      // does its row carries no « Supprimer » — so the next click would land
      // on the row BELOW and remove a note nobody aimed at
      await expect(page.getByLabel("Texte de la note")).toHaveCount(0);
      await expect(page.getByLabel("Statut")).toHaveValue("to_call_back");

      // removed: asked first, then the card goes back to what the history
      // says — the outcome recorded before it, and NOT the one just withdrawn
      await page
        .getByRole("button", { name: "Supprimer la note 1 du" })
        .click();
      await expect(page.getByText("Supprimer cette note ?")).toBeVisible();
      await page.getByRole("button", { name: "Confirmer" }).click();
      await expect(page.getByText("rappeler lundi")).toHaveCount(0);
      await expect(page.getByText("courriel parti ce matin")).toBeVisible();
      await expect(page.getByLabel("Statut")).toHaveValue("email_sent");

      // and it is the SERVER's, not this tab's: reopened at its own address,
      // the card carries the roll-back
      await page.goto(card);
      await expect(page.getByLabel("Statut")).toHaveValue("email_sent");
      await expect(page.getByText("rappeler lundi")).toHaveCount(0);
      await expect(page.getByText("courriel parti ce matin")).toBeVisible();
    });
  });
