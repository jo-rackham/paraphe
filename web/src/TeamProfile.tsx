import { useState } from "react";
import * as API from "./api.ts";
import {
  campaignLabel,
  label,
  MIN_PASSWORD,
  ROLES,
  useSubmitGuard,
} from "./common.tsx";
import * as M from "./messages.ts";
import type { Me, Message, ServerConfig } from "./types.ts";

interface ProfilProps {
  me: Me;
  cfg: ServerConfig;
  onError: (e: unknown) => void;
  onSaved: (personalNote: string, phoneOutreach: boolean | null) => void;
  /** the page-level live region, in the shell: see Team.tsx */
  onMessage: (m: Message) => void;
}

export function Profil({ me, cfg, onError, onSaved, onMessage }: ProfilProps) {
  const [personalNote, setPersonalNote] = useState(
    me.account.personal_note ?? "",
  );
  // THREE states, and « suivre la campagne » is the one a volunteer who has
  // never opened this screen is in — it must keep following as the campaign
  // changes its mind, not freeze at today's value.
  const [appel, setAppel] = useState<boolean | null>(
    me.account.phone_outreach ?? null,
  );
  const [sending, setSending] = useState(false);
  const [busy, done] = useSubmitGuard();
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
        <p>
          <label htmlFor="mon-appel">
            Appels téléphoniques
            <select
              id="mon-appel"
              value={appel === null ? "campagne" : appel ? "oui" : "non"}
              onChange={(e) =>
                setAppel(
                  e.target.value === "campagne"
                    ? null
                    : e.target.value === "oui",
                )
              }
            >
              <option value="campagne">
                Comme la campagne (
                {cfg.phone_outreach ? "j'appelle" : "je n'appelle pas"})
              </option>
              <option value="oui">J'appelle les maires que je contacte</option>
              <option value="non">Je n'appelle pas</option>
            </select>
          </label>
        </p>
        <p className="gris">
          Décide si vos messages proposent un échange téléphonique et annoncent
          un appel. Ne promettez un appel que si vous comptez le passer.
        </p>
        <button
          type="button"
          aria-disabled={sending || undefined}
          onClick={async () => {
            if (busy()) return;
            setSending(true);
            try {
              const r = await API.savePersonalNote(personalNote, appel);
              onSaved(r.personal_note, r.phone_outreach);
            } catch (e) {
              onError(e);
            } finally {
              done();
              setSending(false);
            }
          }}
        >
          {sending ? "Enregistrement…" : "Enregistrer"}
        </button>
      </div>

      <MotDePasse onError={onError} onMessage={onMessage} />

      <div className="carte">
        <h2 style={{ marginTop: 0 }}>La campagne</h2>
        <p className="gris">
          Ces valeurs remplissent tous les messages. Seule la coordination peut
          les changer, dans son onglet « Ma campagne ».
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

/**
 * Choosing one's own password.
 *
 * The CURRENT one is asked for, and that is the whole guard: a session
 * cookie is a bearer token with twelve hours on it, so without this proof
 * whoever picked one up off a shared computer would turn a borrowed
 * afternoon into permanent ownership — with the owner locked out.
 *
 * Confirming the new one is not ceremony either: nothing here can show what
 * was typed, and a password nobody can read back is a password a typo turns
 * into an account nobody opens again.
 */
function MotDePasse({
  onError,
  onMessage,
}: {
  onError: (e: unknown) => void;
  onMessage: (m: Message) => void;
}) {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [sending, setSending] = useState(false);
  // A REF, never state: two clicks in the same tick run two handlers built
  // by the same render and both read `sending` as it was before either of
  // them. On THIS form that spends two of the ten attempts an account is
  // allowed per quarter of an hour, and the second arrives with a password
  // the first has already replaced.
  const [busy, done] = useSubmitGuard();
  // Said HERE rather than through the page-level region: the two passwords
  // not matching is about these two fields, and the answer belongs beside
  // them. A success, on the other hand, unmounts nothing and is worth the
  // shell's region, which is what a screen reader is watching.
  const [refus, setRefus] = useState("");

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (busy()) return;
    if (next !== confirm) {
      done();
      setRefus(
        "Les deux nouveaux mots de passe ne correspondent pas — rien n'a été " +
          "changé.",
      );
      return;
    }
    setRefus("");
    setSending(true);
    try {
      await API.changePassword(current, next);
      setCurrent("");
      setNext("");
      setConfirm("");
      onMessage({
        tone: "ok",
        text:
          "Votre mot de passe est changé. Vos autres sessions — un autre " +
          "navigateur, un téléphone, un poste partagé — ont été déconnectées.",
      });
    } catch (e) {
      // through the SCREEN's own slot, not the page's: « mot de passe actuel
      // incorrect » is an answer to the field just above it
      setRefus(e instanceof Error ? e.message : String(e));
      onError(e);
    } finally {
      done();
      setSending(false);
    }
  };

  return (
    <form className="carte" onSubmit={submit}>
      <h2 style={{ marginTop: 0 }}>Changer mon mot de passe</h2>
      <p className="gris">
        Celui que votre référent vous a communiqué est passé par une
        conversation, un SMS ou un email. Choisir le vôtre le retire de là.
        Changer de mot de passe <strong>déconnecte vos autres sessions</strong>{" "}
        — c'est ce qui sert si vous pensez que quelqu'un d'autre l'a eu.
      </p>
      {/* the region PRE-EXISTS its first message and holds no control:
          mounted with its text, some assistive technology never reads it */}
      <p className={refus ? "alerte erreur" : "sr-only"}>
        <span role="alert">{refus}</span>
      </p>
      <p>
        <label>
          Mot de passe actuel
          <input
            type="password"
            autoComplete="current-password"
            required
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
          />
        </label>
      </p>
      <p>
        <label>
          Nouveau mot de passe
          <input
            type="password"
            autoComplete="new-password"
            required
            minLength={MIN_PASSWORD}
            value={next}
            onChange={(e) => setNext(e.target.value)}
          />
        </label>
      </p>
      <p className="gris">
        Au moins {MIN_PASSWORD} caractères. Trois ou quatre mots sans rapport
        font un bon mot de passe, et se retiennent.
      </p>
      <p>
        <label>
          Répétez le nouveau mot de passe
          <input
            type="password"
            autoComplete="new-password"
            required
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
          />
        </label>
      </p>
      {/* aria-disabled and never disabled: `disabled` on the button holding
          the focus drops that focus to <body>. The re-entry guard above is
          what stops the second click. */}
      <button type="submit" aria-disabled={sending || undefined}>
        {sending ? "Changement…" : "Changer mon mot de passe"}
      </button>
    </form>
  );
}
