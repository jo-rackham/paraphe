// The way OUT of the team version, offered by the team version itself.
//
// A visitor landing on a campaign's address with no account has two doors:
// ask for one, or work alone in their browser. The second one is only worth
// offering if it carries the CAMPAIGN — otherwise it hands over nine fields
// to retype, and a typo in any of them goes out to mayors under the
// campaign's name. The API builds that link (`?org=<slug>`); what is decided
// here is whether the screens show it, and only where it leads somewhere.
//
// The API is mocked: the e2e journey drives the real one, end to end.

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as API from "./api.ts";
import Team from "./Team.tsx";
import { teamConfig, who } from "./testing/fixtures.ts";

const { APIError: API_ERROR } =
  await vi.importActual<typeof import("./api.ts")>("./api.ts");

vi.mock("./api.ts", { spy: true });

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

const OFFERED = "/navigateur/?org=camille2027";

let container: HTMLDivElement;
let root: Root;

const flush = () =>
  act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });

const links = () => [
  ...container.querySelectorAll<HTMLAnchorElement>("a[href]"),
];
const linkTo = (href: string) =>
  links().filter((a) => a.getAttribute("href") === href);

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
async function signInScreen(
  browserVersionUrl: string,
  unfilled: string[] = [],
) {
  vi.mocked(API.me).mockRejectedValue(
    new API_ERROR(401, "Session absente ou expirée."),
  );
  await act(async () => {
    root.render(
      <Team
        config={teamConfig({
          browser_version_url: browserVersionUrl,
          unfilled,
        })}
      />,
    );
  });
  await flush();
}

describe("the account-less version, offered from a campaign", () => {
  it("is on the sign-in screen, and it names the campaign", async () => {
    await signInScreen(OFFERED);

    const card = [...container.querySelectorAll("section.carte")].find((s) =>
      s.textContent?.includes("Sans compte"),
    );
    expect(card).toBeDefined();
    // The parameter is the whole point: without it the volunteer arrives on
    // an empty configuration and retypes the campaign by hand.
    const open = card?.querySelector("a");
    expect(open?.getAttribute("href")).toBe(OFFERED);
    expect(open?.textContent).toContain("Ouvrir la version navigateur");
  });

  // What the card must not do is sell coordination it cannot provide. The
  // team version is what keeps two volunteers off the same mayor; this one
  // does not know the other volunteer exists.
  it("says what it costs, on the same screen", async () => {
    await signInScreen(OFFERED);
    expect(container.textContent).toContain("rien n'est coordonné");
  });

  // The other half of the assertion below, and the reason it is written:
  // « n'annonce pas » alone stays green when the sentence goes away
  // entirely. What the card promises a CONFIGURED campaign is that the
  // texts are already there — no step, no confirmation. That version
  // arrives filled in, so a card describing a screen to accept first would
  // be a promise nobody can keep.
  it("promises a configured campaign that its details are already in", async () => {
    await signInScreen(OFFERED);
    expect(container.textContent).toContain("déjà remplies");
  });

  // A campaign still at its template values pre-fills NOTHING: the API
  // refuses it with a 409 rather than spread « Prénom NOM » to volunteers
  // who have no way of knowing. The card promised the pre-fill anyway, and
  // the same /api/config that carries this link carries `unfilled` — so the
  // promise was one its reader discovered by paying for it.
  it("promises the pre-fill only where it will happen", async () => {
    await signInScreen(OFFERED, ["candidat", "signataire"]);
    expect(container.textContent).not.toContain("déjà remplies");
    expect(container.textContent).toContain("valeurs d'exemple");
    // …and the door is still open: the tool works, it is the pre-fill that
    // does not
    expect(
      linkTo(OFFERED),
      "the link itself must survive an unconfigured campaign",
    ).not.toHaveLength(0);
  });

  // An <a>, never a button dressed as one: /navigateur/ is a second build
  // of this application, outside the single page. A handler calling
  // `navigate` would look identical and land on a view that does not exist.
  it("is a real link out of the application", async () => {
    await signInScreen(OFFERED);
    const opening = [...container.querySelectorAll("button")].filter((b) =>
      b.textContent?.includes("version navigateur"),
    );
    expect(opening).toHaveLength(0);
  });

  // Every screen, once signed in: the footer carries it too. A volunteer
  // who wants to keep working on a train has no reason to sign out first to
  // find the door.
  it("stays reachable from the footer once signed in", async () => {
    await signInScreen(OFFERED);
    expect(
      container.querySelector("footer .sans-compte a")?.getAttribute("href"),
    ).toBe(OFFERED);

    vi.mocked(API.signIn).mockResolvedValueOnce(
      who("alice@exemple.fr", "Alice Bénévole"),
    );
    vi.mocked(API.detectMode).mockResolvedValue({
      kind: "team",
      config: teamConfig({ browser_version_url: OFFERED }),
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
    expect(
      container.querySelector("footer .sans-compte a")?.getAttribute("href"),
    ).toBe(OFFERED);
  });

  // An instance serving no browser version answers with an empty string,
  // and an <a> with no href is a link that looks like one and does nothing.
  it("is absent, card and footer, when the instance offers none", async () => {
    await signInScreen("");

    expect(container.textContent).not.toContain("Sans compte");
    expect(container.querySelector("footer .sans-compte")).toBeNull();
    expect(
      links().some((a) => a.getAttribute("href")?.includes("navigateur")),
    ).toBe(false);
  });

  // The same refusal the footer's source link already makes: a setting is
  // one an operator can get wrong, and `javascript:` in an href is a string
  // the campaign chose and this browser would execute.
  it("refuses a href that is not http(s)", async () => {
    await signInScreen("javascript:alert(1)");

    expect(linkTo("javascript:alert(1)")).toHaveLength(0);
    expect(container.textContent).not.toContain("Sans compte");
  });
});
