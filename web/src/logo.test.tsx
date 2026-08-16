import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ChampLogo, LOGO_MAX_BYTES, Marque } from "./common.tsx";
import * as DB from "./db.ts";
import { fetchCampaign, inlineLogo } from "./prefill.ts";

// The campaign logo, on the three points where getting it wrong is not
// visible: the header falling back when the object store is down, the field
// refusing a file before it is sent, and the address an offered campaign is
// allowed to put into an <img>.

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

const LOGO = {
  url: "https://media.exemple.fr/logos/c/abc.png",
  type: "image/png",
};

describe("the header's mark", () => {
  it("keeps the hexagon whatever a campaign uploads", () => {
    // The style sheet's first line: this identity is deliberately not the
    // State's, and on a shared instance one campaign taking over the whole
    // mark is the squat the moderation exists to prevent.
    act(() => root.render(<Marque logo={LOGO} sous="Marie Dupont" />));
    expect(container.querySelector("svg")).not.toBeNull();
    expect(container.textContent).toContain("paraphe");
    expect(container.textContent).toContain("Marie Dupont");
  });

  it("shows the campaign's logo as DECORATIVE", () => {
    act(() => root.render(<Marque logo={LOGO} sous="Marie Dupont" />));
    const img = container.querySelector("img.logo-campagne");
    expect(img).not.toBeNull();
    expect(img?.getAttribute("src")).toBe(LOGO.url);
    // the campaign's name is already beside it in text: an alt would make a
    // screen reader announce the same campaign twice
    expect(img?.getAttribute("alt")).toBe("");
  });

  it("falls back to the hexagon when the image cannot be fetched", () => {
    // The object store is a separate origin and a separate failure: down,
    // it must cost the picture and nothing else — above all not the
    // browser's broken-image glyph in the header of every page.
    act(() => root.render(<Marque logo={LOGO} sous="Marie Dupont" />));
    const img = container.querySelector("img.logo-campagne");
    act(() => {
      img?.dispatchEvent(new Event("error", { bubbles: false }));
    });
    expect(container.querySelector("img.logo-campagne")).toBeNull();
    expect(container.querySelector("svg")).not.toBeNull();
    expect(container.textContent).toContain("Marie Dupont");
  });

  it("tries again when the logo changes", () => {
    // a new upload is a new URL: a failure must not condemn its successor
    act(() => root.render(<Marque logo={LOGO} sous="x" />));
    act(() => {
      container
        .querySelector("img.logo-campagne")
        ?.dispatchEvent(new Event("error"));
    });
    expect(container.querySelector("img.logo-campagne")).toBeNull();
    act(() =>
      root.render(
        <Marque logo={{ ...LOGO, url: LOGO.url + "?v=2" }} sous="x" />,
      ),
    );
    expect(container.querySelector("img.logo-campagne")).not.toBeNull();
  });

  it("renders nothing but the mark when there is no logo", () => {
    act(() => root.render(<Marque sous="version navigateur" />));
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("svg")).not.toBeNull();
  });
});

/** Fires a file selection the way a browser does. */
function choisir(input: HTMLInputElement, file: File) {
  Object.defineProperty(input, "files", {
    value: { 0: file, length: 1, item: (i: number) => (i === 0 ? file : null) },
    configurable: true,
  });
  act(() => {
    input.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

describe("the logo field", () => {
  it("refuses a file over the ceiling without sending it", async () => {
    const onChoisi = vi.fn();
    const onErreur = vi.fn();
    act(() =>
      root.render(
        <ChampLogo
          logo=""
          onChoisi={onChoisi}
          onRetire={vi.fn()}
          onErreur={onErreur}
        />,
      ),
    );
    const input = container.querySelector<HTMLInputElement>("#champ-logo");
    if (!input) throw new Error("no field");
    choisir(
      input,
      new File([new Uint8Array(LOGO_MAX_BYTES + 1)], "gros.png", {
        type: "image/png",
      }),
    );
    // refused HERE, by the screen that holds the file: the same ceiling
    // exists server-side, but a volunteer should not upload 4 MB to learn it
    expect(onChoisi).not.toHaveBeenCalled();
    expect(onErreur).toHaveBeenCalledTimes(1);
    expect(String(onErreur.mock.calls[0][0])).toContain("Ko");
  });

  it("accepts only the four formats the API accepts", () => {
    act(() =>
      root.render(
        <ChampLogo
          logo=""
          onChoisi={vi.fn()}
          onRetire={vi.fn()}
          onErreur={vi.fn()}
        />,
      ),
    );
    const accept =
      container.querySelector<HTMLInputElement>("#champ-logo")?.accept ?? "";
    for (const type of [
      "image/png",
      "image/jpeg",
      "image/webp",
      "image/svg+xml",
    ]) {
      expect(accept).toContain(type);
    }
    expect(accept).not.toContain("image/gif");
  });

  it("hands focus back before the removal button unmounts", () => {
    // House rule: a control never vanishes under the user's focus —
    // `disabled` or an unmount drops keyboard focus to <body> in every
    // browser, and the next Tab restarts at the top of the page.
    const onRetire = vi.fn();
    act(() =>
      root.render(
        <ChampLogo
          logo="data:image/png;base64,iVBORw0KGgo="
          onChoisi={vi.fn()}
          onRetire={onRetire}
          onErreur={vi.fn()}
        />,
      ),
    );
    const bouton = [...container.querySelectorAll("button")].find((b) =>
      b.textContent?.includes("Retirer"),
    );
    expect(bouton).toBeDefined();
    act(() => bouton?.click());
    expect(onRetire).toHaveBeenCalledTimes(1);
    expect(document.activeElement).toBe(container.querySelector("#champ-logo"));
  });

  it("removes ONCE when the button is clicked twice in the same tick", async () => {
    // House rule, paid for twice in this project: the guard is a REF, never
    // a prop or a state. Two clicks in one tick run two handlers built by
    // the same render, and both read `occupe` as it was before either.
    const onRetire = vi.fn();
    act(() =>
      root.render(
        <ChampLogo
          logo="data:image/png;base64,iVBORw0KGgo="
          onChoisi={vi.fn()}
          onRetire={onRetire}
          onErreur={vi.fn()}
        />,
      ),
    );
    const bouton = [...container.querySelectorAll("button")].find((b) =>
      b.textContent?.includes("Retirer"),
    );
    await act(async () => {
      bouton?.click();
      bouton?.click();
    });
    expect(onRetire).toHaveBeenCalledTimes(1);
  });

  it("refuses a file the picker was talked into showing", async () => {
    // `accept` filters the dialog, and a dialog can be told to show
    // everything. In browser mode nothing else reads these bytes: a text
    // file chosen by mistake became a logo that vanished at the next
    // reload, saying nothing.
    const onChoisi = vi.fn();
    const onErreur = vi.fn();
    act(() =>
      root.render(
        <ChampLogo
          logo=""
          onChoisi={onChoisi}
          onRetire={vi.fn()}
          onErreur={onErreur}
        />,
      ),
    );
    const input = container.querySelector<HTMLInputElement>("#champ-logo");
    if (!input) throw new Error("no field");
    choisir(input, new File(["bonjour"], "notes.txt", { type: "text/plain" }));
    expect(onChoisi).not.toHaveBeenCalled();
    expect(String(onErreur.mock.calls[0][0])).toContain("text/plain");
  });

  it("shows no preview and no removal when there is no logo", () => {
    act(() =>
      root.render(
        <ChampLogo
          logo=""
          onChoisi={vi.fn()}
          onRetire={vi.fn()}
          onErreur={vi.fn()}
        />,
      ),
    );
    expect(container.querySelector(".apercu-logo")).toBeNull();
  });
});

describe("a campaign offered by ?org=", () => {
  const campaign = Object.fromEntries(
    [
      "candidat",
      "candidat_description",
      "candidat_description_longue",
      "signataire",
      "signataire_qualite",
      "contact_tel",
      "contact_email",
      "site",
      "ville_envoi",
    ].map((k) => [k, "rempli"]),
  );

  const answer = (logo: unknown) =>
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({
        ok: true,
        status: 200,
        json: async () => ({ name: "Marie Dupont", campaign, logo }),
      })),
    );

  afterEach(() => vi.unstubAllGlobals());

  it("keeps an http(s) logo", async () => {
    answer({ url: "https://media.exemple.fr/logos/c/abc.png" });
    const offer = await fetchCampaign("camille2027");
    expect(offer.logo).toBe("https://media.exemple.fr/logos/c/abc.png");
  });

  it("drops anything that is not an http(s) address", async () => {
    // This value goes straight into an <img src>. The campaign is remote and
    // an intermediary can answer for it, so a scheme it chose is a scheme
    // this browser would otherwise run.
    for (const url of [
      "javascript:alert(1)",
      "data:text/html,<script>alert(1)</script>",
      "file:///etc/passwd",
      42,
      null,
      {},
    ]) {
      answer({ url });
      const offer = await fetchCampaign("camille2027");
      expect(offer.logo, String(url)).toBe("");
    }
  });

  it("refuses to inline what the store answered if it is not an image", async () => {
    // The last check before the bytes become a data URI this browser keeps
    // for ever. Removing it left the whole suite green, so it guarded
    // nothing: an answer of `text/html` would have been stored as one.
    // REAL blobs: a stub object would fail to be read at all, and the
    // assertion would then hold for a reason that has nothing to do with
    // the check it names — which is exactly what the first version of this
    // test did, and the mutation stayed green.
    for (const [type, size] of [
      ["text/html", 10],
      ["", 10],
      ["image/png", LOGO_MAX_BYTES + 1],
    ] as const) {
      const blob = new Blob([new Uint8Array(size)], { type });
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => ({ ok: true, status: 200, blob: async () => blob })),
      );
      await expect(
        inlineLogo("https://media.exemple.fr/logos/c/abc.png"),
        `${type} ${size}`,
      ).rejects.toThrow();
    }
  });

  it("treats a campaign with no logo as ordinary", async () => {
    answer(null);
    const offer = await fetchCampaign("camille2027");
    expect(offer.logo).toBe("");
    expect(offer.campaign.candidat).toBe("rempli");
  });
});

// `usableLogo` checks a PREFIX, not a document: `data:image/svg+xml,<svg
// onload=…>` satisfies it. What makes that harmless is the rendering
// context and nothing else — an <img> renders SVG in secure static mode, so
// no script runs, no external reference is followed. Verified in Chromium
// against the built bundle, including background-image, <object>, <embed>
// and a nested <image href="data:image/svg+xml,…">.
//
// So the invariant is "the logo only ever reaches a src", and it lived
// nowhere but in that verification. A print stylesheet, a share sheet or a
// drag preview would open it again — in the one mode published on GitHub
// Pages, where there is no Content-Security-Policy to catch anything.
describe("where a logo is allowed to be rendered", () => {
  // jsdom rewrites import.meta.url to an http:// URL, so the directory is
  // found from the working directory — and an empty scan THROWS: a canary
  // over nothing passes while proving nothing.
  let dir = "";
  for (let d = process.cwd(), i = 0; i < 4; i++, d = dirname(d)) {
    for (const c of [join(d, "src"), join(d, "web", "src")]) {
      if (existsSync(join(c, "common.tsx"))) dir = c;
    }
    if (dir) break;
  }
  if (!dir) throw new Error("web/src not found from " + process.cwd());
  const sources = readdirSync(dir)
    .filter((f) => /\.(tsx|ts|css)$/.test(f) && !f.includes(".test."))
    .map((f) => [f, readFileSync(join(dir, f), "utf8")] as const);
  if (sources.length < 5) throw new Error("nothing was scanned");

  it("is a src attribute, and nowhere else", () => {
    // `logo={logo}` passes it down, `src={logo}` renders it; anything else
    // — href, data, style, srcDoc — hands it to a context that executes.
    const passing = /(\w+)=\{[^{}]*\blogo\b[^{}]*\}/g;
    const allowed = new Set(["src", "logo", "key"]);
    for (const [name, text] of sources) {
      for (const [whole, attribute] of text.matchAll(passing)) {
        expect(allowed.has(attribute), `${name}: ${whole}`).toBe(true);
      }
    }
  });

  it("is never a CSS background", () => {
    // A background renders the same bytes in a context where SVG onload
    // has fired, historically, in more than one engine.
    for (const [name, text] of sources) {
      expect(/background[^;]*logo/i.test(text), name).toBe(false);
    }
  });
});

describe("a logo carried by a backup file", () => {
  // The GUIDE tells volunteers to exchange these files to share their
  // tracking. One tampered export otherwise turns every teammate's reload
  // into a beacon: the value becomes an <img src>, fetched on every render,
  // for ever. Published on GitHub Pages there is no Content-Security-Policy
  // to catch it, and « aucune donnée ne quitte ce navigateur » is the whole
  // promise of that build.
  const backup = (logo: unknown) => ({
    format: "paraphe/1",
    exported_at: "2026-08-16",
    mayors: [],
    tracking: [],
    settings: [{ key: "logo", value: logo }],
  });

  it("keeps an inline image", async () => {
    await DB.eraseAll();
    await DB.importAll(backup("data:image/png;base64,iVBORw0KGgo="));
    expect(await DB.readSetting("logo", "")).toBe(
      "data:image/png;base64,iVBORw0KGgo=",
    );
  });

  it("drops a remote address rather than fetch it on every load", async () => {
    for (const hostile of [
      "https://tracker.attaquant.example/pixel.gif?qui=victime",
      "http://192.168.1.1/beacon",
      "//ailleurs.example/x.png",
      "javascript:alert(1)",
      "data:text/html,<script>alert(1)</script>",
      42,
      { url: "https://ailleurs.example" },
    ]) {
      await DB.eraseAll();
      const report = await DB.importAll(backup(hostile));
      expect(await DB.readSetting("logo", ""), String(hostile)).toBe("");
      expect(report.skipped, String(hostile)).toBeGreaterThan(0);
    }
  });
});
