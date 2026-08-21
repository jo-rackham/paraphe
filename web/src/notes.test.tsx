// Correcting and removing a history line, on the card the three modes share.
//
// The card is rendered DIRECTLY: what is decided here is what the screen
// offers and what it sends, and each mode's wiring is proven by its own
// end-to-end journey against a real server.
//
// The rule the buttons draw is the server's, reproduced: the AUTHOR corrects
// their own words, the COORDINATION removes any note and rewrites none.
// `noteRights` is where each mode answers, and the answer is not the same —
// browser mode holds every note it has, team mode reads the server's `mine`
// beside its own role.

import { readFileSync } from "node:fs";
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { EMPTY_CFG, Fiche } from "./common.tsx";
import type { Mayor, Note } from "./types.ts";

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement("div");
  // `focusContenu` aims at the content landmark, which the card does not
  // carry on its own: without it every focus rescue is a no-op and the
  // assertions below would hold for the wrong reason.
  const main = document.createElement("main");
  main.id = "contenu";
  main.tabIndex = -1;
  main.append(container);
  document.body.append(main);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.parentElement?.remove();
});

const MAYOR: Mayor = {
  insee_code: "01001",
  commune: "Artemare",
  department: "Ain",
  last_name: "DESCHAMPS",
  first_name: "Roland",
  title: "M.",
  rank: "no_signal",
  email: "mairie@artemare.fr",
};

const MINE: Note = {
  id: 7,
  volunteer: "Alice",
  status: "to_contact",
  note: "aple demain",
  ts: "2026-01-02T10:00",
};
const THEIRS: Note = {
  id: 8,
  volunteer: "Bruno",
  status: "to_contact",
  note: "note de Bruno",
  ts: "2026-01-03T10:00",
  mine: false,
};

const flush = () =>
  act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });

interface Wiring {
  notes?: Note[];
  noteRights?: (n: Note) => { edit: boolean; delete: boolean };
  onEditNote?: (n: Note, index: number, text: string) => Promise<void>;
  onDeleteNote?: (n: Note, index: number) => Promise<void>;
  onStatus?: (status: string, note: string) => void | Promise<void>;
}

const render = (wiring: Wiring) =>
  act(() => {
    root.render(
      <Fiche
        mayor={MAYOR}
        cfg={EMPTY_CFG}
        notes={wiring.notes ?? [{ ...MINE, mine: true }]}
        noteRights={wiring.noteRights}
        onEditNote={wiring.onEditNote}
        onDeleteNote={wiring.onDeleteNote}
        onBack={() => {}}
        onStatus={wiring.onStatus ?? (() => {})}
      />,
    );
  });

const buttons = (label: string) =>
  [...container.querySelectorAll("button")].filter(
    (b) => (b.getAttribute("aria-label") ?? b.textContent ?? "") === label,
  );

function button(label: string): HTMLButtonElement {
  const found = buttons(label);
  if (!found.length) {
    throw new Error(`no button « ${label} » on screen`);
  }
  return found[0];
}

const click = (label: string) =>
  act(async () => {
    button(label).dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });

const noteEditor = () =>
  [...container.querySelectorAll("label")]
    .find((l) => l.textContent?.startsWith("Texte de la note"))
    ?.querySelector("textarea");

function type(field: HTMLTextAreaElement, value: string) {
  const set = Object.getOwnPropertyDescriptor(
    window.HTMLTextAreaElement.prototype,
    "value",
  )!.set!;
  return act(async () => {
    set.call(field, value);
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

const text = () => container.textContent ?? "";

describe("what the history offers", () => {
  // A mode that cannot do it renders no button at all — which is what an
  // absent `noteRights` means, and what every screen rendering a card
  // without the wiring gets.
  it("offers nothing when the mode says nothing", async () => {
    await render({});
    expect(buttons("Modifier la note 1 du 2026-01-02T10:00")).toHaveLength(0);
    expect(buttons("Supprimer la note 1 du 2026-01-02T10:00")).toHaveLength(0);
  });

  // The wiring is not enough either: a right granted with no callback behind
  // it is a button that does nothing when pressed.
  it("offers nothing when the right is granted and nothing is wired", async () => {
    await render({ noteRights: () => ({ edit: true, delete: true }) });
    expect(buttons("Modifier la note 1 du 2026-01-02T10:00")).toHaveLength(0);
  });

  // The team rule, both halves at once: a volunteer corrects and removes
  // their own line, and reads their colleague's without a button on it.
  it("puts the buttons on the reader's own line and nowhere else", async () => {
    await render({
      notes: [THEIRS, { ...MINE, mine: true }],
      noteRights: (n) => ({ edit: !!n.mine, delete: !!n.mine }),
      onEditNote: async () => {},
      onDeleteNote: async () => {},
    });
    // THEIRS is first in the history, so it is « la note 1 » and the
    // reader's own is « la note 2 » — the position is the row's, not the
    // reader's.
    expect(buttons("Modifier la note 2 du 2026-01-02T10:00")).toHaveLength(1);
    expect(buttons("Supprimer la note 2 du 2026-01-02T10:00")).toHaveLength(1);
    expect(buttons("Modifier la note 1 du 2026-01-03T10:00")).toHaveLength(0);
    expect(buttons("Supprimer la note 1 du 2026-01-03T10:00")).toHaveLength(0);
  });

  // The coordination's own shape: it removes words it must not carry, and
  // does not replace them. Two RIGHTS and not one, because the routes behind
  // them are two — and a screen offering « Modifier » on somebody else's note
  // promises what the server answers 404 to.
  it("lets a coordination remove another's line without rewriting it", async () => {
    await render({
      notes: [THEIRS],
      noteRights: (n) => ({ edit: !!n.mine, delete: true }),
      onEditNote: async () => {},
      onDeleteNote: async () => {},
    });
    expect(buttons("Supprimer la note 1 du 2026-01-03T10:00")).toHaveLength(1);
    expect(buttons("Modifier la note 1 du 2026-01-03T10:00")).toHaveLength(0);
  });

  // Every row carries the same two buttons, so their VISIBLE names repeat
  // down the list and a screen reader enumerates them identically.
  //
  // TWO NOTES OF THE SAME MINUTE, which is what an afternoon of work looks
  // like: an email sent and the call that followed. The date alone left both
  // rows wearing one name, and it is the POSITION that tells them apart.
  it("gives each line's buttons a name of their own", async () => {
    await render({
      notes: [
        { ...MINE, id: 9, note: "appelé", mine: true },
        { ...MINE, mine: true },
      ],
      noteRights: () => ({ edit: true, delete: true }),
      onEditNote: async () => {},
      onDeleteNote: async () => {},
    });
    const named = [...container.querySelectorAll("button")]
      .map((b) => b.getAttribute("aria-label"))
      .filter((l): l is string => !!l);
    expect(new Set(named).size).toBe(named.length);
  });

  // The mark is the note's, not the screen's: rendered from a field, so a
  // line nobody touched says nothing.
  it("says a line was corrected, and only when it was", async () => {
    await render({
      notes: [
        { ...MINE, mine: true, edited_at: "2026-01-04T09:00" },
        { ...THEIRS },
      ],
    });
    expect(text()).toContain("modifiée le 2026-01-04T09:00");
    expect(text().match(/modifiée le/g)).toHaveLength(1);
  });
});

describe("correcting a line", () => {
  const wired = (onEditNote: Wiring["onEditNote"]) =>
    render({
      noteRights: () => ({ edit: true, delete: false }),
      onEditNote,
    });

  // The editor opens on the line's CURRENT text. Opened empty, « Enregistrer »
  // is one press away from replacing a call note with nothing.
  it("opens on the text it is about to replace", async () => {
    await wired(async () => {});
    await click("Modifier la note 1 du 2026-01-02T10:00");
    expect(noteEditor()?.value).toBe("aple demain");
  });

  it("sends the corrected text and closes", async () => {
    const sent: string[] = [];
    await wired(async (_n, _i, t) => {
      sent.push(t);
    });
    await click("Modifier la note 1 du 2026-01-02T10:00");
    await type(noteEditor()!, "rappeler demain");
    await click("Enregistrer la note");
    await flush();
    expect(sent).toEqual(["rappeler demain"]);
    expect(noteEditor()).toBeUndefined();
    expect(text()).toContain("Note modifiée.");
  });

  // CORRECTING WORDS MOVES THE CARD NOWHERE, so it takes nothing from the
  // volunteer. Written into the act a correction and a removal share, the
  // pick was dropped by both: choose an outcome, notice a typo in an older
  // line, fix it — and the choice is gone, silently, the select simply back
  // to what the card carries. The next « Enregistrer » then files THAT.
  it("keeps a pending pick when only the words of a line are corrected", async () => {
    const filed: string[] = [];
    await render({
      noteRights: () => ({ edit: true, delete: false }),
      onEditNote: async () => {},
      onStatus: async (s) => {
        filed.push(s);
      },
    });
    const select = () => container.querySelector("select")!;
    await act(async () => {
      const s = select();
      s.value = "to_call_back";
      s.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(select().value).toBe("to_call_back");

    await click("Modifier la note 1 du 2026-01-02T10:00");
    await type(noteEditor()!, "rappeler demain");
    await click("Enregistrer la note");
    await flush();

    expect(select().value).toBe("to_call_back");
    await click("Enregistrer");
    await flush();
    expect(filed).toEqual(["to_call_back"]);
  });

  // A refusal is the volunteer's only word about a correction that did not
  // land — « cette note a changé depuis son affichage » — so the editor stays
  // open with what they typed still in it, and the message is shown as is.
  it("keeps the editor and the words when the write is refused", async () => {
    await wired(async () => {
      throw new Error("Cette note a changé depuis son affichage.");
    });
    await click("Modifier la note 1 du 2026-01-02T10:00");
    await type(noteEditor()!, "corrigée");
    await click("Enregistrer la note");
    await flush();
    expect(text()).toContain("Cette note a changé depuis son affichage.");
    expect(noteEditor()?.value).toBe("corrigée");
  });

  // « Annuler » destroys the control under the pointer, so focus falls to
  // <body> and the next Tab restarts at the top of the page.
  it("hands focus to the content when the editor is dismissed", async () => {
    await wired(async () => {});
    await click("Modifier la note 1 du 2026-01-02T10:00");
    button("Annuler").focus();
    await click("Annuler");
    expect(document.activeElement).toBe(document.getElementById("contenu"));
  });
});

describe("removing a line", () => {
  const wired = (onDeleteNote: Wiring["onDeleteNote"]) =>
    render({
      noteRights: () => ({ edit: false, delete: true }),
      onDeleteNote,
    });

  // Asked in the ROW. The act removes a line from a register the whole
  // campaign reads, and one press is not an answer to that.
  it("asks before it removes", async () => {
    const removed: number[] = [];
    await wired(async (_n, i) => {
      removed.push(i);
    });
    await click("Supprimer la note 1 du 2026-01-02T10:00");
    await flush();
    expect(removed).toEqual([]);
    expect(text()).toContain("Supprimer cette note ?");

    await click("Confirmer");
    await flush();
    expect(removed).toEqual([0]);
    expect(text()).toContain("Note supprimée.");
  });

  it("puts the question away when it is declined", async () => {
    const removed: number[] = [];
    await wired(async (_n, i) => {
      removed.push(i);
    });
    await click("Supprimer la note 1 du 2026-01-02T10:00");
    await click("Annuler");
    await flush();
    expect(removed).toEqual([]);
    expect(text()).not.toContain("Supprimer cette note ?");
  });

  // A REF and not the `notesBusy` state: two clicks in the same tick run two
  // handlers built by the same render, both read it as false, and both go —
  // which on a deletion is a second request against a row that no longer
  // exists, answered 404 to somebody whose deletion worked.
  it("goes once however fast the confirmation is pressed twice", async () => {
    let calls = 0;
    await wired(async () => {
      calls++;
      await new Promise((r) => setTimeout(r, 10));
    });
    await click("Supprimer la note 1 du 2026-01-02T10:00");
    const confirm = button("Confirmer");
    await act(async () => {
      confirm.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      confirm.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flush();
    expect(calls).toBe(1);
  });

  // The button that ran the deletion dies WITH the line, at the completion of
  // the reload and not at the click. `rescueFocusAfterCommit` cannot see that
  // — called after the await it finds focus already on <body> and cannot tell
  // « nobody was holding anything » from « the holder just died ».
  it("rescues the focus of the control that died with the line", async () => {
    // A parent holding the history, which is the shape both modes have: the
    // line leaves when the RELOAD lands, not when the deletion answers.
    //
    // 200 ms, as a round trip takes. `rescueFocusAfterCommit` checks at 0 and
    // 60 ms — a BET on when React commits, and against a real answer it loses:
    // both checks read a button still in the page and decline, and nothing
    // looks again. Written with an instant reload, this test passes with
    // either helper and says nothing.
    function Carte() {
      const [notes, setNotes] = useState<Note[]>([{ ...MINE, mine: true }]);
      return (
        <Fiche
          mayor={MAYOR}
          cfg={EMPTY_CFG}
          notes={notes}
          noteRights={() => ({ edit: false, delete: true })}
          onDeleteNote={() =>
            new Promise((resolve) => {
              setTimeout(() => {
                setNotes([]);
                resolve();
              }, 200);
            })
          }
          onBack={() => {}}
          onStatus={() => {}}
        />
      );
    }
    await act(() => {
      root.render(<Carte />);
    });
    await click("Supprimer la note 1 du 2026-01-02T10:00");
    const confirm = button("Confirmer");
    await act(async () => {
      confirm.focus();
    });
    await act(async () => {
      confirm.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    for (const ms of [5, 40, 80, 150, 250, 300]) {
      await act(async () => {
        await new Promise((r) => setTimeout(r, ms));
      });
    }
    expect(text(), "the line left the history").not.toContain("aple demain");
    expect(document.activeElement?.id).toBe("contenu");
  });

  // THE CONTROL FOLLOWS THE CARD WHEN THE CARD MOVES UNDER IT. Removing the
  // head rolls the card back, and a select still showing the withdrawn status
  // writes it straight back on the next « Enregistrer » — with a `seen` the
  // parent has refreshed, so the server accepts it and the roll-back is
  // undone by a screen that was never told.
  it("shows the status the card came back with", async () => {
    function Carte() {
      const [notes, setNotes] = useState<Note[]>([
        { ...MINE, status: "refused", mine: true },
        { ...THEIRS, status: "email_sent", mine: true },
      ]);
      return (
        <Fiche
          mayor={MAYOR}
          cfg={EMPTY_CFG}
          notes={notes}
          status={notes[0]?.status ?? "to_contact"}
          noteRights={() => ({ edit: false, delete: true })}
          onDeleteNote={async (_n, i) => {
            setNotes((held) => held.filter((_, at) => at !== i));
          }}
          onBack={() => {}}
          onStatus={() => {}}
        />
      );
    }
    await act(() => {
      root.render(<Carte />);
    });
    const select = container.querySelector("select")!;
    expect(select.value).toBe("refused");

    await click("Supprimer la note 1 du 2026-01-02T10:00");
    await click("Confirmer");
    await flush();
    expect(container.querySelector("select")!.value).toBe("email_sent");
  });

  // THE PICK IS SPENT ONCE IT IS RECORDED, and this is the case that says so.
  // Remembered against the card status it was made under, it came back to
  // life the day the card RETURNED to that status — which is precisely what
  // removing a note does. Measured end to end: the roll-back landed in the
  // database and the select went on showing the status that had just been
  // withdrawn.
  it("does not bring a spent pick back when the card returns to where it was", async () => {
    function Carte() {
      const [notes, setNotes] = useState<Note[]>([
        { ...THEIRS, status: "email_sent", mine: true },
      ]);
      const status = notes[0]?.status ?? "to_contact";
      return (
        <Fiche
          mayor={MAYOR}
          cfg={EMPTY_CFG}
          notes={notes}
          status={status}
          noteRights={() => ({ edit: false, delete: true })}
          onStatus={async (chosen) => {
            setNotes((held) => [
              { ...MINE, status: chosen, note: "appelé", mine: true },
              ...held,
            ]);
          }}
          onDeleteNote={async (_n, i) => {
            setNotes((held) => held.filter((_, at) => at !== i));
          }}
          onBack={() => {}}
        />
      );
    }
    await act(() => {
      root.render(<Carte />);
    });
    const select = () => container.querySelector("select")!;
    expect(select().value).toBe("email_sent");

    // recorded: the card carries the pick, and the pick is spent
    await act(async () => {
      const s = select();
      s.value = "to_call_back";
      s.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(select().value).toBe("to_call_back");
    await click("Enregistrer");
    await flush();
    expect(select().value).toBe("to_call_back");

    // removed: the card goes back to « email_sent », and so must the select
    await click("Supprimer la note 1 du 2026-01-02T10:00");
    await click("Confirmer");
    await flush();
    expect(select().value).toBe("email_sent");
  });

  // A REFUSAL IS NOT A ROLL-BACK. The pick is dropped BEFORE the round trip
  // so that no frame shows the withdrawn status — but a removal that is
  // refused moves the card nowhere, and the pick was destroyed for nothing:
  // the select silently back to what the card already carried, and the next
  // « Enregistrer » filing THAT. Reachable on a 404 (somebody removed the
  // line first), a 409, a network failure, and in browser mode on the very
  // refusal that keeps two tabs from overwriting each other.
  it("gives the pick back when the removal is refused", async () => {
    const filed: string[] = [];
    await render({
      noteRights: () => ({ edit: false, delete: true }),
      onDeleteNote: async () => {
        throw new Error("Aucune note à supprimer ici.");
      },
      onStatus: async (s) => {
        filed.push(s);
      },
    });
    const select = () => container.querySelector("select")!;
    await act(async () => {
      const s = select();
      s.value = "email_sent";
      s.dispatchEvent(new Event("change", { bubbles: true }));
    });

    await click("Supprimer la note 1 du 2026-01-02T10:00");
    await click("Confirmer");
    await flush();

    expect(text()).toContain("Aucune note à supprimer ici.");
    expect(
      select().value,
      "the card stood still and the choice was taken anyway",
    ).toBe("email_sent");
    await click("Enregistrer");
    await flush();
    expect(filed).toEqual(["email_sent"]);
  });

  it("shows a refusal and keeps the line", async () => {
    await wired(async () => {
      throw new Error("Aucune note à supprimer ici.");
    });
    await click("Supprimer la note 1 du 2026-01-02T10:00");
    await click("Confirmer");
    await flush();
    expect(text()).toContain("Aucune note à supprimer ici.");
    expect(text()).toContain("aple demain");
  });
});

// A PICK BELONGS TO A PERSON, and a card can change under a mounted
// component. Team mode clears its card before fetching the next, so this one
// unmounts between two mayors; BROWSER mode derives the card synchronously
// from the list it already holds, so one card's address to another — a
// bookmark, a link between two volunteers, « précédent » between two cards —
// swaps the mayor with everything still mounted. Both being « à contacter »,
// as most of the list is, the pick stood on the next mayor and « Enregistrer »
// filed it against the wrong person.
describe("a pick belongs to the mayor it was made on", () => {
  const OTHER: Mayor = {
    ...MAYOR,
    insee_code: "01002",
    commune: "Belley",
    last_name: "MARTIN",
    first_name: "Claude",
  };

  function DeuxFiches({
    filed,
    notes = [],
    wiring = {},
  }: {
    filed: string[];
    notes?: Note[];
    wiring?: Wiring;
  }) {
    const [mayor, setMayor] = useState(MAYOR);
    return (
      <>
        <button type="button" onClick={() => setMayor(OTHER)}>
          fiche suivante
        </button>
        <button type="button" onClick={() => setMayor(MAYOR)}>
          fiche précédente
        </button>
        <Fiche
          mayor={mayor}
          cfg={EMPTY_CFG}
          notes={notes}
          noteRights={wiring.noteRights}
          onEditNote={wiring.onEditNote}
          onDeleteNote={wiring.onDeleteNote}
          onBack={() => {}}
          // the card it was started on is recorded WITH the answer, and a
          // wiring that wants to hold the request open is honoured — without
          // that, a test about what lands after a swap resolves before the
          // swap and passes having exercised nothing
          onStatus={(s, note) => {
            filed.push(`${mayor.insee_code}:${s}`);
            return wiring.onStatus?.(s, note);
          }}
        />
      </>
    );
  }

  it("does not carry a pick from one card to the next", async () => {
    const filed: string[] = [];
    await act(() => {
      root.render(<DeuxFiches filed={filed} />);
    });
    const select = () => container.querySelector("select")!;
    await act(async () => {
      const s = select();
      s.value = "email_sent";
      s.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(select().value).toBe("email_sent");

    await click("fiche suivante");
    expect(
      select().value,
      "the next mayor's card shows a status nobody chose for them",
    ).toBe("to_contact");

    await click("Enregistrer");
    await flush();
    expect(filed).toEqual(["01002:to_contact"]);
  });

  // AND SO DOES THE EDITOR OPEN ON ONE OF ITS LINES. The pick was keyed on
  // the person and the editor was not, so the box stayed open across the
  // swap with the FIRST mayor's text in it — and « Enregistrer la note »
  // then rewrote the note at the same position on the SECOND mayor's card
  // with words written about somebody else. Nothing on screen said so.
  it("closes an editor left open when the card changes", async () => {
    const sent: string[][] = [];
    await act(() => {
      root.render(
        <DeuxFiches
          filed={[]}
          notes={[{ ...MINE, mine: true }]}
          wiring={{
            noteRights: () => ({ edit: true, delete: false }),
            onEditNote: async (_n, i, text) => {
              sent.push([String(i), text]);
            },
          }}
        />,
      );
    });
    await click("Modifier la note 1 du 2026-01-02T10:00");
    await type(noteEditor()!, "écrit à propos du premier maire");

    await click("fiche suivante");
    expect(
      noteEditor(),
      "the editor stayed open over the next mayor's history",
    ).toBeUndefined();
    expect(sent).toEqual([]);
  });

  // The same for the question that stands between a click and the act: left
  // standing, « Confirmer » removes the line at that position on the card
  // that is now on screen.
  it("puts away a confirmation left standing when the card changes", async () => {
    const removed: number[] = [];
    await act(() => {
      root.render(
        <DeuxFiches
          filed={[]}
          notes={[{ ...MINE, mine: true }]}
          wiring={{
            noteRights: () => ({ edit: false, delete: true }),
            onDeleteNote: async (_n, i) => {
              removed.push(i);
            },
          }}
        />,
      );
    });
    await click("Supprimer la note 1 du 2026-01-02T10:00");
    expect(text()).toContain("Supprimer cette note ?");

    await click("fiche suivante");
    expect(
      text(),
      "the question followed the volunteer to another mayor",
    ).not.toContain("Supprimer cette note ?");
    expect(removed).toEqual([]);
  });

  // A REQUEST STARTED ON ONE CARD FINISHES ON THE CARD IT WAS STARTED ON.
  //
  // Clearing per-card state at the swap is not enough: the terminal writes of
  // an act happen AFTER the await, and the mayor may have changed in between.
  // The worst of them is `setNote("")` — the volunteer opens the next card
  // and starts typing what the mayor just said, the previous request lands,
  // and the field empties under their hands.
  it("does not empty the next card's note when the previous save lands", async () => {
    let release: () => void = () => {};
    const inFlight = new Promise<void>((r) => {
      release = r;
    });
    const filed: string[] = [];
    await act(() => {
      root.render(
        <DeuxFiches filed={filed} wiring={{ onStatus: () => inFlight }} />,
      );
    });
    await click("Enregistrer");

    await click("fiche suivante");
    const field = [...container.querySelectorAll("label")]
      .find((l) => l.textContent?.startsWith("Note"))!
      .querySelector("textarea")!;
    await type(field, "il rappelle jeudi");

    await act(async () => {
      release();
      await inFlight;
    });
    await flush();

    expect(field.value, "the note typed on the next card was emptied").toBe(
      "il rappelle jeudi",
    );
    expect(
      text(),
      "the previous card's confirmation landed here",
    ).not.toContain("Enregistré.");
  });

  // The same, one act over: a refusal about a line of the PREVIOUS card,
  // shown over this one, is an error a volunteer cannot act on.
  it("does not show the previous card's refusal on the next", async () => {
    let refuse: (e: Error) => void = () => {};
    const inFlight = new Promise<void>((_r, reject) => {
      refuse = reject;
    });
    await act(() => {
      root.render(
        <DeuxFiches
          filed={[]}
          notes={[{ ...MINE, mine: true }]}
          wiring={{
            noteRights: () => ({ edit: false, delete: true }),
            onDeleteNote: () => inFlight,
          }}
        />,
      );
    });
    await click("Supprimer la note 1 du 2026-01-02T10:00");
    await click("Confirmer");

    await click("fiche suivante");
    await act(async () => {
      refuse(new Error("Aucune note à supprimer ici."));
      await inFlight.catch(() => {});
    });
    await flush();

    expect(text()).not.toContain("Aucune note à supprimer ici.");
  });

  // And the controls of the next card are its own: a save still in flight on
  // the previous one left this button reading « Enregistrement… » and its
  // re-entry guard armed, so a click here was refused in silence — and then
  // the previous card's « Enregistré. » arrived and read as this one's.
  it("does not leave the next card's button busy or refusing", async () => {
    let release: () => void = () => {};
    const inFlight = new Promise<void>((r) => {
      release = r;
    });
    const filed: string[] = [];
    await act(() => {
      root.render(
        <DeuxFiches filed={filed} wiring={{ onStatus: () => inFlight }} />,
      );
    });
    await click("Enregistrer");
    await click("fiche suivante");

    // found by its place, not by its label — the label IS what is wrong
    const save = container.querySelector<HTMLButtonElement>(
      ".barre-statut button",
    )!;
    expect(
      save.textContent,
      "the next card's button is busy with the previous card's request",
    ).toBe("Enregistrer");
    await act(async () => {
      save.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flush();
    expect(filed, "the click on the next card was swallowed").toHaveLength(2);
    await act(async () => {
      release();
      await inFlight;
    });
  });

  // AND COMING BACK IS NOT BEING THERE ALL ALONG. Gating the terminal writes
  // on the mayor's IDENTITY is necessary and not sufficient: leave the card
  // with a request in flight, come back to it, start writing again — the
  // identity matches, the gate opens, and the landing request empties the
  // field for the second time. What supersedes an act is any act after it,
  // and leaving the card is one.
  it("does not empty a card returned to while its own save was in flight", async () => {
    let release: () => void = () => {};
    const inFlight = new Promise<void>((r) => {
      release = r;
    });
    await act(() => {
      root.render(
        <DeuxFiches filed={[]} wiring={{ onStatus: () => inFlight }} />,
      );
    });
    const field = () =>
      [...container.querySelectorAll("label")]
        .find((l) => l.textContent?.startsWith("Note"))!
        .querySelector("textarea")!;
    await type(field(), "premier passage");
    await click("Enregistrer");

    await click("fiche suivante");
    await click("fiche précédente");
    await type(field(), "deuxième passage");

    await act(async () => {
      release();
      await inFlight;
    });
    await flush();
    expect(field().value, "the second visit's note was emptied").toBe(
      "deuxième passage",
    );
  });

  // AND A FINISHED ACT DOES NOT UNLOCK SOMEBODY ELSE'S. Releasing the
  // re-entry guards when the card changes is right — two mayors are two
  // requests — but the guard the first act releases when it lands is then
  // the SECOND card's, and the next click doubles its save. One note per
  // intention is the whole reason that guard exists.
  it("does not unlock the next card's button when the previous act lands", async () => {
    let releaseFirst: () => void = () => {};
    const first = new Promise<void>((r) => {
      releaseFirst = r;
    });
    const second = new Promise<void>(() => {});
    const filed: string[] = [];
    let call = 0;
    await act(() => {
      root.render(
        <DeuxFiches
          filed={filed}
          wiring={{
            onStatus: () => (++call === 1 ? first : second),
          }}
        />,
      );
    });
    await click("Enregistrer");
    await click("fiche suivante");
    await click("Enregistrer");
    expect(filed).toHaveLength(2);

    // the first card's request lands; the second is still out
    await act(async () => {
      releaseFirst();
      await first;
    });
    await flush();

    const save = container.querySelector<HTMLButtonElement>(
      ".barre-statut button",
    )!;
    await act(async () => {
      save.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flush();
    expect(
      filed,
      "the second card was saved twice for one intention",
    ).toHaveLength(2);
  });

  // AND ONE ACT DOES NOT SUPERSEDE ANOTHER OF A DIFFERENT KIND. Recording an
  // outcome and correcting a line are two acts with two buttons, two
  // re-entry guards and two busy labels; what each writes when it lands is
  // its own to write. Counted together, correcting a line while a save was
  // in flight left « Enregistrement… » on a card the volunteer had not left,
  // and its guard armed with nothing to release it: the save was bricked
  // until they went somewhere else.
  it("does not brick the save because a line was corrected meanwhile", async () => {
    let release: () => void = () => {};
    const inFlight = new Promise<void>((r) => {
      release = r;
    });
    await render({
      notes: [{ ...MINE, mine: true }],
      noteRights: () => ({ edit: true, delete: false }),
      onEditNote: async () => {},
      onStatus: () => inFlight,
    });
    await click("Enregistrer");

    await click("Modifier la note 1 du 2026-01-02T10:00");
    await type(noteEditor()!, "corrigée pendant ce temps");
    await click("Enregistrer la note");
    await flush();

    await act(async () => {
      release();
      await inFlight;
    });
    await flush();
    const save = container.querySelector<HTMLButtonElement>(
      ".barre-statut button",
    )!;
    expect(save.textContent, "the save never came back").toBe("Enregistrer");
  });

  // The same the other way round: the correction lands, and the editor it
  // was typed in is still on screen because a save had been started since.
  it("closes the editor even if a save was started meanwhile", async () => {
    let release: () => void = () => {};
    const inFlight = new Promise<void>((r) => {
      release = r;
    });
    await render({
      notes: [{ ...MINE, mine: true }],
      noteRights: () => ({ edit: true, delete: false }),
      onEditNote: () => inFlight,
      onStatus: async () => {},
    });
    await click("Modifier la note 1 du 2026-01-02T10:00");
    await type(noteEditor()!, "corrigée");
    await click("Enregistrer la note");

    await click("Enregistrer");
    await flush();
    await act(async () => {
      release();
      await inFlight;
    });
    await flush();
    expect(
      noteEditor(),
      "the editor outlived the correction it held",
    ).toBeUndefined();
  });

  // And « a refusal is not a roll-back » holds whatever else the volunteer
  // did in between: the pick dropped for a removal comes back when that
  // removal is turned down, even if a save was started while it was out.
  it("gives the pick back on a refusal that a save overlapped", async () => {
    let refuse: (e: Error) => void = () => {};
    const inFlight = new Promise<void>((_r, reject) => {
      refuse = reject;
    });
    await render({
      notes: [{ ...MINE, status: "email_sent", mine: true }],
      noteRights: () => ({ edit: false, delete: true }),
      onDeleteNote: () => inFlight,
      onStatus: async () => {},
    });
    const select = () => container.querySelector("select")!;
    await act(async () => {
      const s = select();
      s.value = "to_call_back";
      s.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await click("Supprimer la note 1 du 2026-01-02T10:00");
    await click("Confirmer");

    await click("Enregistrer");
    await flush();
    await act(async () => {
      refuse(new Error("Aucune note à supprimer ici."));
      await inFlight.catch(() => {});
    });
    await flush();
    expect(
      select().value,
      "the refused removal kept the choice it dropped",
    ).toBe("to_call_back");
  });

  // AND TYPING SUPERSEDES NOTHING, which is why the counter cannot be the
  // whole answer. On a weak connection — a phone, a train, rural 4G — the
  // button reads « Enregistrement… » for a second or two, and a volunteer
  // still on the telephone goes on writing. The save lands and empties the
  // field: what it clears is what it SENT, and this is neither the same card
  // nor a different act, so nothing had superseded it.
  it("clears only the note it actually sent", async () => {
    let release: () => void = () => {};
    const inFlight = new Promise<void>((r) => {
      release = r;
    });
    const sent: string[] = [];
    await render({
      onStatus: (_s, note) => {
        sent.push(note);
        return inFlight;
      },
    });
    const field = () =>
      [...container.querySelectorAll("label")]
        .find((l) => l.textContent?.startsWith("Note"))!
        .querySelector("textarea")!;
    await type(field(), "premier envoi");
    await click("Enregistrer");

    // still on the phone, still writing
    await type(field(), "premier envoi — puis rappel à 15 h");
    await act(async () => {
      release();
      await inFlight;
    });
    await flush();

    expect(sent).toEqual(["premier envoi"]);
    expect(field().value, "what was typed during the save was erased").toBe(
      "premier envoi — puis rappel à 15 h",
    );
  });

  // A CALL LOG HAS LINES. The editor keeps them and the history swallowed
  // them: « rappeler avant 11 h \n secrétariat: Mme X » came back as one run
  // of words, and a volunteer could not read their own notes.
  // The RULE and not the computed style: jsdom loads no stylesheet, so
  // `getComputedStyle` here would answer the empty string whatever the CSS
  // says. What is checked is that the text is in an element of its own and
  // that the sheet keeps that element's lines.
  it("keeps the lines of a note that has several", async () => {
    await render({
      notes: [{ ...MINE, note: "ligne une\nligne deux", mine: true }],
    });
    const held = container.querySelector(".note-item span.note-texte");
    expect(held?.textContent).toBe("ligne une\nligne deux");
    // run from `web/`, like every other suite here
    const sheet = readFileSync("src/style.css", "utf8");
    expect(sheet).toMatch(/\.note-texte\s*\{[^}]*white-space:\s*pre-wrap/);
  });

  // TEN MINUTES OF CAREFUL REWRITING ARE NOT THROWN AWAY WITHOUT A WORD.
  // Opening « Modifier » on another line abandons the correction in progress
  // — one editor, one draft — and the screen SAYS SO. Held per line instead,
  // the drafts were keyed by the row's position, and in browser mode a
  // removal shifts those positions: a draft typed for one line came back
  // under another, and pressing « Enregistrer la note » wrote it there.
  it("says so when opening another line abandons a correction", async () => {
    await render({
      notes: [
        { ...MINE, mine: true },
        { ...THEIRS, mine: true },
      ],
      noteRights: () => ({ edit: true, delete: false }),
      onEditNote: async () => {},
    });
    await click("Modifier la note 1 du 2026-01-02T10:00");
    await type(noteEditor()!, "dix minutes de réécriture");

    await click("Modifier la note 2 du 2026-01-03T10:00");
    expect(noteEditor()?.value).toBe("note de Bruno");
    expect(text(), "the abandoned correction was not mentioned").toContain(
      "correction en cours",
    );

    // and it is gone, not hiding somewhere waiting to land on another line
    await click("Modifier la note 1 du 2026-01-02T10:00");
    expect(noteEditor()?.value).toBe("aple demain");
  });

  // A DRAFT NEVER COMES BACK UNDER ANOTHER LINE. Browser mode names a note by
  // its POSITION, and a removal shifts every position newer than it — so a
  // draft kept by position was handed to whichever line inherited the number.
  // The volunteer opened what they took for their own recent draft, pressed
  // save, and the wrong note was overwritten with words about another
  // contact.
  it("never hands a correction to the line that inherited a position", async () => {
    // no ids: this is how browser mode's history arrives
    const local = (note: string, ts: string) => ({
      volunteer: null,
      status: "to_contact",
      note,
      ts,
    });
    const sent: string[][] = [];
    function Trois() {
      const [notes, setNotes] = useState<Note[]>([
        local("récente", "2026-01-03T10:00"),
        local("du milieu", "2026-01-02T10:00"),
        local("ancienne", "2026-01-01T10:00"),
      ]);
      return (
        <Fiche
          mayor={MAYOR}
          cfg={EMPTY_CFG}
          notes={notes}
          noteRights={() => ({ edit: true, delete: true })}
          onEditNote={async (_n, i, t) => {
            sent.push([String(i), t]);
          }}
          onDeleteNote={async (_n, i) => {
            setNotes((held) => held.filter((_, at) => at !== i));
          }}
          onBack={() => {}}
          onStatus={() => {}}
        />
      );
    }
    await act(() => {
      root.render(<Trois />);
    });

    await click("Modifier la note 2 du 2026-01-02T10:00");
    await type(noteEditor()!, "écrit à propos de celle du milieu");
    await click("Annuler");

    // the OLDEST goes: every position newer than it shifts down by one
    await click("Supprimer la note 3 du 2026-01-01T10:00");
    await click("Confirmer");
    await flush();

    await click("Modifier la note 1 du 2026-01-03T10:00");
    expect(
      noteEditor()?.value,
      "the newest line was handed a correction written about another",
    ).toBe("récente");
  });

  // A LANDING ACT CLOSES ITS OWN EDITOR, NOT WHICHEVER ONE IS OPEN. On a
  // rural connection a correction takes a second or two; the volunteer moves
  // to another line and starts writing what the mayor is saying. The first
  // one landed and closed THAT editor, and every character went with it —
  // no warning, because the message about an abandoned correction only fires
  // when one editor replaces another, not when one is taken away.
  it("closes the editor it was opened from, not the one now open", async () => {
    let release: () => void = () => {};
    const inFlight = new Promise<void>((r) => {
      release = r;
    });
    await render({
      notes: [
        { ...MINE, mine: true },
        { ...THEIRS, mine: true },
      ],
      noteRights: () => ({ edit: true, delete: false }),
      onEditNote: () => inFlight,
    });
    await click("Modifier la note 2 du 2026-01-03T10:00");
    await type(noteEditor()!, "correction de la ligne 2");
    await click("Enregistrer la note");

    await click("Modifier la note 1 du 2026-01-02T10:00");
    await type(noteEditor()!, "ce que le maire dit maintenant");
    await act(async () => {
      release();
      await inFlight;
    });
    await flush();

    expect(
      noteEditor()?.value,
      "the landing correction took away the editor open on another line",
    ).toBe("ce que le maire dit maintenant");
  });

  // « ANNULER » MUST NOT LET THE THING IT CANCELS HAPPEN. Pressed while the
  // removal is already out, it closed the question — which reads as « done,
  // nothing removed » — and the note went anyway, with « Note supprimée. »
  // underneath. The request cannot be taken back, so the button refuses and
  // says so rather than promising what it cannot do.
  it("does not offer to cancel a removal that is already out", async () => {
    let release: () => void = () => {};
    const inFlight = new Promise<void>((r) => {
      release = r;
    });
    const removed: number[] = [];
    await render({
      noteRights: () => ({ edit: false, delete: true }),
      onDeleteNote: (_n, i) => {
        removed.push(i);
        return inFlight;
      },
    });
    await click("Supprimer la note 1 du 2026-01-02T10:00");
    await click("Confirmer");

    const cancel = button("Annuler");
    expect(cancel.getAttribute("aria-disabled")).toBe("true");
    await act(async () => {
      cancel.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(
      text(),
      "the question was put away, which reads as « nothing was removed »",
    ).toContain("Suppression…");

    await act(async () => {
      release();
      await inFlight;
    });
    await flush();
    expect(removed).toEqual([0]);
  });

  // A STATUS CHOSEN WHILE A REMOVAL IS OUT IS THE VOLUNTEER'S CURRENT
  // INTENTION. The roll-back that lands is about the history, not about the
  // outcome they have just decided; dropping their choice in silence leaves
  // the select showing the rolled-back status, and the next « Enregistrer »
  // — which a hurried volunteer presses without re-reading — files THAT
  // against a named mayor.
  it("keeps a status chosen while the removal was out", async () => {
    let release: () => void = () => {};
    const inFlight = new Promise<void>((r) => {
      release = r;
    });
    const filed: string[] = [];
    function Carte() {
      const [notes, setNotes] = useState<Note[]>([
        { ...MINE, status: "to_call_back", mine: true },
        { ...THEIRS, status: "email_sent", mine: true },
      ]);
      return (
        <Fiche
          mayor={MAYOR}
          cfg={EMPTY_CFG}
          notes={notes}
          status={notes[0]?.status ?? "to_contact"}
          noteRights={() => ({ edit: false, delete: true })}
          onDeleteNote={(_n, i) =>
            inFlight.then(() => {
              setNotes((held) => held.filter((_, at) => at !== i));
            })
          }
          onBack={() => {}}
          onStatus={(s) => {
            filed.push(s);
          }}
        />
      );
    }
    await act(() => {
      root.render(<Carte />);
    });
    const select = () => container.querySelector("select")!;
    expect(select().value).toBe("to_call_back");

    await click("Supprimer la note 1 du 2026-01-02T10:00");
    await click("Confirmer");
    // they change their mind about the OUTCOME while the removal is out
    await act(async () => {
      const s = select();
      s.value = "refused";
      s.dispatchEvent(new Event("change", { bubbles: true }));
    });

    await act(async () => {
      release();
      await inFlight;
    });
    await flush();
    expect(
      select().value,
      "the choice made during the removal was dropped in silence",
    ).toBe("refused");
    await click("Enregistrer");
    await flush();
    expect(filed).toEqual(["refused"]);
  });

  // And what the screen SAYS about the last act belongs to the card it was
  // said about: « Enregistré. » over a mayor nobody has written to, or a red
  // alert about a refusal on somebody else, is a sentence read as this
  // card's.
  it("does not carry what it said about one card onto the next", async () => {
    const filed: string[] = [];
    await act(() => {
      root.render(<DeuxFiches filed={filed} />);
    });
    await click("Enregistrer");
    await flush();
    expect(text()).toContain("Enregistré.");

    await click("fiche suivante");
    expect(text(), "the confirmation followed the volunteer").not.toContain(
      "Enregistré.",
    );
  });

  // AND THE NOTE CONTROLS ARE RELEASED LIKE THE SAVE BUTTON, which is the
  // same rule one panel down and was carried by nothing: removing either half
  // of it from the reset block left all 335 tests green. Two mayors are two
  // requests, so a note act still out on the previous card must not hold this
  // card's buttons — the guard armed with nothing to release it, and a
  // « Suppression… » on a card nobody has acted on.
  it("frees the note controls when the card changes under an act", async () => {
    const asked: string[] = [];
    const onDeleteNote = async (n: Note) => {
      asked.push(n.note);
      // the first never lands: the volunteer moves on while it is out
      if (asked.length === 1) await new Promise<void>(() => {});
    };
    await act(() => {
      root.render(
        <DeuxFiches
          filed={[]}
          notes={[{ ...MINE, mine: true }]}
          wiring={{
            noteRights: () => ({ edit: true, delete: true }),
            onDeleteNote,
          }}
        />,
      );
    });
    await click("Supprimer la note 1 du 2026-01-02T10:00");
    await click("Confirmer");

    await click("fiche suivante");
    await click("Supprimer la note 1 du 2026-01-02T10:00");
    const asking = [...container.querySelectorAll("button")].find((b) =>
      /Confirmer|Suppression…/.test(b.textContent ?? ""),
    );
    expect(
      asking?.textContent,
      "the next card's button is busy with the previous card's request",
    ).toBe("Confirmer");

    await click("Confirmer");
    await flush();
    expect(
      asked,
      "the guard the previous card armed swallowed this card's removal",
    ).toHaveLength(2);
  });
});

describe("what a card renders without the wiring", () => {
  // The history block itself is unchanged for every screen that does not pass
  // the wiring — the account-less version before this shipped, a card
  // rendered by a test, a mode added tomorrow.
  it("still shows the line, its date and its status", async () => {
    await render({});
    expect(text()).toContain("2026-01-02T10:00");
    expect(text()).toContain("aple demain");
    expect(text()).toContain("Alice");
  });

  it("makes no request of a callback it was not given", async () => {
    const onStatus = vi.fn();
    await act(() => {
      root.render(
        <Fiche
          mayor={MAYOR}
          cfg={EMPTY_CFG}
          notes={[MINE]}
          onBack={() => {}}
          onStatus={onStatus}
        />,
      );
    });
    expect(onStatus).not.toHaveBeenCalled();
  });
});

// AN ACT IS AIMED AT A LINE, NOT AT A POSITION.
//
// Browser mode names a note by where it sits, so a key re-binds the moment
// the history changes length — and held as the aim it re-bound under acts
// that were already open, in both directions. Neither of these needs a race:
// the history moves because THIS screen moved it.
describe("an act is aimed at a line, not at a position", () => {
  const ligne = (note: string, ts: string, status = "to_contact"): Note => ({
    volunteer: "Alice",
    status,
    note,
    ts,
    mine: true,
  });

  // No id: browser mode, where a line is named by its position.
  const LIGNES: Note[] = [
    ligne("récente", "2026-01-04T10:00"),
    ligne("milieu 1", "2026-01-03T10:00"),
    ligne("milieu 2", "2026-01-02T10:00"),
    ligne("ancienne", "2026-01-01T10:00"),
  ];

  let setLignes: (n: Note[]) => void = () => {};

  function Registre({
    depart = LIGNES,
    ...wiring
  }: Wiring & { depart?: Note[] }) {
    const [notes, setNotes] = useState<Note[]>(depart);
    setLignes = setNotes;
    return (
      <Fiche
        mayor={MAYOR}
        cfg={EMPTY_CFG}
        notes={notes}
        noteRights={() => ({ edit: true, delete: true })}
        onEditNote={wiring.onEditNote}
        onDeleteNote={wiring.onDeleteNote}
        onBack={() => {}}
        onStatus={() => {}}
      />
    );
  }

  // A removal the store REFUSES re-reads the record and hands it back, which
  // is what makes a second attempt possible at all. The list is then one line
  // shorter, every key NEWER than the gap slides along, and the standing
  // « Supprimer cette note ? » came back attached to whichever note inherited
  // the number. « Confirmer » removed it.
  it("drops the question when the line it was asked about has gone", async () => {
    const removed: string[] = [];
    const onDeleteNote = async (n: Note) => {
      if (n.note === "milieu 2") {
        // another window got there first: the store re-reads, then re-raises
        setLignes(LIGNES.filter((x) => x.note !== "milieu 2"));
        throw new Error("Cette note a changé depuis son affichage");
      }
      removed.push(n.note);
    };
    await act(() => {
      root.render(<Registre onDeleteNote={onDeleteNote} />);
    });
    await click("Supprimer la note 3 du 2026-01-02T10:00");
    await click("Confirmer");
    await flush();

    const asked = [...container.querySelectorAll("button")].some(
      (b) => b.textContent === "Confirmer",
    );
    expect(
      asked,
      "the question outlived the line it was asked about, on a row that " +
        "inherited its number",
    ).toBe(false);
    expect(removed, "a note nobody pointed at was removed").toEqual([]);
  });

  // A line that has gone takes its act with it — and the words typed into
  // that act go too, so it is SAID. « La correction en cours a été
  // abandonnée » does not fire here: that sentence is for opening ANOTHER
  // editor, not for losing this one.
  it("says so when the line an editor was open on has gone", async () => {
    await act(() => {
      root.render(<Registre onEditNote={async () => {}} />);
    });
    await click("Modifier la note 2 du 2026-01-03T10:00");
    expect(noteEditor()).toBeTruthy();

    // a colleague removes that very line: nothing on this screen was clicked
    await act(async () => {
      setLignes(LIGNES.filter((x) => x.note !== "milieu 1"));
    });
    expect(
      noteEditor(),
      "the editor stayed open over a line that is not there",
    ).toBeFalsy();
    expect(text()).toContain("La correction en cours est abandonnée.");
  });

  // And the same slide on the SUCCESS path, which needs no refusal at all: an
  // act closes its OWN editor and not whichever one is open, so a removal
  // landing under an editor opened meanwhile left it standing — over a list
  // that had just lost a line, hence over a different note, with the
  // volunteer's words still in it.
  it("keeps an editor on its own line when a removal lands under it", async () => {
    let release: () => void = () => {};
    const rewritten: string[] = [];
    const onDeleteNote = async (n: Note) => {
      await new Promise<void>((r) => {
        release = r;
      });
      setLignes(LIGNES.filter((x) => x.note !== n.note));
    };
    const onEditNote = async (n: Note) => {
      rewritten.push(n.note);
    };
    await act(() => {
      root.render(
        <Registre onDeleteNote={onDeleteNote} onEditNote={onEditNote} />,
      );
    });
    await click("Supprimer la note 4 du 2026-01-01T10:00");
    await click("Confirmer");
    await click("Modifier la note 2 du 2026-01-03T10:00");
    const box = noteEditor();
    if (!box) throw new Error("no editor is open");
    await type(box, "corrigé pour milieu 1");
    await act(async () => {
      release();
      await new Promise((r) => setTimeout(r, 0));
    });
    await click("Enregistrer la note");
    await flush();

    expect(
      rewritten,
      "the correction was written into a line nobody was correcting",
    ).toEqual(["milieu 1"]);
  });

  // …AND IT IS SAID WHERE SOMETHING IS LISTENING. Written inside the history
  // card, the sentence went with it: a card holding ONE note is the ordinary
  // shape of a mayor contacted once, and `notes.length > 0` took the card,
  // the sentence and the editor away together.
  it("says so even when the line was the only one", async () => {
    await act(() => {
      root.render(
        <Registre
          depart={[ligne("la seule", "2026-01-04T10:00")]}
          onEditNote={async () => {}}
        />,
      );
    });
    await click("Modifier la note 1 du 2026-01-04T10:00");
    await act(async () => {
      setLignes([]);
    });
    expect(text()).toContain("n'est plus dans l'historique");
  });

  // The region PRE-EXISTS and only its text changes — a region inserted
  // together with its text is announced by some screen readers and dropped by
  // others, which is the whole doctrine. And the control the volunteer was
  // holding died WITH the row, from outside, so no click ran and
  // `holdFocusThrough` never armed.
  it("announces it in a region that was already there, and catches the focus", async () => {
    await act(() => {
      root.render(
        <Registre
          depart={[ligne("la seule", "2026-01-04T10:00")]}
          onEditNote={async () => {}}
        />,
      );
    });
    const regions = () => [
      ...container.querySelectorAll('[role="alert"],[role="status"]'),
    ];
    const before = regions();
    expect(before.length, "no live region to speak into").toBeGreaterThan(0);
    expect(before.map((r) => r.textContent).join("")).toBe("");

    await click("Modifier la note 1 du 2026-01-04T10:00");
    button("Enregistrer la note").focus();
    await act(async () => {
      setLignes([]);
    });

    const spoken = regions().filter((r) =>
      r.textContent?.includes("n'est plus dans l'historique"),
    );
    expect(spoken, "the sentence reaches no live region").toHaveLength(1);
    expect(
      before.includes(spoken[0]),
      "the region was inserted together with its text",
    ).toBe(true);
    expect(
      document.activeElement?.tagName,
      "the keyboard was left on <body>",
    ).not.toBe("BODY");
  });

  // ONE SENTENCE PER EVENT. The refusal that comes with the removal — « une
  // autre fenêtre l'a modifiée. Annulez pour voir son texte. » — is false when
  // the line was REMOVED, and it names a control that is no longer on screen.
  // Left standing beside the true one, two live regions read two contradictory
  // sentences one after the other.
  it("does not leave a refusal standing beside the line that has gone", async () => {
    const REFUS =
      "Cette note a changé depuis son affichage — une autre fenêtre l'a " +
      "modifiée. Annulez pour voir son texte.";
    await act(() => {
      root.render(
        <Registre
          depart={[ligne("la seule", "2026-01-04T10:00")]}
          onDeleteNote={async () => {
            // the store re-reads and re-raises, as browser mode does
            setLignes([]);
            throw new Error(REFUS);
          }}
        />,
      );
    });
    await click("Supprimer la note 1 du 2026-01-04T10:00");
    await click("Confirmer");
    await flush();

    expect(text()).toContain("n'est plus dans l'historique");
    expect(
      text(),
      "the refusal points at an Annuler that is gone",
    ).not.toContain("Annulez pour voir son texte");
  });

  // AND THE FOCUS IS CAUGHT EVEN WHEN IT DID NOT FALL. Browser mode keys a row
  // by its POSITION, so React reuses the row's DOM when a line vanishes: the
  // focused « Enregistrer la note » became « Modifier » under the very same
  // element, and a keyboard user's next Enter opened an editor on somebody
  // else's line. `document.activeElement` never reached <body>, so the rescue
  // that watches for that saw nothing.
  it("catches the focus when the row it was in was reused", async () => {
    await act(() => {
      root.render(
        <Registre
          depart={[
            ligne("la plus récente", "2026-01-07T10:00"),
            ligne("la plus ancienne", "2026-01-06T10:00"),
          ]}
          onEditNote={async () => {}}
        />,
      );
    });
    await click("Modifier la note 2 du 2026-01-06T10:00");
    button("Enregistrer la note").focus();
    expect(document.activeElement?.textContent).toBe("Enregistrer la note");

    // the other window removes that line: the surviving row inherits its key
    await act(async () => {
      setLignes([ligne("la plus récente", "2026-01-07T10:00")]);
    });
    expect(
      document.activeElement?.textContent,
      "the keyboard was left on a control the removal had renamed",
    ).not.toBe("Modifier");
    expect(document.activeElement?.id).toBe("contenu");
  });

  // ONE SENTENCE PER EVENT, INCLUDING THE NEXT ONE. What silences the store's
  // refusal is a ref, and a ref outlives the act that set it: not put back at
  // the start of the following one, it swallows that one's own refusal — a
  // dropped connection, a 409, a 500 — and the volunteer is told nothing at
  // all. The code puts it back; nothing saw that it did.
  it("says a refusal of its own after a line has gone", async () => {
    const boom = "La connexion a été perdue — la note n'est pas partie.";
    let fails = false;
    await act(() => {
      root.render(
        <Registre
          depart={[
            ligne("la première", "2026-01-08T10:00"),
            ligne("la seconde", "2026-01-07T10:00"),
          ]}
          onEditNote={async () => {
            if (fails) throw new Error(boom);
          }}
        />,
      );
    });
    await click("Modifier la note 1 du 2026-01-08T10:00");
    // a colleague removes the very line that editor is open on
    await act(async () => {
      setLignes([ligne("la seconde", "2026-01-07T10:00")]);
    });
    expect(text()).toContain("n'est plus dans l'historique");

    // …and the next act fails for a reason entirely its own
    fails = true;
    await click("Modifier la note 1 du 2026-01-07T10:00");
    await click("Enregistrer la note");
    await flush();
    expect(
      text(),
      "the refusal was swallowed by the line that went before it",
    ).toContain(boom);
  });

  // `sameLine` is NOT an identity — it leaves the text out, so an afternoon
  // of « à rappeler » produces lines that answer to it alike. Matched row by
  // row, the aim opened an editor on every one of them, sharing one draft.
  it("opens one editor when two lines share a minute and an outcome", async () => {
    await act(() => {
      root.render(
        <Registre
          depart={[
            ligne(
              "le secrétariat rappelle",
              "2026-01-06T17:00",
              "to_call_back",
            ),
            ligne("personne au bout", "2026-01-06T17:00", "to_call_back"),
          ]}
          onEditNote={async () => {}}
        />,
      );
    });
    await click("Modifier la note 2 du 2026-01-06T17:00");
    const open = [...container.querySelectorAll("label")].filter((l) =>
      l.textContent?.startsWith("Texte de la note"),
    );
    expect(open, "one aim, two editors, one draft between them").toHaveLength(
      1,
    );
  });

  // …AND ON THE ONE THE VOLUNTEER CLICKED. Counting the editors is not enough:
  // resolved by the fallback alone, the aim lands on the FIRST line answering
  // to the minute and the outcome — which in browser mode is the POSITION the
  // store writes at. The editor then looks right and corrects the line above.
  it("opens it on the line that was clicked, not on the first that resembles it", async () => {
    const at: number[] = [];
    await act(() => {
      root.render(
        <Registre
          depart={[
            ligne(
              "le secrétariat rappelle",
              "2026-01-06T17:00",
              "to_call_back",
            ),
            ligne("personne au bout", "2026-01-06T17:00", "to_call_back"),
          ]}
          onEditNote={async (_n, i) => {
            at.push(i);
          }}
        />,
      );
    });
    await click("Modifier la note 2 du 2026-01-06T17:00");
    await click("Enregistrer la note");
    await flush();
    expect(
      at,
      "the correction was written at the other line's position",
    ).toEqual([1]);
  });

  // A CHOICE MADE WHILE THE REQUEST WAS IN FLIGHT WINS OVER THE ONE BEING
  // HANDED BACK. The pick is restored through the SETTER for that reason, and
  // handing the captured value back instead reads identically in every test
  // that does not choose DURING the flight.
  it("hands the pick back without overwriting one made since", async () => {
    let refuse!: (e: Error) => void;
    await act(() => {
      root.render(
        <Registre
          depart={[ligne("la seule", "2026-01-04T10:00")]}
          onDeleteNote={() =>
            new Promise<void>((_ok, ko) => {
              refuse = ko;
            })
          }
        />,
      );
    });
    const select = () => container.querySelector("select") as HTMLSelectElement;
    const choose = (value: string) =>
      act(async () => {
        select().value = value;
        select().dispatchEvent(new Event("change", { bubbles: true }));
      });

    await choose("refused");
    await click("Supprimer la note 1 du 2026-01-04T10:00");
    await click("Confirmer");
    // the removal is out, and the volunteer chooses something else meanwhile
    await choose("email_sent");
    await act(async () => {
      refuse(new Error("refusé"));
      await new Promise((r) => setTimeout(r, 0));
    });
    expect(
      select().value,
      "the pick handed back overwrote the one made while the request was out",
    ).toBe("email_sent");
  });

  // AN EDITOR MUST NOT CLOSE BECAUSE THE CAMPAIGN CHANGED. The block that
  // clears everything a card owns is guarded on WHO and not on the render:
  // the email's basis moves whenever the campaign's texts or its logo do, and
  // a correction in progress has nothing to do with either.
  it("keeps an open editor when the campaign changes under it", async () => {
    function Campagne() {
      const [cfg, setCfg] = useState(EMPTY_CFG);
      return (
        <>
          <button
            type="button"
            onClick={() => setCfg({ ...EMPTY_CFG, candidat: "Ariane Fictive" })}
          >
            changer la campagne
          </button>
          <Fiche
            mayor={MAYOR}
            cfg={cfg}
            notes={[{ ...MINE, mine: true }]}
            noteRights={() => ({ edit: true, delete: true })}
            onEditNote={async () => {}}
            onBack={() => {}}
            onStatus={() => {}}
          />
        </>
      );
    }
    await act(() => {
      root.render(<Campagne />);
    });
    await click("Modifier la note 1 du 2026-01-02T10:00");
    const box = noteEditor();
    if (!box) throw new Error("no editor is open");
    await type(box, "corrigé pendant que la campagne bouge");

    await click("changer la campagne");
    expect(
      noteEditor()?.value,
      "the correction closed because the campaign moved under it",
    ).toBe("corrigé pendant que la campagne bouge");
  });

  // AND WHAT GOES OUT IS WHAT THE VOLUNTEER READ. The fallback that keeps an
  // editor over a line another window has corrected also matches a line that
  // merely INHERITED the minute and the outcome — a colleague removed the
  // aimed one and recorded their own contact in the same minute. Sending the
  // row's current note as `seen` made the store accept it, and the
  // colleague's words were replaced by a correction written for somebody
  // else's call.
  it("sends the line the volunteer read, not the one that took its place", async () => {
    const sent: string[] = [];
    await act(() => {
      root.render(
        <Registre
          depart={[ligne("original", "2026-01-05T10:00", "to_call_back")]}
          onEditNote={async (n) => {
            sent.push(n.note);
          }}
        />,
      );
    });
    await click("Modifier la note 1 du 2026-01-05T10:00");
    const box = noteEditor();
    if (!box) throw new Error("no editor is open");
    await type(box, "je corrige l'original");

    // the other window removes that line and records ANOTHER contact, in the
    // same minute and with the same outcome
    await act(async () => {
      setLignes([
        ligne("mot nouveau du collègue", "2026-01-05T10:00", "to_call_back"),
      ]);
    });
    await click("Enregistrer la note");
    await flush();
    expect(
      sent,
      "the store was handed the line now on screen, so it could not refuse",
    ).toEqual(["original"]);
  });
});
