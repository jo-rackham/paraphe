import { useState } from "react";
import * as API from "./api.ts";
import { campaignLabel, label, ROLES } from "./common.tsx";
import * as M from "./messages.ts";
import type { Me, ServerConfig } from "./types.ts";

interface ProfilProps {
  me: Me;
  cfg: ServerConfig;
  onError: (e: unknown) => void;
  onSaved: (personalNote: string) => void;
}

export function Profil({ me, cfg, onError, onSaved }: ProfilProps) {
  const [personalNote, setPersonalNote] = useState(
    me.account.personal_note ?? "",
  );
  const [sending, setSending] = useState(false);
  return (
    <>
      <h1>Mon profil</h1>
      <div className="carte">
        <p>
          <strong>{me.account.name}</strong> — {me.account.email}
          <br />
          <span className="gris">
            Rôle : {label(ROLES, me.account.role)} · Équipe :{" "}
            {me.account.team_name ?? "nationale"}
            {me.departments.length > 0 && ` (${me.departments.join(", ")})`}
          </span>
        </p>
      </div>

      <div className="carte">
        <h2 style={{ marginTop: 0 }}>Votre touche personnelle</h2>
        <p className="gris">
          Une ou deux phrases à vous, insérées dans vos emails et vos courriers.
          C'est ce qui distingue votre message d'un publipostage.
        </p>
        <p>
          <label>
            {/* the visible title of the card, repeated for the ear only */}
            <span className="sr-only">Votre touche personnelle</span>
            <textarea
              rows={4}
              value={personalNote}
              onChange={(e) => setPersonalNote(e.target.value)}
            />
          </label>
        </p>
        <button
          type="button"
          disabled={sending}
          onClick={async () => {
            setSending(true);
            try {
              const r = await API.savePersonalNote(personalNote);
              onSaved(r.personal_note);
            } catch (e) {
              onError(e);
            } finally {
              setSending(false);
            }
          }}
        >
          {sending ? "Enregistrement…" : "Enregistrer"}
        </button>
      </div>

      <div className="carte">
        <h2 style={{ marginTop: 0 }}>La campagne</h2>
        <p className="gris">
          Ces valeurs remplissent tous les messages. Seule la coordination peut
          les changer, dans l'onglet « Mon équipe ».
        </p>
        <table>
          <tbody>
            {M.CAMPAIGN_KEYS.map((k) => (
              <tr key={k}>
                <td className="gris">{campaignLabel(k)}</td>
                <td>{cfg.campaign[k]}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}
