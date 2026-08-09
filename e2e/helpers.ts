import { expect, type Page } from "@playwright/test";

/** Signs in and waits for the application to be past the login form. */
export async function signIn(
  page: Page, origin: string, email: string, password: string,
) {
  await page.goto(`${origin}/`);
  await page.getByLabel("Adresse email").fill(email);
  await page.getByLabel("Mot de passe").fill(password);
  await page.getByRole("button", { name: "Se connecter" }).click();
  await expect(page.getByRole("button", { name: "déconnexion" })).toBeVisible();
}

/** Moves to one of the campaign tabs. */
export async function openTab(page: Page, name: string) {
  await page.getByRole("button", { name, exact: true }).click();
}
