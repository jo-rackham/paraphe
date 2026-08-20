import { expect, test } from "@playwright/test";
import pg from "pg";

import {
  API_ORIGIN,
  appDatabaseUrl,
  campaignOrigin,
  FIRST_CAMPAIGN,
  INSTANCE_ADMIN,
} from "./config.ts";
import { linkIn, signIn, waitForMail } from "./helpers.ts";

// The other half of moderation: what the administration REFUSES, and the way
// back in when a campaign locks itself out. 01-instance drives the happy
// path — request, approval, invitation; this file drives the doors that stay
// shut, which only ever existed in Go tests until now.

// This file's own SOURCE, believed because the harness declares the loopback
// a trusted proxy the way production declares its ingress (global-setup).
// The suite's shared source is spent: its sign-in ceiling is counted and
// never refunded, and the hosting form's three-per-hour is already filed by
// 01 and 03. Every context this file opens carries the same address.
const SOURCE = { "X-Forwarded-For": "192.0.2.13" };
test.use({ extraHTTPHeaders: SOURCE });

const REFUSED_SLUG = "refusee";
const GRANTED = { email: "secours@premiere.test", name: "Sacha Secours" };
const ASLEEP = "sommeil";

async function signInAsAdmin(page: import("@playwright/test").Page) {
  await page.goto(`${API_ORIGIN}/`);
  await page.getByRole("button", { name: "Se connecter" }).click();
  await page.getByLabel("Adresse email").fill(INSTANCE_ADMIN.email);
  await page.getByLabel("Mot de passe").fill(INSTANCE_ADMIN.password);
  await page.getByRole("button", { name: "Se connecter" }).click();
  await expect(
    page.getByRole("heading", { name: "Demandes d'hébergement" }),
  ).toBeVisible();
}

test.describe
  .serial("what the instance refuses, and the way back in", () => {
    test("a refused request grants nothing, and joins the processed list", async ({
      page,
    }) => {
      // filed from THIS file's source: on the shared one the form's
      // three-per-hour is already spent, and a fourth request reads a 429
      const filed = await page.request.post(`${API_ORIGIN}/api/request`, {
        data: {
          slug: REFUSED_SLUG,
          name: "Campagne à refuser",
          requester_name: "Rémi Refusé",
          requester_email: "refuse@exemple.test",
          message: "Une demande que l'administration va refuser.",
        },
      });
      expect(filed.status(), await filed.text()).toBe(201);

      await signInAsAdmin(page);
      const queued = page.locator(".carte", { hasText: REFUSED_SLUG });
      await expect(queued).toContainText("refuse@exemple.test");
      // the reason is typed BEFORE deciding: it is what the processed list
      // keeps, and the only trace of why
      await queued
        .getByLabel(/Motif/)
        .fill("Le nom demandé appartient à une campagne existante.");
      // no « J'ai vérifié » tick on this path: a refusal sends nothing
      await queued.getByRole("button", { name: "Refuser" }).click();

      // the request leaves the queue for « Demandes traitées », with the
      // decision, the reason and who took it
      const processed = page
        .locator(".carte", { hasText: "Refusée" })
        .locator("tr", { hasText: REFUSED_SLUG });
      await expect(processed).toBeVisible();
      await expect(processed).toContainText("Refusée");
      await expect(processed).toContainText("campagne existante");
      await expect(processed).toContainText(INSTANCE_ADMIN.email);

      // and NOTHING was created — the return code first, then the door
      const config = await page.request.get(
        `${campaignOrigin(REFUSED_SLUG)}/api/config`,
      );
      expect(config.status()).toBe(404);
    });

    test("the administration opens a coordination access on a campaign that locked itself out", async ({
      page,
      browser,
    }) => {
      await signInAsAdmin(page);

      const form = page.getByRole("form", {
        name: "Ouvrir un accès de coordination",
      });
      // chosen from the hosted list, never typed: the bootstrap campaign is
      // selectable by its address even while its coordination never named it
      await form.getByLabel("Campagne").selectOption(FIRST_CAMPAIGN);
      await form.getByLabel("Nom", { exact: true }).fill(GRANTED.name);
      await form.getByLabel("Adresse email").fill(GRANTED.email);
      await page.getByRole("button", { name: "Ouvrir l'accès" }).click();

      // the same one-time password card as every other flow that mints one
      await expect(
        page.getByRole("heading", {
          name: `Accès de coordination ouvert sur ${FIRST_CAMPAIGN}.localhost`,
        }),
      ).toBeVisible();
      const password = (
        await page.locator(".carte code").first().innerText()
      ).trim();
      expect(password.length).toBeGreaterThan(8);

      // the invitation the grant sent opens the campaign — on the CAMPAIGN's
      // subdomain, not the apex the administrator was on
      const link = linkIn(await waitForMail(GRANTED.email));
      expect(
        link.startsWith(`${campaignOrigin(FIRST_CAMPAIGN)}/connexion#jeton=`),
      ).toBe(true);
      // a fresh browser, as the recipient's is — carrying this file's
      // source, as every context here must
      const recipient = await browser.newContext({ extraHTTPHeaders: SOURCE });
      const opened = await recipient.newPage();
      await opened.goto(link);
      await expect(
        opened.getByRole("button", { name: "déconnexion" }),
      ).toBeVisible();

      // the act is visible to the campaign it was done to: the account the
      // instance opened carries the administrator's address in `created_by`,
      // in the roster the campaign's own management screen loads
      const roster = await opened.request.get(
        `${campaignOrigin(FIRST_CAMPAIGN)}/api/team`,
      );
      expect(roster.status()).toBe(200);
      const granted = (await roster.json()).accounts.find(
        (a: { email: string }) => a.email === GRANTED.email,
      );
      expect(granted?.role).toBe("coordination");
      expect(granted?.created_by).toBe(INSTANCE_ADMIN.email);
      await recipient.close();

      // …and the one-time password opens the same door
      await signIn(
        page,
        campaignOrigin(FIRST_CAMPAIGN),
        GRANTED.email,
        password,
      );
    });

    test("an address that already has an account is refused, never promoted", async ({
      page,
    }) => {
      await signInAsAdmin(page);
      const form = page.getByRole("form", {
        name: "Ouvrir un accès de coordination",
      });
      await form.getByLabel("Campagne").selectOption(FIRST_CAMPAIGN);
      await form.getByLabel("Nom", { exact: true }).fill(GRANTED.name);
      await form.getByLabel("Adresse email").fill(GRANTED.email);
      await page.getByRole("button", { name: "Ouvrir l'accès" }).click();

      // the 409's own sentence, on screen: the ONE fact this route discloses
      await expect(
        page.getByText(`${GRANTED.email} a déjà un compte sur cette campagne`),
      ).toBeVisible();
    });

    test("a suspended campaign answers 503 everywhere, and comes back whole", async ({
      page,
    }) => {
      // a campaign of this journey's own, so nothing else depends on it
      await signInAsAdmin(page);
      const created = await page.request.post(
        `${API_ORIGIN}/api/admin/campaigns`,
        {
          data: {
            slug: ASLEEP,
            name: "Campagne en sommeil",
            coordination_email: "coord@sommeil.test",
            coordination_name: "Coralie Coordination",
          },
        },
      );
      expect(created.status(), await created.text()).toBe(201);
      const { password } = await created.json();
      const origin = campaignOrigin(ASLEEP);

      // alive first: the sign-in that works is what makes the 503 below a
      // statement about the suspension and not about the campaign
      const before = await page.request.post(`${origin}/api/session`, {
        data: { email: "coord@sommeil.test", password },
      });
      expect(before.status()).toBe(200);

      // No route sets this state — an operator types it, deliberately, so
      // the journey types it too. The application's own DSN, the same one
      // the API under test holds.
      const db = new pg.Client({ connectionString: appDatabaseUrl() });
      await db.connect();
      try {
        await db.query("UPDATE orgs SET state='suspended' WHERE slug=$1", [
          ASLEEP,
        ]);

        // every route of the campaign, sign-in included
        const config = await page.request.get(`${origin}/api/config`);
        expect(config.status()).toBe(503);
        expect((await config.json()).error).toContain(
          "Cette campagne est suspendue",
        );
        const signin = await page.request.post(`${origin}/api/session`, {
          data: { email: "coord@sommeil.test", password },
        });
        expect(signin.status()).toBe(503);

        // minting a credential on it is refused too: it would open nothing
        // and say it worked
        const grant = await page.request.post(
          `${API_ORIGIN}/api/admin/campaigns/${ASLEEP}/coordination`,
          { data: { email: "autre@sommeil.test", name: "Autre Personne" } },
        );
        expect(grant.status()).toBe(409);
        expect((await grant.json()).error).toContain("est suspendue");

        // what a volunteer sees: the outage shell, carrying the server's own
        // sentence — work preserved, whom to contact
        await page.goto(`${origin}/`);
        await expect(
          page.getByText("Cette campagne est suspendue"),
        ).toBeVisible();
      } finally {
        // in the FINALLY: an assertion failing above must not leave the
        // campaign suspended for whatever runs after this file
        await db.query("UPDATE orgs SET state='active' WHERE slug=$1", [
          ASLEEP,
        ]);
        await db.end();
      }

      // « Réessayer » walks out of the outage — and straight back INTO the
      // session the suspension had shut out: the cookie this page held was
      // never invalidated, only refused, so reactivation gives back exactly
      // the session that was standing
      await page.getByRole("button", { name: "Réessayer" }).click();
      await expect(
        page.getByRole("button", { name: "déconnexion" }),
      ).toBeVisible();
      await expect(page.getByText("Campagne en sommeil")).toBeVisible();
    });
  });
