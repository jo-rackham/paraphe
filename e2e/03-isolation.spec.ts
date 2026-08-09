import { expect, test } from "@playwright/test";

import {
  API_ORIGIN, campaignOrigin, COORDINATION, FIRST_CAMPAIGN, INSTANCE_ADMIN,
} from "./config.ts";
import { openTab, signIn } from "./helpers.ts";

// Two campaigns on the same instance. The mayor list is shared — it is public
// data, identical for everyone — but the work is not: what one campaign
// reserves must stay invisible to the other, and no session may cross over.
//
// This is the guarantee PostgreSQL enforces through row-level security rather
// than through the WHERE clauses of the routes. Here it is checked from the
// outside, the way a rival campaign would try it.

const SLUG = "troisieme";
const REQUESTER = "porteur@troisieme.test";

test.describe.serial("two campaigns side by side", () => {
  let password = "";

  test("a second campaign is opened through the API", async ({ page }) => {
    await page.goto(`${API_ORIGIN}/`);
    const filed = await page.request.post(`${API_ORIGIN}/api/request`, {
      data: {
        slug: SLUG, name: "Campagne Troisième", requester_email: REQUESTER,
        requester_name: "Autre Porteur", message: "une autre candidature",
      },
    });
    expect(filed.status()).toBe(201);

    const signedIn = await page.request.post(`${API_ORIGIN}/api/session`, {
      data: { email: INSTANCE_ADMIN.email, password: INSTANCE_ADMIN.password },
    });
    expect(signedIn.status()).toBe(200);

    const queue = await (await page.request.get(`${API_ORIGIN}/api/admin/requests`)).json();
    const pending = queue.requests.find(
      (d: { slug: string; state: string }) => d.slug === SLUG && d.state === "pending");
    expect(pending, "the request is not in the moderation queue").toBeTruthy();

    const decided = await page.request.post(
      `${API_ORIGIN}/api/admin/requests/${pending.id}`,
      { data: { decision: "accepted" } });
    expect(decided.status()).toBe(200);
    password = (await decided.json()).password;
    expect(password.length).toBeGreaterThan(8);
  });

  test("what one campaign reserves stays invisible to the other",
    async ({ page }) => {
      await signIn(page, campaignOrigin(SLUG), REQUESTER, password);
      await openTab(page, "Mon tableau");
      await page.getByRole("button", { name: "Prendre un lot" }).click();
      await expect(page.getByRole("heading", { name: /Mes maires \([1-9]/ }))
        .toBeVisible();

      const mine = await (await page.request.get(
        `${campaignOrigin(SLUG)}/api/dashboard`)).json();
      const reserved: string[] = mine.mine.map(
        (m: { insee_code: string }) => m.insee_code);
      expect(reserved.length).toBeGreaterThan(0);

      // The other campaign reaches those very mayors — the list is shared, and
      // both campaigns start from the best-scored ones, so they legitimately
      // work on the same people. What must never appear there is WHO, in this
      // campaign, took them.
      await signIn(page, campaignOrigin(FIRST_CAMPAIGN),
        COORDINATION.email, COORDINATION.password);
      for (const insee of reserved) {
        const card = await page.request.get(
          `${campaignOrigin(FIRST_CAMPAIGN)}/api/mayors/${insee}`);
        expect(card.status(), `${insee} refused to the other campaign`).toBe(200);
        const { mayor, notes } = await card.json();
        expect(mayor.volunteer, `${insee} carries the other campaign's volunteer`)
          .not.toBe(REQUESTER);
        for (const note of notes ?? []) {
          expect(JSON.stringify(note)).not.toContain(REQUESTER);
        }
      }

      // and its export mentions nobody from the other campaign
      const csv = await (await page.request.get(
        `${campaignOrigin(FIRST_CAMPAIGN)}/api/export.csv`)).text();
      expect(csv).not.toContain(REQUESTER);
    });

  test("a session does not cross from one campaign to the other",
    async ({ page }) => {
      await signIn(page, campaignOrigin(SLUG), REQUESTER, password);
      const elsewhere = await page.request.get(
        `${campaignOrigin(FIRST_CAMPAIGN)}/api/me`);
      expect(elsewhere.status()).toBe(401);
    });

  test("an unknown subdomain is not served some campaign at random",
    async ({ page }) => {
      const response = await page.request.get(
        `${campaignOrigin("inexistante")}/api/config`);
      expect(response.status()).toBe(404);
    });
});
