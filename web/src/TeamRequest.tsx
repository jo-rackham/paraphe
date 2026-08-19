import { useState } from "react";
import * as API from "./api.ts";
import { Alerte, rescueFocusAfterCommit, useSubmitGuard } from "./common.tsx";
import type { ServerConfig } from "./types.ts";

// Asking a campaign to open a local team, from its sign-in screen: whoever
// wants to gather a team around them has no account yet, and this is the one
// door open to them. It creates nothing — the campaign's coordination
// decides, and it is that decision which opens the team and its lead access.

export function DemandeEquipe({ cfg }: { cfg: ServerConfig }) {
  const [name, setName] = useState("");
  const [departments, setDepartments] = useState<string[]>([]);
  const [requesterName, setRequesterName] = useState("");
  const [requesterEmail, setRequesterEmail] = useState("");
  const [pitch, setPitch] = useState("");
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sent, setSent] = useState<string | null>(null);
  // before the early return: a hook runs on every render or on none
  const [busy, done] = useSubmitGuard();

  if (sent) {
    return (
      <div className="carte">
        <h2>Demande enregistrée</h2>
        <p role="status">{sent}</p>
      </div>
    );
  }

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    // a REF, not `sending`: aria-disabled keeps the button clickable, and two
    // submits in one tick both read the state of the render they were created
    // in — two rows in the coordination's queue for one intent
    if (busy()) return;
    setError(null);
    setSending(true);
    try {
      const rep = await API.requestTeam({
        name: name.trim(),
        departments,
        requester_name: requesterName.trim(),
        requester_email: requesterEmail.trim(),
        message: pitch.trim(),
      });
      // the whole form — the pressed button included — is replaced by the
      // confirmation: focus would fall to <body> and the next Tab restart at
      // the skip link
      rescueFocusAfterCommit();
      setSent(rep.message);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      done();
      setSending(false);
    }
  };

  return (
    <form className="carte etroite" onSubmit={submit}>
      <h2 style={{ marginTop: 0 }}>Demander à créer une équipe</h2>
      <Alerte message={error ? { tone: "erreur", text: error } : null} />
      <p className="gris">
        Une équipe locale reçoit ses lots dans ses départements et ouvre
        elle-même les accès de ses bénévoles. Elle lit toute la liste de la
        campagne : le périmètre décide d'où viennent les lots, pas de ce qu'on a
        le droit de lire ni d'enregistrer. La coordination de la campagne
        examine la demande : rien n'est créé avant son accord.
      </p>
      <p>
        <label>
          Nom de l'équipe
          <input
            type="text"
            required
            value={name}
            placeholder="Équipe du Nord, Comité de Lyon…"
            onChange={(e) => setName(e.target.value)}
          />
        </label>
      </p>
      <p>
        <label>
          Départements (plusieurs possibles)
          <select
            multiple
            size={6}
            value={departments}
            onChange={(e) =>
              setDepartments([...e.target.selectedOptions].map((o) => o.value))
            }
          >
            {cfg.departments.map((d) => (
              <option key={d} value={d}>
                {d}
              </option>
            ))}
          </select>
        </label>
      </p>
      <p className="gris">
        Aucun département sélectionné : l'équipe travaillerait partout. La
        coordination peut corriger ce périmètre en acceptant.
      </p>
      <p>
        <label>
          Votre nom
          <input
            type="text"
            autoComplete="name"
            required
            value={requesterName}
            onChange={(e) => setRequesterName(e.target.value)}
          />
        </label>
      </p>
      <p>
        <label>
          Votre adresse email
          <input
            type="text"
            autoComplete="email"
            required
            value={requesterEmail}
            onChange={(e) => setRequesterEmail(e.target.value)}
          />
        </label>
      </p>
      <p className="gris">
        C'est à cette adresse que la coordination répondra, et c'est elle qui
        ouvrira votre accès de référent si la demande est acceptée.
      </p>
      <p>
        <label>
          En quelques mots, qui vous êtes et ce que vous comptez faire
          <textarea
            rows={4}
            value={pitch}
            onChange={(e) => setPitch(e.target.value)}
          />
        </label>
      </p>
      <button type="submit" aria-disabled={sending || undefined}>
        {sending ? "Envoi…" : "Envoyer la demande"}
      </button>
    </form>
  );
}
