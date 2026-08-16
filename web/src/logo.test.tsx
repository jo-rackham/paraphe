import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ChampLogo, LOGO_MAX_BYTES, Marque } from "./common.tsx";
import { fetchCampaign } from "./prefill.ts";

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

  it("treats a campaign with no logo as ordinary", async () => {
    answer(null);
    const offer = await fetchCampaign("camille2027");
    expect(offer.logo).toBe("");
    expect(offer.campaign.candidat).toBe("rempli");
  });
});
