import { useState } from "react";
import * as API from "./api.ts";
import { Alerte } from "./common.tsx";
import type { InstanceConfig } from "./types.ts";

export function Demande({ config }: { config: InstanceConfig }) {
  const [slug, setSlug] = useState("");
  const [name, setName] = useState("");
  const [requesterName, setRequesterName] = useState("");
  const [requesterEmail, setRequesterEmail] = useState("");
  const [pitch, setPitch] = useState("");
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sent, setSent] = useState<string | null>(null);

  if (sent) {
    return (
      <div className="carte">
        <h2>Demande enregistrée</h2>
        <p>{sent}</p>
      </div>
    );
  }

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (sending) return; // aria-disabled greys the button but keeps it live
    setError(null);
    setSending(true);
    try {
      const rep = await API.requestCampaign({
        slug: slug.trim().toLowerCase(),
        name: name.trim(),
        requester_name: requesterName.trim(),
        requester_email: requesterEmail.trim(),
        message: pitch.trim(),
      });
      setSent(rep.message);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
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
      <button type="submit" aria-disabled={sending || undefined}>
        {sending ? "Envoi…" : "Envoyer la demande"}
      </button>
    </form>
  );
}
