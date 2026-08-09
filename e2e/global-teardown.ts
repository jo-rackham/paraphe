import { readFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import pg from "pg";

import { DB_NAME, WORK_DIR } from "./config.ts";

// Kills by PID, never by pattern: `pkill -f paraphe` also matches the shell
// that launched the suite, and has already taken down a developer's own
// running instance on this project.

export default async function globalTeardown() {
  let pids: Record<string, number> = {};
  try {
    pids = JSON.parse(readFileSync(join(WORK_DIR, "pids.json"), "utf8"));
  } catch {
    // setup never got far enough to record any
  }
  for (const [name, pid] of Object.entries(pids)) {
    try {
      process.kill(pid, "SIGTERM");
    } catch (error) {
      console.warn(`${name} (pid ${pid}) not stopped: ${error}`);
    }
  }

  const adminDsn = (process.env.PARAPHE_TEST_DATABASE_URL ?? "").trim();
  if (adminDsn) {
    const admin = new pg.Client({ connectionString: adminDsn });
    try {
      await admin.connect();
      // the API may still be closing its pool: WITH (FORCE) rather than a
      // failure nobody reads
      await admin.query(`DROP DATABASE IF EXISTS ${DB_NAME} WITH (FORCE)`);
    } catch (error) {
      console.warn(`throwaway database not dropped: ${error}`);
    } finally {
      await admin.end().catch(() => {});
    }
  }
  rmSync(WORK_DIR, { recursive: true, force: true });
}
