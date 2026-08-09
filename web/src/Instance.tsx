import { useEffect, useState } from "react";
import * as API from "./api.ts";
import {
  Alerte, Hexagone, label, ORG_STATES, PiedDePage, RenderGuard, REQUEST_STATES,
} from "./common.tsx";
import type {
  InstanceConfig, Me, Message, ModerationQueue, QueuedRequest,
} from "./types.ts";

// The instance landing page — the domain apex, when several campaigns are
// hosted. No work happens here: campaigns request their hosting, and the
// administration answers.
//
// The form is public and creates nothing. Without moderation, the first
// abuse would be squatting a candidate's name, and the squatted campaign
// would have no recourse since the subdomain would already be taken.

export default function Instance({ config }: { config: InstanceConfig }) {
  const [me, setMe] = useState<Me | null>(null);
  const [ready, setReady] = useState(false);
  const [message, setMessage] = useState<Message | null>(null);
  const [signingIn, setSigningIn] = useState(false);

  useEffect(() => {
    (async () => {
      try {
        setMe(await API.me());
      } catch (e) {
        // 401 = visitor not signed in: this page's normal state
        if (!(e instanceof API.APIError) || e.code !== 401) {
          setMessage({ tone: "erreur", text: (e as Error).message });
        }
      } finally {
        setReady(true);
      }
    })();
  }, []);

  const signOut = async () => {
    try {
      await API.signOut();
    } finally {
      setMe(null);
      setSigningIn(false);
    }
  };

  return (
    <>
      <div className="tricolore"><i /><i /><i /></div>
      <header>
        <span className="marque">
          <Hexagone />
          <span>paraphe<br /><span className="sous">{config.base_domain}</span></span>
        </span>
        {me && (
          <span className="qui">
            {me.account.name}
            {" · "}
            <button className="lien" onClick={signOut}>déconnexion</button>
          </span>
        )}
      </header>
      <div className="rayures" />
      <RenderGuard>
        <main>
          <Alerte message={message} onClose={() => setMessage(null)} />
          {!ready && <p>Chargement…</p>}
          {ready && me && <Moderation onMessage={setMessage} />}
          {ready && !me && !signingIn && (
            <Accueil config={config} onAdministration={() => setSigningIn(true)} />
          )}
          {ready && !me && signingIn && (
            <AdministrationSignIn config={config} onSignedIn={setMe}
              onBack={() => setSigningIn(false)} />
          )}
        </main>
      </RenderGuard>
      <PiedDePage sourceUrl={config.source_url}>
        <p>
          Chaque campagne hébergée ici travaille sur son propre sous-domaine et
          ne voit que son propre travail. La liste des maires, elle, est
          publique et commune.
        </p>
      </PiedDePage>
    </>
  );
}

function Accueil(
  { config, onAdministration }: { config: InstanceConfig; onAdministration: () => void },
) {
  return (
    <>
      <h1>Héberger une campagne</h1>
      <p>
        Cette instance héberge, pour plusieurs campagnes, l'outil de recherche
        de parrainages d'élus. Chaque campagne reçoit son adresse
        (<code>votre-campagne.{config.base_domain}</code>), ses équipes locales
        et son suivi — invisibles des autres.
      </p>
      <p className="alerte">
        Les demandes sont <strong>examinées avant ouverture</strong>. C'est ce
        qui empêche quelqu'un de prendre l'adresse d'une campagne qui n'est pas
        la sienne.
      </p>
      <Demande config={config} />
      <p className="gris">
        Vous administrez cette instance ?{" "}
        <button className="lien" onClick={onAdministration}>Se connecter</button>
      </p>
    </>
  );
}

function Demande({ config }: { config: InstanceConfig }) {
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
    setError(null);
    setSending(true);
    try {
      const rep = await API.requestCampaign({
        slug: slug.trim().toLowerCase(), name: name.trim(),
        requester_name: requesterName.trim(),
        requester_email: requesterEmail.trim(), message: pitch.trim(),
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
      <p><label>Adresse souhaitée
        <input type="text" value={slug} placeholder="ma-campagne"
          onChange={(e) => setSlug(e.target.value)} />
      </label></p>
      <p className="gris">
        L'adresse sera <code>{(slug.trim() || "ma-campagne").toLowerCase()}
          .{config.base_domain}</code> — minuscules, chiffres et tirets.
      </p>
      <p><label>Nom de la campagne
        <input type="text" value={name} placeholder="Campagne de …"
          onChange={(e) => setName(e.target.value)} />
      </label></p>
      <p><label>Votre nom
        <input type="text" value={requesterName}
          onChange={(e) => setRequesterName(e.target.value)} />
      </label></p>
      <p><label>Votre adresse email
        <input type="text" value={requesterEmail}
          onChange={(e) => setRequesterEmail(e.target.value)} />
      </label></p>
      <p className="gris">
        C'est à cette adresse que la réponse sera envoyée, et c'est elle qui
        ouvrira l'accès de coordination si la demande est acceptée.
      </p>
      <p><label>En quelques mots, la campagne
        <textarea rows={4} value={pitch}
          onChange={(e) => setPitch(e.target.value)} />
      </label></p>
      <button type="submit" disabled={sending}>
        {sending ? "Envoi…" : "Envoyer la demande"}
      </button>
    </form>
  );
}

function AdministrationSignIn(
  { config, onSignedIn, onBack }: {
    config: InstanceConfig; onSignedIn: (m: Me) => void; onBack: () => void;
  },
) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [sending, setSending] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setSending(true);
    try {
      onSignedIn(await API.signIn(email.trim(), password));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSending(false);
    }
  };

  return (
    <>
      <h1>Administration de l'instance</h1>
      {config.no_account && (
        <p className="alerte erreur">
          <strong>Aucun compte n'existe encore.</strong> Démarrez l'application
          avec PARAPHE_INSTANCE_ADMIN_EMAIL et PARAPHE_INSTANCE_ADMIN_PASSWORD
          pour créer le premier accès.
        </p>
      )}
      <form className="carte" onSubmit={submit}>
        <Alerte message={error ? { tone: "erreur", text: error } : null} />
        <p><label>Adresse email
          <input type="text" autoComplete="username" value={email}
            onChange={(e) => setEmail(e.target.value)} />
        </label></p>
        <p><label>Mot de passe
          <input type="password" autoComplete="current-password" value={password}
            onChange={(e) => setPassword(e.target.value)} />
        </label></p>
        <button type="submit" disabled={sending}>
          {sending ? "Connexion…" : "Se connecter"}
        </button>
      </form>
      <p><button className="lien" onClick={onBack}>Retour à l'accueil</button></p>
    </>
  );
}

function Moderation({ onMessage }: { onMessage: (m: Message) => void }) {
  const [queue, setQueue] = useState<ModerationQueue | null>(null);
  const [busy, setBusy] = useState<number | null>(null);
  const [reasons, setReasons] = useState<Record<number, string>>({});
  // The coordination password is returned only ONCE: it does not go back
  // to the database in the clear, and there is no way to retrieve it
  // afterwards.
  const [opened, setOpened] = useState<{
    address: string; coordination: string; password: string;
  } | null>(null);

  const load = async () => {
    try {
      setQueue(await API.moderationQueue());
    } catch (e) {
      onMessage({ tone: "erreur", text: (e as Error).message });
    }
  };
  useEffect(() => { void load(); }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const decide = async (d: QueuedRequest, decision: "accepted" | "refused") => {
    setBusy(d.id);
    try {
      const rep = await API.decideRequest(d.id, decision, reasons[d.id] ?? "");
      if (rep.password && rep.address && rep.coordination) {
        setOpened({
          address: rep.address, coordination: rep.coordination,
          password: rep.password,
        });
      } else {
        onMessage({ tone: "ok", text: `Demande ${d.slug} refusée.` });
      }
      await load();
    } catch (e) {
      onMessage({ tone: "erreur", text: (e as Error).message });
    } finally {
      setBusy(null);
    }
  };

  if (!queue) return <p>Chargement…</p>;
  const pending = queue.requests.filter((d) => d.state === "pending");
  const decided = queue.requests.filter((d) => d.state !== "pending");

  return (
    <>
      <h1>Demandes d'hébergement</h1>

      {opened && (
        <div className="carte">
          <h2>Campagne ouverte : {opened.address}</h2>
          <p>
            Transmettez ces accès à {opened.coordination}. Le mot de passe
            n'est affiché <strong>qu'une seule fois</strong> — il n'est stocké
            nulle part en clair.
          </p>
          <p><code>{opened.password}</code></p>
          <button onClick={() => setOpened(null)}>J'ai noté</button>
        </div>
      )}

      {pending.length === 0 && <p className="gris">Aucune demande en attente.</p>}
      {pending.map((d) => (
        <div className="carte" key={d.id}>
          <h2>{d.name}</h2>
          <p>
            <code>{d.slug}.{queue.base_domain}</code> — demandée par{" "}
            {d.requester_name} ({d.requester_email}), le {d.ts}
          </p>
          {d.message && <p>{d.message}</p>}
          <p><label>Motif (transmis au demandeur en cas de refus)
            <input type="text" value={reasons[d.id] ?? ""}
              onChange={(e) => setReasons({ ...reasons, [d.id]: e.target.value })} />
          </label></p>
          <p>
            <button disabled={busy === d.id}
              onClick={() => decide(d, "accepted")}>
              Ouvrir la campagne
            </button>{" "}
            <button className="lien" disabled={busy === d.id}
              onClick={() => decide(d, "refused")}>
              Refuser
            </button>
          </p>
        </div>
      ))}

      <h2>Campagnes hébergées</h2>
      <table>
        <thead>
          <tr><th>Adresse</th><th>Nom</th><th>État</th><th>Depuis</th></tr>
        </thead>
        <tbody>
          {queue.organisations.map((o) => (
            <tr key={o.id}>
              <td><code>{o.slug}.{queue.base_domain}</code></td>
              <td>{o.name}</td><td>{label(ORG_STATES, o.state)}</td>
              <td>{o.created_at}</td>
            </tr>
          ))}
        </tbody>
      </table>

      {decided.length > 0 && (
        <>
          <h2>Demandes traitées</h2>
          <table>
            <thead>
              <tr><th>Adresse</th><th>Décision</th><th>Motif</th><th>Par</th></tr>
            </thead>
            <tbody>
              {decided.map((d) => (
                <tr key={d.id}>
                  <td><code>{d.slug}</code></td>
                  <td>{label(REQUEST_STATES, d.state)}</td>
                  <td>{d.reason}</td><td>{d.decided_by}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </>
  );
}
