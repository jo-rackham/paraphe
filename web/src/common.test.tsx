import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Alerte, EMPTY_CFG, Fiche, httpUrl } from "./common.tsx";
import type { Mayor, Message } from "./types.ts";

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  vi.useFakeTimers();
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.useRealTimers();
});

const show = (message: Message | null, onClose: () => void) =>
  act(() => {
    root.render(<Alerte message={message} onClose={onClose} />);
  });

describe("httpUrl", () => {
  it("keeps http(s) and same-origin relative URLs", () => {
    expect(httpUrl("https://github.com/x/y")).toBe("https://github.com/x/y");
    expect(httpUrl("http://example.fr")).toBe("http://example.fr");
    expect(httpUrl("/navigateur/")).toBe("/navigateur/");
  });
  it("rejects a script or data scheme, and the empty value", () => {
    // eslint-disable-next-line no-script-url
    expect(httpUrl("javascript:alert(1)")).toBeUndefined();
    expect(httpUrl("data:text/html,<script>1</script>")).toBeUndefined();
    expect(httpUrl("")).toBeUndefined();
    expect(httpUrl(undefined)).toBeUndefined();
    expect(httpUrl(null)).toBeUndefined();
  });
  // A HOST wearing a path's clothes, and the reason testing the resolved
  // protocol cannot see it: `new URL("//ailleurs.test", origin)` resolves to
  // `https://ailleurs.test`, so the check passed and the raw string went
  // into the href — where the browser reads the same thing and leaves the
  // campaign. It is what an operator's `//` typo does to every sign-in
  // screen and every footer.
  it("rejects a value that names another host without saying so", () => {
    expect(httpUrl("//ailleurs.test/paraphe")).toBeUndefined();
    expect(httpUrl("//ailleurs.test")).toBeUndefined();
    expect(httpUrl("//")).toBeUndefined();
    // …and naming one OUT LOUD is still allowed: that is a browser version
    // published elsewhere, which is a supported deployment.
    expect(httpUrl("https://ailleurs.test/paraphe/")).toBe(
      "https://ailleurs.test/paraphe/",
    );
  });
});

describe("Alerte", () => {
  it("dismisses a success on its own after seven seconds", () => {
    const onClose = vi.fn();
    show({ tone: "ok", text: "Enregistré." }, onClose);
    act(() => vi.advanceTimersByTime(6900));
    expect(onClose).not.toHaveBeenCalled();
    act(() => vi.advanceTimersByTime(200));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("counts from the message, not from the last render", () => {
    // parents pass an inline arrow whose identity changes on every render:
    // a timer armed on the CALLBACK would be reset for ever
    const message: Message = { tone: "ok", text: "Enregistré." };
    const first = vi.fn();
    const second = vi.fn();
    show(message, first);
    act(() => vi.advanceTimersByTime(5000));
    show(message, second);
    act(() => vi.advanceTimersByTime(2100));
    expect(second).toHaveBeenCalledTimes(1);
    expect(first).not.toHaveBeenCalled();
  });

  it("an error stays until acted on", () => {
    const onClose = vi.fn();
    show({ tone: "erreur", text: "Écriture refusée." }, onClose);
    act(() => vi.advanceTimersByTime(60_000));
    expect(onClose).not.toHaveBeenCalled();
  });

  it("a fresh message OBJECT with the same content does not rearm", () => {
    // a caller building the message inline hands a new object every
    // render; keyed on identity, the timer would never fire
    const onClose = vi.fn();
    for (let i = 0; i < 20; i++) {
      show({ tone: "ok", text: "Enregistré." }, onClose);
      act(() => vi.advanceTimersByTime(500));
    }
    expect(onClose).toHaveBeenCalled();
  });
});

describe("Fiche", () => {
  const MAYOR: Mayor = {
    insee_code: "01001",
    commune: "Artemare",
    department: "Ain",
    last_name: "DESCHAMPS",
    first_name: "Roland",
    title: "M.",
    phone: "04 79 87 32 64",
    email: "mairie@artemare.fr",
  };

  // MAYOR alone renders NO message: with no rank column the engine falls
  // back to `has_endorsed`, whose required fields it lacks, so the card
  // shows the error instead of the three channels — and an assertion about
  // message text would read an empty string and pass on anything.
  const SENDABLE: Mayor = {
    ...MAYOR,
    rank: "no_signal",
  };

  const render = (mayor: Mayor) =>
    act(() => {
      root.render(
        <Fiche
          mayor={mayor}
          cfg={EMPTY_CFG}
          onBack={() => {}}
          onStatus={() => {}}
        />,
      );
    });

  // WHO SIGNS, ON ALL THREE CHANNELS. The card excluded the letter while it
  // was written at the candidate's own « je »; signed by whoever posts it,
  // leaving it out puts the CAMPAIGN's signatory at the bottom — which on a
  // campaign configured by its coordination is the coordinator's name, under
  // a letter a volunteer prints and stamps. The engine has always honoured
  // `signer`; this is about the card actually giving it one.
  it("signs the letter with the volunteer, like the email", () => {
    const cfg = { ...EMPTY_CFG, candidat: "Camille Réelle" };
    act(() => {
      root.render(
        <Fiche
          mayor={SENDABLE}
          cfg={cfg}
          signer="Alex Bénévole"
          onBack={() => {}}
          onStatus={() => {}}
        />,
      );
    });
    const letter = container.querySelector("pre.lettre")?.textContent ?? "";
    expect(letter, "the letter is not on screen").toContain(
      "Conseil constitutionnel vous adressera",
    );
    const signature = letter.trimEnd().split("\n").filter(Boolean).at(-2);
    expect(signature).toContain("Alex Bénévole");
    expect(signature).not.toContain(EMPTY_CFG.signataire);
  });

  // OPT-IN: unset means the campaign has not said it telephones, and the
  // card must not promise a call on its behalf.
  it("promises no telephone call unless the campaign said it calls", () => {
    // PER CHANNEL, not pooled. Pooled, the letter alone satisfies « a call is
    // mentioned » and the email's own wiring is pinned by nothing — measured:
    // dropping `phoneOutreach` from the email call left this green.
    const email = () =>
      container.querySelector<HTMLTextAreaElement>("textarea[rows='16']")
        ?.value ?? "";
    const letterText = () =>
      container.querySelector("pre.lettre")?.textContent ?? "";

    render(SENDABLE);
    // the messages ARE on screen — otherwise « does not mention a call »
    // holds because nothing is rendered at all, and the assertion is worth
    // nothing
    expect(email()).toContain("présentations");
    expect(letterText()).toContain("Conseil constitutionnel");
    expect(email()).not.toMatch(/par téléphone/);
    expect(letterText()).not.toMatch(/vous appeler/);
    act(() => {
      root.render(
        <Fiche
          mayor={SENDABLE}
          cfg={EMPTY_CFG}
          phoneOutreach
          onBack={() => {}}
          onStatus={() => {}}
        />,
      );
    });
    expect(email()).toMatch(/par téléphone/);
    expect(letterText()).toMatch(/vous appeler/);
  });

  it("the phone dials: a tel: link carrying digits only", () => {
    render(MAYOR);
    const link = container.querySelector<HTMLAnchorElement>('a[href^="tel:"]');
    expect(link?.getAttribute("href")).toBe("tel:0479873264");
    expect(link?.textContent).toBe("04 79 87 32 64");
  });

  it("two numbers in the field: the link dials the FIRST, never both", () => {
    // stripped blindly, "04… / 06…" concatenated into one twenty-digit
    // string — any ten-digit prefix of which is somebody's real number
    render({ ...MAYOR, phone: "04 79 87 32 64 / 06 12 34 56 78" });
    const link = container.querySelector<HTMLAnchorElement>('a[href^="tel:"]');
    expect(link?.getAttribute("href")).toBe("tel:0479873264");
    expect(link?.textContent).toBe("04 79 87 32 64 / 06 12 34 56 78");
  });

  it("an extension is not part of the number", () => {
    render({ ...MAYOR, phone: "04 79 87 32 64 poste 25" });
    const link = container.querySelector<HTMLAnchorElement>('a[href^="tel:"]');
    expect(link?.getAttribute("href")).toBe("tel:0479873264");
  });

  it("an international number keeps its plus", () => {
    render({ ...MAYOR, phone: "+590 590 12 34 56" });
    const link = container.querySelector<HTMLAnchorElement>('a[href^="tel:"]');
    expect(link?.getAttribute("href")).toBe("tel:+590590123456");
  });

  it("junk that holds no plausible number is text, not a link", () => {
    render({ ...MAYOR, phone: "   " });
    expect(container.querySelector('a[href^="tel:"]')).toBeNull();
  });

  it("no phone, no link — the absence is written out", () => {
    render({ ...MAYOR, phone: "" });
    expect(container.querySelector('a[href^="tel:"]')).toBeNull();
    expect(container.textContent).toContain("non renseigné");
  });

  it("two clicks in the same tick record the status ONCE, not twice", async () => {
    // A re-entry guard is a REF, never state. Two clicks in the same tick run
    // two handlers built by the same render, both read `saving` as false, and
    // both POST — two identical notes in the team's history for one intention,
    // and two racing writes in browser mode. `aria-disabled` greys the button
    // but keeps it live; the guard has to live in the handler.
    const onStatus = vi.fn(() => new Promise<void>(() => {})); // stays in flight
    await act(async () => {
      root.render(
        <Fiche
          mayor={SENDABLE}
          cfg={EMPTY_CFG}
          onBack={() => {}}
          onStatus={onStatus}
        />,
      );
    });
    const save = [...container.querySelectorAll("button")].find((b) =>
      b.textContent?.includes("Enregistrer"),
    );
    expect(save).toBeTruthy();
    await act(async () => {
      save?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      save?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(onStatus).toHaveBeenCalledTimes(1);
  });
});
