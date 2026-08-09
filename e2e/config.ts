import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

// Shared addresses and paths for the end-to-end suite.
//
// Everything lives on `localhost` on purpose: browsers resolve `*.localhost`
// to the loopback address without touching DNS, which is the only way to
// exercise the real thing here — one campaign per subdomain, resolved from the
// Host header.

export const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
export const WORK_DIR = join(ROOT, "e2e", ".tmp");

/** Ports well clear of the ones a developer runs by hand (8047, 5180). */
export const API_PORT = 8399;
export const STATIC_PORT = 8398;

export const BASE_DOMAIN = "localhost";
export const API_ORIGIN = `http://${BASE_DOMAIN}:${API_PORT}`;
export const STATIC_ORIGIN = `http://127.0.0.1:${STATIC_PORT}`;

export const campaignOrigin = (slug: string) =>
  `http://${slug}.${BASE_DOMAIN}:${API_PORT}`;

/** The campaign the instance boots with. */
export const FIRST_CAMPAIGN = "premiere";

export const COORDINATION = {
  email: "coordination@premiere.test",
  password: "coordination-password-not-a-template",
};

export const INSTANCE_ADMIN = {
  email: "admin@instance.test",
  password: "instance-admin-password-not-a-template",
};

/**
 * A throwaway database, dropped and recreated at every run, owned by a role
 * WITHOUT privileges. That last part is not a detail: a superuser walks
 * straight through row-level security, so a suite run as the admin account
 * would certify a wall between campaigns that does not exist in production.
 */
export const DB_NAME = "paraphe_e2e";
export const DB_ROLE = "paraphe_e2e_app";
export const DB_PASSWORD = "paraphe_e2e";
