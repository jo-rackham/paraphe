import { useEffect, useState } from "react";
import * as API from "./api.ts";
import {
  Alerte,
  Hexagone,
  PiedDePage,
  RenderGuard,
  SkipLink,
  useViewFocus,
} from "./common.tsx";
import { Moderation } from "./InstanceModeration.tsx";
import { Demande } from "./InstanceRequest.tsx";
import type { InstanceConfig, Me, Message } from "./types.ts";

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

  useViewFocus(
    !ready
      ? "chargement"
      : me
        ? "moderation"
        : signingIn
          ? "connexion"
          : "accueil",
    !ready
      ? null
      : me
        ? "Demandes d'hébergement"
        : signingIn
          ? "Administration de l'instance"
          : "Héberger une campagne",
  );

  return (
    <>
      <SkipLink />
      <div className="tricolore" aria-hidden="true">
        <i />
        <i />
        <i />
      </div>
      <header>
        <span className="marque">
          <Hexagone />
          <span>
            paraphe
            <br />
            <span className="sous">{config.base_domain}</span>
          </span>
        </span>
        {me && (
          <span className="qui">
            {me.account.name}
            {" · "}
            <button type="button" className="lien" onClick={signOut}>
              déconnexion
            </button>
          </span>
        )}
      </header>
      <div className="rayures" aria-hidden="true" />
      <RenderGuard>
        <main id="contenu" tabIndex={-1}>
          <Alerte message={message} onClose={() => setMessage(null)} />
          {!ready && <p role="status">Chargement…</p>}
          {ready && me && <Moderation onMessage={setMessage} />}
          {ready && !me && !signingIn && (
            <Accueil
              config={config}
              onAdministration={() => setSigningIn(true)}
            />
          )}
          {ready && !me && signingIn && (
            <AdministrationSignIn
              config={config}
              onSignedIn={setMe}
              onBack={() => setSigningIn(false)}
            />
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

function Accueil({
  config,
  onAdministration,
}: {
  config: InstanceConfig;
  onAdministration: () => void;
}) {
  return (
    <>
      <h1>Héberger une campagne</h1>
      <p>
        Cette instance héberge, pour plusieurs campagnes, l'outil de recherche
        de parrainages d'élus. Chaque campagne reçoit son adresse (
        <code>votre-campagne.{config.base_domain}</code>), ses équipes locales
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
        <button type="button" className="lien" onClick={onAdministration}>
          Se connecter
        </button>
      </p>
    </>
  );
}

function AdministrationSignIn({
  config,
  onSignedIn,
  onBack,
}: {
  config: InstanceConfig;
  onSignedIn: (m: Me) => void;
  onBack: () => void;
}) {
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
      <form className="carte etroite" onSubmit={submit}>
        <Alerte message={error ? { tone: "erreur", text: error } : null} />
        <p>
          <label>
            Adresse email
            <input
              type="text"
              autoComplete="username"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
            />
          </label>
        </p>
        <p>
          <label>
            Mot de passe
            <input
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </label>
        </p>
        <button type="submit" disabled={sending}>
          {sending ? "Connexion…" : "Se connecter"}
        </button>
      </form>
      <p>
        <button type="button" className="lien" onClick={onBack}>
          Retour à l'accueil
        </button>
      </p>
    </>
  );
}
