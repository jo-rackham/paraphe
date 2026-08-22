// What a campaign's address SAYS to whoever lands on it without an account,
// and the door back to the apex that hosts it.
//
// A subdomain used to open on a bare « Connexion » form: nothing said whose
// space it was, what the tool does, or that an instance with a public home
// exists one label up. The sign-in screen now introduces the campaign in a
// sentence — the candidate when the field is filled, never the template's
// « Prénom NOM » — and the apex is linked where the instance names one,
// built on the page's own scheme and port (an instance is reachable as this
// very page was reached).
//
// The API is mocked: the shape of /api/config is the fixture's.

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as API from "./api.ts";
import Team from "./Team.tsx";
import { teamConfig, who } from "./testing/fixtures.ts";
import type { ServerConfig } from "./types.ts";

const { APIError: API_ERROR } =
  await vi.importActual<typeof import("./api.ts")>("./api.ts");

vi.mock("./api.ts", { spy: true });

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

/** The href instanceApex must build: the page's own scheme and port. */
const APEX_HREF = (() => {
  const port = window.location.port ? `:${window.location.port}` : "";
  return `${window.location.protocol}//paraphe.test${port}/`;
})();

let container: HTMLDivElement;
let root: Root;

const flush = () =>
  act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });

const apexLinks = () =>
  [...container.querySelectorAll<HTMLAnchorElement>("a[href]")].filter(
    (a) => a.getAttribute("href") === APEX_HREF,
  );

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => {
    root.unmount();
  });
  container.remove();
  vi.resetAllMocks();
});

/** The screen a visitor with no session lands on. */
async function signInScreen(over: Partial<ServerConfig> = {}) {
  vi.mocked(API.me).mockRejectedValue(
    new API_ERROR(401, "Session absente ou expirée."),
  );
  await act(async () => {
    root.render(<Team config={teamConfig(over)} />);
  });
  await flush();
}

const hosted = {
  base_domain: "paraphe.test",
  organisation: { slug: "camille2027", name: "Avec Camille", listed: true },
};

describe("what the sign-in screen says about the campaign", () => {
  it("names the candidate once the field is filled", async () => {
    await signInScreen(hosted);
    // the fixture fills every key with « valeur de <clé> »
    expect(container.textContent).toContain(
      "la campagne de valeur de candidat, valeur de candidat_description",
    );
    expect(container.textContent).toContain("parrainages");
  });

  // The template's « Prénom NOM » must reach no screen as if it were a
  // name: an unconfigured campaign is introduced by its moderated NAME,
  // which every campaign born of a hosting request has.
  it("falls back to the campaign's name while the candidate is a template", async () => {
    await signInScreen({
      ...hosted,
      unfilled: ["candidat", "candidat_description"],
    });
    expect(container.textContent).toContain("la campagne « Avec Camille »");
    expect(container.textContent).not.toContain("valeur de candidat");
  });

  it("still says what the space is when nothing is filled in", async () => {
    await signInScreen({
      ...hosted,
      organisation: { slug: "camille2027", name: "", listed: true },
      unfilled: ["candidat", "candidat_description"],
    });
    expect(container.textContent).toContain("campagne en préparation");
    expect(container.textContent).toContain("parrainages");
  });
});

describe("the door back to the apex", () => {
  it("is on the sign-in screen AND in the footer, on the page's own scheme and port", async () => {
    await signInScreen(hosted);
    // one in the presentation, one in the footer: the second is what
    // follows the volunteer onto every signed-in screen
    expect(apexLinks().length).toBeGreaterThanOrEqual(2);
    expect(
      container.querySelector("footer")?.textContent,
      "the footer names the apex",
    ).toContain("paraphe.test");
  });

  it("stays in the footer once signed in", async () => {
    await signInScreen(hosted);
    vi.mocked(API.signIn).mockResolvedValueOnce(
      who("alice@exemple.fr", "Alice Bénévole"),
    );
    vi.mocked(API.detectMode).mockResolvedValue({
      kind: "team",
      config: teamConfig(hosted),
    });
    const [email, password] = container.querySelectorAll("input");
    const set = Object.getOwnPropertyDescriptor(
      window.HTMLInputElement.prototype,
      "value",
    )?.set;
    await act(async () => {
      set?.call(email, "alice@exemple.fr");
      email.dispatchEvent(new Event("input", { bubbles: true }));
      set?.call(password, "mot-de-passe");
      password.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => {
      [...container.querySelectorAll("button")]
        .find((b) => b.textContent?.includes("Se connecter"))
        ?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flush();
    await flush();

    expect(container.textContent).toContain("Mon tableau");
    const inFooter = [
      ...(container
        .querySelector("footer")
        ?.querySelectorAll<HTMLAnchorElement>("a[href]") ?? []),
    ].filter((a) => a.getAttribute("href") === APEX_HREF);
    expect(inFooter).toHaveLength(1);
  });

  // A single-campaign instance has no apex: every host serves the campaign,
  // and a « door back » would point at the very page it is on. The tool is
  // still introduced — the link is what goes, not the sentence.
  it("is absent on a single-campaign instance", async () => {
    await signInScreen({ base_domain: "" });
    expect(apexLinks()).toHaveLength(0);
    expect(container.textContent).toContain("un outil libre");
  });
});
