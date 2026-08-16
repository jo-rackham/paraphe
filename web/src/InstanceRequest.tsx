import { useState } from "react";
import * as API from "./api.ts";
import { Alerte, rescueFocusAfterCommit, useSubmitGuard } from "./common.tsx";
import type { InstanceConfig } from "./types.ts";

export function Demande({ config }: { config: InstanceConfig }) {
  const [slug, setSlug] = useState("");
  const [name, setName] = useState("");
  const [requesterName, setRequesterName] = useState("");
  const [requesterEmail, setRequesterEmail] = useState("");
  const [pitch, setPitch] = useState("");
  const [listed, setListed] = useState(true);
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
    // a REF, not `sending`: aria-disabled keeps the button clickable, and
    // two submits in one tick both read the state of the render they were
    // created in — two rows in the moderation queue for one intent
    if (busy()) return;
    setError(null);
    setSending(true);
    try {
      const rep = await API.requestCampaign({
        slug: slug.trim().toLowerCase(),
        name: name.trim(),
        requester_name: requesterName.trim(),
        requester_email: requesterEmail.trim(),
        message: pitch.trim(),
        listed,
      });
      // the whole form — the pressed button included — is replaced by the
      // confirmation: focus would fall to <body> and the next Tab restart
      // at the skip link
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
    <form className="carte" onSubmit={submit}>
      <h2>Demander l'ouverture d'une campagne</h2>
      <Alerte message={error ? { tone: "erreur", text: error } : null} />
      <p>
        <label>
          Adresse souhaitée
          <input
            type="text"
            required
            value={slug}
            placeholder="ma-campagne"
            onChange={(e) => setSlug(e.target.value)}
          />
        </label>
      </p>
      <p className="gris">
        L'adresse sera{" "}
        <code>
          {(slug.trim() || "ma-campagne").toLowerCase()}.{config.base_domain}
        </code>{" "}
        — minuscules, chiffres et tirets.
      </p>
      <p>
        <label>
          Nom de la campagne
          <input
            type="text"
            required
            value={name}
            placeholder="Campagne de …"
            onChange={(e) => setName(e.target.value)}
          />
        </label>
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
        C'est à cette adresse que la réponse sera envoyée, et c'est elle qui
        ouvrira l'accès de coordination si la demande est acceptée.
      </p>
      <p>
        <label>
          En quelques mots, la campagne
          <textarea
            rows={4}
            value={pitch}
            onChange={(e) => setPitch(e.target.value)}
          />
        </label>
      </p>
      <p>
        <label>
          <input
            type="checkbox"
            checked={listed}
            onChange={(e) => setListed(e.target.checked)}
          />{" "}
          Référencer la campagne dans l'annuaire public de {config.base_domain}
        </label>
      </p>
      <p className="gris">
        Décochez pour préparer la campagne discrètement : son adresse ne sera
        pas affichée sur cet accueil. La coordination pourra changer ce choix à
        tout moment.
      </p>
      <button type="submit" aria-disabled={sending || undefined}>
        {sending ? "Envoi…" : "Envoyer la demande"}
      </button>
    </form>
  );
}
