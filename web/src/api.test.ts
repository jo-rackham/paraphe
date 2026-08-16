// The mode must never switch on its own.
//
// An intercepted answer on /api/config (Wi-Fi portal, proxy, 502) must
// not flip a team instance into browser mode: the volunteer would work in
// their browser, on the team's origin, nothing reaching the server, and
// their nominative work would stay on the computer after they sign out.

import { afterEach, describe, expect, it, vi } from "vitest";
import { dashboard, detectMode, me, SESSION_LOST } from "./api.ts";
import type { ServerConfig } from "./types.ts";

const CONFIG: ServerConfig = {
  mode: "team",
  campaign: { candidat: "Camille" },
  batch_size: 10,
  unfilled: [],
  source_url: "",
  no_account: false,
  logo: null,
  statuses: [{ key: "to_contact", label: "À contacter", colour: "#eee" }],
  ranks: [{ key: "has_endorsed", label: "A déjà parrainé" }],
};

const mark = (value: string | null) => {
  document.head.innerHTML =
    value === null ? "" : `<meta name="paraphe-mode" content="${value}">`;
};

const respond = (
  body: unknown,
  { ok = true, status = 200, type = "application/json" } = {},
) => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue({
      ok,
      status,
      headers: { get: () => type },
      json: async () => body,
    }),
  );
};

afterEach(() => {
  vi.unstubAllGlobals();
  document.head.innerHTML = "";
});

describe("the page marked by the API", () => {
  it("enters team mode when the configuration is usable", async () => {
    mark("team");
    respond(CONFIG);
    expect((await detectMode()).kind).toBe("team");
  });

  it.each([
    ["a Wi-Fi portal answering HTML", { type: "text/html" }],
    ["a failing gateway", { ok: false, status: 502 }],
    ["a 404 on the API", { ok: false, status: 404 }],
  ])("reports an outage on %s, without switching", async (_case, options) => {
    mark("team");
    respond({ error: "…" }, options);
    expect((await detectMode()).kind).toBe("outage");
  });

  it("reports an outage on a truncated configuration", async () => {
    mark("team");
    respond({ ...CONFIG, statuses: undefined });
    expect((await detectMode()).kind).toBe("outage");
  });

  it("reports an outage when the network drops", async () => {
    mark("team");
    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new TypeError("Failed to fetch")),
    );
    expect((await detectMode()).kind).toBe("outage");
  });
});

describe("the lost-session signal", () => {
  const watch = async (call: () => Promise<unknown>) => {
    respond(
      { error: "Session absente ou expirée." },
      { ok: false, status: 401 },
    );
    let signalled = false;
    const listen = () => {
      signalled = true;
    };
    window.addEventListener(SESSION_LOST, listen);
    try {
      await call().catch(() => {});
    } finally {
      window.removeEventListener(SESSION_LOST, listen);
    }
    return signalled;
  };

  // "who am I?" is asked BEFORE any session: its 401 is a visitor's normal
  // state. Announced as an expiry, it greeted every volunteer with "votre
  // session a expiré" on a pristine browser.
  it("stays silent on the 401 of /api/me at load", async () => {
    expect(await watch(me)).toBe(false);
  });

  // any call that assumes an open session, however, must bring back the
  // form: without it, the screen stayed on an endless "Chargement…"
  it("signals the 401 of a call that assumes a session", async () => {
    expect(await watch(dashboard)).toBe(true);
  });
});

describe("the apex of a multi-campaign instance", () => {
  const INSTANCE = {
    mode: "instance",
    base_domain: "paraphe.fr",
    source_url: "",
    no_account: false,
    logo: null,
    campaign_keys: ["candidat"],
  };

  it("serves the instance landing page, not a campaign", async () => {
    mark("team");
    respond(INSTANCE);
    expect((await detectMode()).kind).toBe("instance");
  });

  // the marker says "an API answers", not "a campaign is served": without
  // this case, an instance apex viewed through Vite would switch to
  // browser mode and offer to work on data that is not there
  it("also announces itself when the page is unmarked", async () => {
    mark(null);
    respond(INSTANCE);
    expect((await detectMode()).kind).toBe("instance");
  });
});

describe("the UNMARKED page", () => {
  it("falls into browser mode when nothing answers", async () => {
    mark(null);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new TypeError("Failed to fetch")),
    );
    expect((await detectMode()).kind).toBe("browser");
  });

  it("falls into browser mode on a 404 page (GitHub Pages)", async () => {
    mark(null);
    respond({}, { ok: false, status: 404, type: "text/html" });
    expect((await detectMode()).kind).toBe("browser");
  });

  // development serves the interface through Vite and proxies /api: the
  // marker is absent, but the API does answer
  it("still enters team mode if an API answers", async () => {
    mark(null);
    respond(CONFIG);
    expect((await detectMode()).kind).toBe("team");
  });
});
