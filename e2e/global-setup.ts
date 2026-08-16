import { type ChildProcess, spawn, spawnSync } from "node:child_process";
import { cpSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { createConnection } from "node:net";
import { join } from "node:path";
import pg from "pg";

import {
  API_ORIGIN,
  API_PORT,
  BASE_DOMAIN,
  COORDINATION,
  DB_NAME,
  DB_PASSWORD,
  DB_ROLE,
  FIRST_CAMPAIGN,
  INSTANCE_ADMIN,
  MEDIA,
  mediaConfigured,
  ROOT,
  STATIC_ORIGIN,
  STATIC_PORT,
  WORK_DIR,
} from "./config.ts";

// Boots the whole thing the way production does: a real PostgreSQL, the real
// Go binary, the real built interface. Nothing is mocked — these tests exist
// to catch what unit tests structurally cannot, such as a JSON key that the
// API renames and the interface still expects.

const adminDsn = (process.env.PARAPHE_TEST_DATABASE_URL ?? "").trim();

function run(command: string, args: string[], env: NodeJS.ProcessEnv = {}) {
  const result = spawnSync(command, args, {
    cwd: ROOT,
    encoding: "utf8",
    env: { ...process.env, ...env },
  });
  if (result.status !== 0) {
    throw new Error(
      `${command} ${args.join(" ")} failed (${result.status}):\n` +
        `${result.stdout ?? ""}${result.stderr ?? ""}`,
    );
  }
  return result.stdout ?? "";
}

/** Creates the throwaway database and its unprivileged owner. */
async function prepareDatabase() {
  const admin = new pg.Client({ connectionString: adminDsn });
  await admin.connect();
  try {
    await admin.query(`DROP DATABASE IF EXISTS ${DB_NAME}`);
    await admin.query(
      `DO $$ BEGIN
         IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='${DB_ROLE}') THEN
           CREATE ROLE ${DB_ROLE} LOGIN PASSWORD '${DB_PASSWORD}'
             NOSUPERUSER NOBYPASSRLS;
         END IF;
       END $$`,
    );
    await admin.query(`CREATE DATABASE ${DB_NAME} OWNER ${DB_ROLE}`);
  } finally {
    await admin.end();
  }

  const created = new pg.Client({
    connectionString: adminDsn.replace(/\/[^/?]+(\?|$)/, `/${DB_NAME}$1`),
  });
  await created.connect();
  try {
    await created.query(`GRANT CREATE, USAGE ON SCHEMA public TO ${DB_ROLE}`);
  } finally {
    await created.end();
  }
}

function appDsn() {
  const url = new URL(adminDsn);
  url.username = DB_ROLE;
  url.password = DB_PASSWORD;
  url.pathname = `/${DB_NAME}`;
  return url.toString();
}

/**
 * A leftover server from a crashed run keeps its port, OUR server dies on
 * EADDRINUSE — and waitFor() then accepts the stranger's answer: the whole
 * suite certifies a build it is not serving. Refuse loud instead.
 */
function refuseOccupiedPort(port: number, what: string) {
  // BOTH address families: the origins resolve through `localhost`, which
  // many hosts map to ::1 as well — a v6-only leftover would slip past a
  // v4-only probe and still answer waitFor() in our place
  const families = ["127.0.0.1", "::1"];
  return Promise.all(
    families.map(
      (host) =>
        new Promise<void>((resolve, reject) => {
          const probe = createConnection({ host, port }, () => {
            probe.destroy();
            reject(
              new Error(
                `port ${port} (${host}) is already taken: a previous run's ` +
                  `${what} is still alive, and the suite would test ITS ` +
                  "files, not this build's. Kill it, then re-run.",
              ),
            );
          });
          // nothing listening — or no IPv6 on this host: the port is ours
          probe.on("error", () => resolve());
          // a firewalled port answers nothing at all: do not hang the suite
          probe.setTimeout(2000, () => {
            probe.destroy();
            resolve();
          });
        }),
    ),
  );
}

async function waitFor(url: string, what: string) {
  const deadline = Date.now() + 90_000;
  for (;;) {
    try {
      const response = await fetch(url);
      if (response.ok) return;
    } catch {
      // not up yet
    }
    if (Date.now() > deadline)
      throw new Error(`${what} never answered on ${url}`);
    await new Promise((r) => setTimeout(r, 250));
  }
}

function record(children: Record<string, ChildProcess>) {
  const pids: Record<string, number> = {};
  for (const [name, child] of Object.entries(children)) {
    if (child.pid) pids[name] = child.pid;
  }
  writeFileSync(join(WORK_DIR, "pids.json"), JSON.stringify(pids), "utf8");
}

export default async function globalSetup() {
  if (!adminDsn) {
    throw new Error(
      "PARAPHE_TEST_DATABASE_URL is required: the end-to-end suite needs a " +
        "throwaway PostgreSQL it may drop databases on. Locally: `task db`.",
    );
  }
  rmSync(WORK_DIR, { recursive: true, force: true });
  mkdirSync(WORK_DIR, { recursive: true });

  // A synthetic list rather than the real 34 826 mayors: it builds in
  // milliseconds, it carries no personal data, and it already clears the
  // import floor the API enforces against a truncated CSV.
  const dataDir = join(WORK_DIR, "data");
  mkdirSync(dataDir, { recursive: true });
  run("node", ["outils/faux-jeu.ts", dataDir]);

  // PARAPHE_BASE_PATH matters: the interface defaults to the /paraphe/ prefix of
  // its GitHub Pages publication, and a build without it asks the API for
  // assets under a path it does not serve — a blank page, no error.
  run("pnpm", ["--dir", "web", "run", "build"], { PARAPHE_BASE_PATH: "/" });
  const apiBinary = join(WORK_DIR, "paraphe-api");
  run("go", ["build", "-C", "api", "-o", apiBinary, "."]);

  await prepareDatabase();

  await refuseOccupiedPort(API_PORT, "API");
  await refuseOccupiedPort(STATIC_PORT, "static server");

  const api = spawn(apiBinary, [], {
    cwd: ROOT,
    stdio: ["ignore", "pipe", "pipe"],
    env: {
      ...process.env,
      PARAPHE_DATABASE_URL: appDsn(),
      PARAPHE_CSV: join(dataDir, "04_base_complete.csv"),
      PARAPHE_WEB_DIR: join(ROOT, "web", "dist"),
      PARAPHE_HOST: "127.0.0.1",
      PARAPHE_PORT: String(API_PORT),
      PARAPHE_BASE_DOMAIN: BASE_DOMAIN,
      PARAPHE_ORG_SLUG: FIRST_CAMPAIGN,
      PARAPHE_ADMIN_EMAIL: COORDINATION.email,
      PARAPHE_ADMIN_PASSWORD: COORDINATION.password,
      PARAPHE_INSTANCE_ADMIN_EMAIL: INSTANCE_ADMIN.email,
      PARAPHE_INSTANCE_ADMIN_PASSWORD: INSTANCE_ADMIN.password,
      PARAPHE_SECRET_KEY: "e2e-session-key-0123456789abcdef0123456789abcdef",
      // Passed through only when the run was given a store. Half of these
      // would fail the API's start, which is the point of that refusal —
      // so it is all five or none, exactly as an operator faces it.
      ...(mediaConfigured()
        ? {
            PARAPHE_MEDIA_ENDPOINT: MEDIA.endpoint,
            PARAPHE_MEDIA_BUCKET: MEDIA.bucket,
            PARAPHE_MEDIA_ACCESS_KEY: MEDIA.accessKey,
            PARAPHE_MEDIA_SECRET_KEY: MEDIA.secretKey,
            PARAPHE_MEDIA_PUBLIC_URL: MEDIA.publicUrl,
          }
        : {}),
    },
  });
  const log: string[] = [];
  api.stdout.on("data", (b) => log.push(String(b)));
  api.stderr.on("data", (b) => log.push(String(b)));
  api.on("exit", (code) => {
    if (code !== 0 && code !== null) {
      console.error(`the API died (${code}):\n${log.join("")}`);
    }
  });

  // The browser-mode application is served as a plain static directory, the
  // way GitHub Pages serves it — with the published lists beside it, since
  // that mode reads them straight from the CSV.
  const staticDir = join(WORK_DIR, "static");
  cpSync(join(ROOT, "web", "dist"), staticDir, { recursive: true });
  cpSync(dataDir, join(staticDir, "donnees"), { recursive: true });
  const statics = spawn(
    "node",
    [join(ROOT, "e2e", "static-server.mjs"), staticDir, String(STATIC_PORT)],
    { stdio: ["ignore", "pipe", "pipe"] },
  );

  record({ api, statics });
  await waitFor(`${API_ORIGIN}/health/db`, "the API");
  await waitFor(`${STATIC_ORIGIN}/index.html`, "the static server");
}
