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
        onStatus={() => {}}
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
