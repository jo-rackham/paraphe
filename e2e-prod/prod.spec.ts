import { expect, type Page, test } from "@playwright/test";

// Production, exercised for real. Three sections, three levels of touch:
//
//   1. The doors — read-only: health, headers, the apex, the directory.
//   2. The account-less version — writes THIS RUNNER's IndexedDB and nothing
//      on the server; wiped at the end of the journey.
//   3. A signed-in campaign — needs a PROBE campaign's coordination, given
//      by environment, and confines every write to that campaign:
//
//        PARAPHE_PROD_CAMPAIGN_ORIGIN   e.g. https://essai-preuve.paraphe.org
//        PARAPHE_PROD_COORD_EMAIL
//        PARAPHE_PROD_COORD_PASSWORD
//
//      Without them the section SKIPS, out loud. The probe campaign is the
//      operator's to open (unlisted, .invalid addresses — see the grant
//      route); this suite never creates a campaign, an account or a public
//      request: production has no route to delete any of them.
//
// The public hosting form is never submitted here — three per hour per
// source, and each one is moderation work for a human.

const ORIGIN = (
  process.env.PARAPHE_PROD_ORIGIN ?? "https://paraphe.org"
).trim();

test.describe("the doors, read only", () => {
  test("health answers, database included", async ({ request }) => {
    const live = await request.get(`${ORIGIN}/health`);
    expect(live.status()).toBe(200);
    expect((await live.json()).state).toBe("ok");
    const ready = await request.get(`${ORIGIN}/health/db`);
    expect(ready.status()).toBe(200);
    expect((await ready.json()).state).toBe("ok");
  });

  test("every page carries the binary's security headers", async ({
    request,
  }) => {
    const page = await request.get(`${ORIGIN}/`);
    expect(page.status()).toBe(200);
    const csp = page.headers()["content-security-policy"] ?? "";
    // the policy the binary assembles — pages included, which is the reason
    // the two images became one
    expect(csp).toContain("default-src 'self'");
    expect(csp).toContain("frame-ancestors 'none'");
    expect(csp).toContain("base-uri 'none'");
    expect(page.headers()["x-content-type-options"]).toBe("nosniff");
    expect(page.headers()["referrer-policy"]).toBe("same-origin");
    // HSTS is present. As served it is the INGRESS's (max-age=15768000, no
    // includeSubDomains), which overrides the binary's two-year one — known,
    // observed 20/08/2026; this asserts the protection exists at all.
    expect(page.headers()["strict-transport-security"]).toContain("max-age=");
  });

  test("the served page carries no inline script", async ({ request }) => {
    // default-src 'self' with no script-src refuses inline scripts: one in
    // the page would be a blank screen. A CSP violation reported by a
    // volunteer whose page WORKS is therefore an extension's, not ours.
    const html = await (await request.get(`${ORIGIN}/`)).text();
    const inline = html.match(/<script(?![^>]*\bsrc=)[^>]*>/g) ?? [];
    expect(inline).toEqual([]);
  });

  test("the bundle leaves compressed", async ({ request }) => {
    const html = await (await request.get(`${ORIGIN}/`)).text();
    const bundle = /\/assets\/[^"]+\.js/.exec(html)?.[0];
    expect(bundle, "no bundle referenced by the page").toBeTruthy();
    const js = await request.get(`${ORIGIN}${bundle}`, {
      headers: { "Accept-Encoding": "br" },
    });
    expect(js.status()).toBe(200);
    expect(js.headers()["content-encoding"]).toBe("br");
  });

  test("the apex explains, lists the hosted campaigns, and serves no campaign", async ({
    page,
    request,
  }) => {
    await page.goto(`${ORIGIN}/`);
    await expect(
      page.getByRole("heading", {
        name: "Chercher 500 parrainages, méthodiquement",
      }),
    ).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Les campagnes hébergées" }),
    ).toBeVisible();

    // the one cross-origin-readable route: on the apex there is no campaign
    // to offer, and 404 is that absence — the account-less version reads it
    // as « no campaign here » and stays quiet
    const offer = await request.get(`${ORIGIN}/api/campaign/public`);
    expect(offer.status()).toBe(404);
    const directory = await request.get(`${ORIGIN}/api/campaigns`);
    expect(directory.status()).toBe(200);
    expect(Array.isArray((await directory.json()).campaigns)).toBe(true);
  });
});

test.describe
  .serial("the account-less version, on this runner alone", () => {
    test("loads the real priority list, and a card writes a message", async ({
      page,
    }) => {
      await page.goto(`${ORIGIN}/navigateur/`);
      // the account-less build: no mode marker on an origin that serves an API
      await expect(page.locator('meta[name="paraphe-mode"]')).toHaveCount(0);
      // the REAL list: ~1,960 endorsers, downloaded from this origin
      await expect(page.getByText(/affiché\(s\) sur 1\d{3}\./)).toBeVisible({
        timeout: 30_000,
      });
      await page.locator("table button.lien").first().click();
      // a real mayor's card, a message generated from the real row
      const email = await page.getByLabel("Message").inputValue();
      expect(email.length).toBeGreaterThan(200);
      expect(email).not.toMatch(/\{[a-z_]+\}/);
      // the way back to the account version, on this very origin
      await expect(
        page.getByRole("link", {
          name: "Revenir à la version avec compte, pour travailler à plusieurs",
        }),
      ).toBeVisible();
    });

    test("a rewritten template is stored, survives a reload, and is wiped", async ({
      page,
    }) => {
      await page.goto(`${ORIGIN}/navigateur/campagne`);
      const editor = page.locator(".carte", {
        hasText: "Les modèles de messages",
      });
      // the subject is its own field; the stored file keeps the OBJET line
      await editor.getByLabel("Objet de l'email").fill("Sonde e2e prod");
      const box = editor.getByLabel(/^Texte/);
      await box.fill(
        "{salutation},\n\nTexte de sonde — ce navigateur seulement.\n\n{signataire}, {signataire_qualite}\n",
      );
      await editor
        .getByRole("button", { name: "Enregistrer les modèles" })
        .click();
      // the unsaved-changes note clears: what the box holds IS what is stored
      await expect(
        editor.getByText("modifications non enregistrées"),
      ).toHaveCount(0);

      // stored in IndexedDB: a fresh document load reads it back
      await page.goto(`${ORIGIN}/navigateur/campagne`);
      await expect(
        page
          .locator(".carte", { hasText: "Les modèles de messages" })
          .getByLabel("Modèle"),
      ).toHaveValue("email.txt");
      await expect(
        page.locator(".carte", { hasText: "Les modèles de messages" }),
      ).toContainText("Texte personnalisé");

      // leave the runner as found: back to the shipped text, then wipe
      const again = page.locator(".carte", {
        hasText: "Les modèles de messages",
      });
      await again
        .getByRole("button", { name: "Revenir au texte fourni" })
        .click();
      await again
        .getByRole("button", { name: "Enregistrer les modèles" })
        .click();
      // said through the page's own region, like every save confirmation
      await expect(page.getByText(/aucun texte personnalisé/)).toBeVisible();
      await page.goto(`${ORIGIN}/navigateur/donnees`);
      page.on("dialog", (d) => d.accept());
      await page.getByRole("button", { name: "Effacer ce navigateur" }).click();
      await expect(
        page.getByText("Tout a été effacé de ce navigateur."),
      ).toBeVisible();
    });
  });

// -- The signed-in campaign, on the operator's probe -------------------------

const PROBE = {
  origin: (process.env.PARAPHE_PROD_CAMPAIGN_ORIGIN ?? "").trim(),
  email: (process.env.PARAPHE_PROD_COORD_EMAIL ?? "").trim(),
  password: (process.env.PARAPHE_PROD_COORD_PASSWORD ?? "").trim(),
};
const probeConfigured = () =>
  PROBE.origin !== "" && PROBE.email !== "" && PROBE.password !== "";

async function probeSignIn(page: Page) {
  await page.goto(`${PROBE.origin}/`);
  await page.getByLabel("Adresse email").fill(PROBE.email);
  await page.getByLabel("Mot de passe").fill(PROBE.password);
  await page.getByRole("button", { name: "Se connecter" }).click();
  await expect(page.getByRole("button", { name: "déconnexion" })).toBeVisible();
}

test.describe
  .serial("the probe campaign, signed in", () => {
    test.skip(
      () => !probeConfigured(),
      "PARAPHE_PROD_CAMPAIGN_ORIGIN / _COORD_EMAIL / _COORD_PASSWORD not set: " +
        "the signed-in journeys need the probe campaign's coordination",
    );

    test("the coordination signs in, and a card renders a message", async ({
      page,
    }) => {
      await probeSignIn(page);
      // the guide is the landing; the dashboard hands a card
      await page
        .getByRole("button", { name: "Mon tableau", exact: true })
        .click();
      await expect(
        page.getByRole("button", { name: "Prendre un lot" }),
      ).toBeVisible();
      const cards = page.locator("table button.lien");
      if ((await cards.count()) === 0) {
        await page.getByRole("button", { name: "Prendre un lot" }).click();
      }
      await expect(cards.first()).toBeVisible();
      await cards.first().click();
      const email = await page.getByLabel("Message").inputValue();
      expect(email.length).toBeGreaterThan(200);
      expect(email).not.toMatch(/\{[a-z_]+\}/);
    });

    test("a campaign template saves, renders on the card, and is reverted", async ({
      page,
    }) => {
      await probeSignIn(page);
      await page
        .getByRole("button", { name: /^(Ma campagne|Mon équipe)$/ })
        .click();
      const editor = page.locator(".carte", {
        hasText: "Les modèles de messages",
      });
      await editor.getByLabel("Modèle").selectOption("courrier.txt");
      const box = editor.getByLabel(/^Texte/);
      await box.fill(
        "Texte de sonde e2e prod, pour {salutation} de {commune_de}.\n\n{signataire}, {signataire_qualite}\n",
      );
      await editor
        .getByRole("button", { name: "Enregistrer les modèles" })
        .click();
      await expect(page.getByText(/Modèles enregistrés/)).toBeVisible();
      await expect(
        editor.getByText("modifications non enregistrées"),
      ).toHaveCount(0);

      // the card renders the stored text, placeholders filled
      await page
        .getByRole("button", { name: "Mon tableau", exact: true })
        .click();
      await page.locator("table button.lien").first().click();
      await page.getByText("📮 Courrier").click();
      const letter = await page.locator("pre.lettre").innerText();
      expect(letter).toContain("Texte de sonde e2e prod");
      expect(letter).not.toMatch(/\{[a-z_]+\}/);

      // leave the probe campaign on the shipped text
      await page
        .getByRole("button", { name: /^(Ma campagne|Mon équipe)$/ })
        .click();
      const again = page.locator(".carte", {
        hasText: "Les modèles de messages",
      });
      await again.getByLabel("Modèle").selectOption("courrier.txt");
      await again.getByRole("button", { name: "Revenir au texte" }).click();
      await again
        .getByRole("button", { name: "Enregistrer les modèles" })
        .click();
      await expect(page.getByText(/aucun texte personnalisé/)).toBeVisible();
      await page.getByRole("button", { name: "déconnexion" }).click();
    });
  });
