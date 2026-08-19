import { useState } from "react";
import { useSubmitGuard } from "./common.tsx";
import * as M from "./messages.ts";
import type { Message, Templates } from "./types.ts";

// The six texts a campaign sends, editable by its coordination — and again,
// on top, by each of its teams.
//
// WHAT IS EMPTY IS INHERITED, and the screen has to make that visible or the
// whole design is a trap: a référent who sees the campaign's letter in a box
// and saves it has just frozen a copy, and every later correction the
// coordination makes stops reaching them. So the inherited text is the
// PLACEHOLDER of an empty box, never its value, and « revenir au texte … »
// empties it rather than pasting anything back.

const CHANNELS: { file: string; label: string; audience: string }[] = [
  {
    file: "email.txt",
    label: "Email",
    audience: "maire qui a déjà parrainé",
  },
  {
    file: "email_decouverte.txt",
    label: "Email",
    audience: "maire sans parrainage connu",
  },
  {
    file: "courrier.txt",
    label: "Courrier",
    audience: "maire qui a déjà parrainé",
  },
  {
    file: "courrier_decouverte.txt",
    label: "Courrier",
    audience: "maire sans parrainage connu",
  },
  {
    file: "telephone.txt",
    label: "Script téléphone",
    audience: "maire qui a déjà parrainé",
  },
  {
    file: "telephone_decouverte.txt",
    label: "Script téléphone",
    audience: "maire sans parrainage connu",
  },
];

export function ModelesMessages({
  /** "campaign" = a coordination's, "team" = one team's over its campaign's. */
  niveau,
  /** This level's own overrides, as they were last stored. */
  propres,
  /** What an empty box falls back to: the campaign's texts, or the image's. */
  herites,
  onSave,
  onEnregistre,
  onError,
  onMessage,
}: {
  niveau: "campaign" | "team" | "navigateur";
  propres: Templates;
  herites: Templates;
  /**
   * Stores the overlay and gives back WHAT WAS STORED — which is not what was
   * sent: an emptied text is an override removed. A route in team mode, an
   * IndexedDB write in the account-less one. Where they are saved is the
   * caller's business; `niveau` decides only the WORDS on screen.
   */
  onSave: (templates: Templates) => Promise<Templates>;
  onEnregistre: (templates: Templates) => void;
  onError: (e: unknown) => void;
  onMessage: (m: Message) => void;
}) {
  const [choisi, setChoisi] = useState(CHANNELS[0].file);
  const [brouillon, setBrouillon] = useState<Templates>(propres);
  const [envoi, setEnvoi] = useState(false);
  const [busy, done] = useSubmitGuard();

  const source = niveau === "team" ? "de la campagne" : "fourni";
  const hérité = herites[choisi] ?? M.SHIPPED_TEMPLATES[choisi] ?? "";
  const propre = brouillon[choisi] ?? "";
  const modifié = CHANNELS.some(
    (c) => (brouillon[c.file] ?? "") !== (propres[c.file] ?? ""),
  );

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    if (busy()) return; // a REF: state is a render behind
    setEnvoi(true);
    try {
      // what came BACK, not what went out: a text emptied is an override
      // removed, and the box has to show the inherited one in its place
      const stored = await onSave(brouillon);
      setBrouillon(stored);
      onEnregistre(stored);
      const n = Object.keys(stored).length;
      onMessage({
        tone: "ok",
        text:
          n === 0
            ? `Modèles enregistrés : aucun texte personnalisé, tous suivent ` +
              `le texte ${source}.`
            : `Modèles enregistrés (${n} texte${n > 1 ? "s" : ""} ` +
              `personnalisé${n > 1 ? "s" : ""}).`,
      });
    } catch (err) {
      // the ENGINE's own sentence, whoever raised it — the server reproduces
      // it in team mode, the engine itself answers in the account-less one —
      // and it names the file and the placeholder, which is what the person
      // looking at this box needs
      onError(err);
    } finally {
      done();
      setEnvoi(false);
    }
  };

  return (
    <form className="carte" onSubmit={save}>
      <h2 style={{ marginTop: 0 }}>Les modèles de messages</h2>
      <p className="gris">
        {niveau === "team" &&
          "Les textes que votre équipe envoie. Laissés vides, ils suivent " +
            "ceux de la campagne — y compris quand la coordination les " +
            "corrige ensuite."}
        {niveau === "campaign" &&
          "Les textes que la campagne envoie. Laissés vides, ils suivent " +
            "ceux fournis avec l'application — y compris quand une nouvelle " +
            "version les améliore. Chaque équipe peut les réécrire à son tour."}
        {niveau === "navigateur" &&
          "Les textes que vous envoyez. Laissés vides, ils suivent ceux " +
            "fournis avec l'application. Reprendre une campagne remplace ces " +
            "textes par les siens, comme elle remplace les autres champs. " +
            "Tout reste dans ce navigateur."}
      </p>
      <p>
        <label htmlFor="modele-choisi">Modèle</label>
        <select
          id="modele-choisi"
          value={choisi}
          onChange={(e) => setChoisi(e.target.value)}
        >
          {CHANNELS.map((c) => (
            <option key={c.file} value={c.file}>
              {c.label} — {c.audience}
              {brouillon[c.file] ? " (personnalisé)" : ""}
            </option>
          ))}
        </select>
      </p>
      <p>
        <label htmlFor="modele-texte">
          Texte {propre ? "personnalisé" : `(vide : suit le texte ${source})`}
        </label>
        <textarea
          id="modele-texte"
          rows={18}
          spellCheck
          aria-describedby="modele-champs"
          // the INHERITED text as placeholder and never as value: filled in,
          // it would be a copy frozen the day it was opened
          placeholder={hérité}
          value={propre}
          onChange={(e) =>
            setBrouillon({ ...brouillon, [choisi]: e.target.value })
          }
        />
      </p>
      <p className="gris" id="modele-champs">
        Champs disponibles dans ce modèle :{" "}
        {M.placeholderNames(choisi)
          .map((n) => `{${n}}`)
          .join(" ")}
        .{" "}
        {choisi.includes("_decouverte")
          ? "Ce modèle s'adresse à un maire dont aucun parrainage n'est " +
            "connu : il ne peut rien lui prêter."
          : "Ce modèle remercie un parrainage réel."}
      </p>
      <p>
        {/* NAMED FOR WHAT IT SAVES. « Ma campagne » in the account-less
            version now carries two save buttons — the nine fields, and these
            six texts — and two controls called « Enregistrer » on one screen
            are two controls a screen reader enumerates identically, with
            nothing to tell them apart. */}
        <button type="submit" aria-disabled={envoi}>
          {envoi ? "Enregistrement…" : "Enregistrer les modèles"}
        </button>{" "}
        {propre && (
          <button
            type="button"
            className="secondaire"
            onClick={() => {
              const reste = { ...brouillon };
              delete reste[choisi];
              setBrouillon(reste);
            }}
          >
            Revenir au texte {source}
          </button>
        )}{" "}
        {/* persistent, and only its TEXT changes: a region that mounts with
            its warning already written announces nothing */}
        <span className="gris" role="status">
          {modifié ? "modifications non enregistrées" : ""}
        </span>
      </p>
    </form>
  );
}
