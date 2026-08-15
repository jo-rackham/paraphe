import { useEffect, useState } from "react";
import * as API from "./api.ts";
import {
  Alerte,
  CompteurResultats,
  Hexagone,
  PiedDePage,
  RenderGuard,
  SkipLink,
  ThemeToggle,
  useSubmitGuard,
  useViewFocus,
} from "./common.tsx";
import { Moderation } from "./InstanceModeration.tsx";
import { Demande } from "./InstanceRequest.tsx";
import type { InstanceConfig, Me, Message } from "./types.ts";

// The instance landing page — the domain apex, when several campaigns are
// hosted. No work happens here: the page explains the tool, lists the
// hosted campaigns, and leads to the hosting request; the administration
// signs in to moderate.
//
// The form is public and creates nothing. Without moderation, the first
// abuse would be squatting a candidate's name, and the squatted campaign
// would have no recourse since the subdomain would already be taken.

type View = "accueil" | "demande" | "connexion";

export default function Instance({ config }: { config: InstanceConfig }) {
  const [me, setMe] = useState<Me | null>(null);
  const [ready, setReady] = useState(false);
  const [message, setMessage] = useState<Message | null>(null);
  const [view, setView] = useState<View>("accueil");

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
      setView("accueil");
    }
  };

  useViewFocus(
    !ready ? "chargement" : me ? "moderation" : view,
    !ready
      ? null
      : me
        ? "Demandes d'hébergement"
        : view === "connexion"
          ? "Administration de l'instance"
          : view === "demande"
            ? "Héberger une campagne"
            : "Chercher 500 parrainages, méthodiquement",
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
        <ThemeToggle />
      </header>
      <div className="rayures" aria-hidden="true" />
      <RenderGuard>
        <main id="contenu" tabIndex={-1}>
          <Alerte message={message} onClose={() => setMessage(null)} />
          {!ready && <p role="status">Chargement…</p>}
          {ready && me && <Moderation onMessage={setMessage} />}
          {ready && !me && view === "accueil" && (
            <Accueil
              config={config}
              onDemande={() => setView("demande")}
              onAdministration={() => setView("connexion")}
            />
          )}
          {ready && !me && view === "demande" && (
            <DemandeView config={config} onBack={() => setView("accueil")} />
          )}
          {ready && !me && view === "connexion" && (
            <AdministrationSignIn
              config={config}
              onSignedIn={setMe}
              onBack={() => setView("accueil")}
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
  onDemande,
  onAdministration,
}: {
  config: InstanceConfig;
  onDemande: () => void;
  onAdministration: () => void;
}) {
  return (
    <>
      <h1>Chercher 500 parrainages, méthodiquement</h1>
      <p>
        paraphe outille les campagnes présidentielles qui partent loin des
        projecteurs. Son point de départ : les parrainages accordés en 2017 et
        en 2022 aux candidats peu médiatisés — publiés par le Conseil
        constitutionnel — croisés avec le Répertoire national des élus et
        l'annuaire des mairies. De quoi savoir à qui parler d'abord, et comment
        joindre sa mairie.
      </p>
      <p>
        <button type="button" onClick={onDemande}>
          Héberger une campagne
        </button>
      </p>

      <h2>Ce que l'outil fait</h2>
      <div className="carte">
        <ul>
          <li>
            <strong>Prioriser</strong> — les 34 826 maires de France, chacun
            avec son historique : priorité à ceux qui ont déjà parrainé une
            candidature peu médiatisée, en 2017, en 2022, ou les deux.
          </li>
          <li>
            <strong>Travailler à plusieurs</strong> — des équipes par
            départements, des lots de fiches qu'on réserve pour ne jamais
            contacter deux fois la même mairie, un suivi partagé : à contacter,
            appelé, hésite, promis, signé.
          </li>
          <li>
            <strong>Écrire juste</strong> — emails, courriers et arguments
            d'appel générés depuis les modèles de la campagne, avec la touche
            personnelle de chaque bénévole, fiche par fiche.
          </li>
          <li>
            <strong>Cloisonner</strong> — chaque campagne sur sa propre adresse,
            son travail invisible des autres. Seule son adresse peut apparaître
            dans l'annuaire ci-dessous, et chaque campagne choisit d'y figurer
            ou non.
          </li>
        </ul>
      </div>

      <h2>Comment ça marche</h2>
      <div className="carte">
        <ol>
          <li>
            Vous demandez l'ouverture d'une campagne : une adresse, un nom, un
            mot d'explication.
          </li>
          <li>
            L'administration de l'instance examine la demande — c'est ce qui
            empêche quelqu'un de prendre l'adresse d'une campagne qui n'est pas
            la sienne.
          </li>
          <li>
            Acceptée, la campagne reçoit son accès de coordination : elle
            remplit ses textes, ouvre ses équipes locales, et le travail
            commence.
          </li>
        </ol>
        <p>
          <button type="button" onClick={onDemande}>
            Demander l'ouverture d'une campagne
          </button>
        </p>
      </div>

      <Annuaire />
      {config.browser_version_url && (
        <>
          <h2>Essayer sans compte</h2>
          <p>
            La <a href={config.browser_version_url}>version navigateur</a> offre
            le même outil, sans inscription : les listes se chargent dans votre
            navigateur et rien ne quitte votre poste. Pour travailler à
            plusieurs et partager le suivi, demandez plutôt l'hébergement d'une
            campagne.
          </p>
        </>
      )}
      <p className="gris">
        Vous administrez cette instance ?{" "}
        <button type="button" className="lien" onClick={onAdministration}>
          Se connecter
        </button>
      </p>
    </>
  );
}

function DemandeView({
  config,
  onBack,
}: {
  config: InstanceConfig;
  onBack: () => void;
}) {
  return (
    <>
      <h1>Héberger une campagne</h1>
      <p>
        Chaque campagne reçoit son adresse (
        <code>votre-campagne.{config.base_domain}</code>), ses équipes locales
        et son suivi — le travail des unes est invisible des autres.
      </p>
      <p className="alerte">
        Les demandes sont <strong>examinées avant ouverture</strong>. C'est ce
        qui empêche quelqu'un de prendre l'adresse d'une campagne qui n'est pas
        la sienne.
      </p>
      <Demande config={config} />
      <p>
        <button type="button" className="lien" onClick={onBack}>
          Retour à l'accueil
        </button>
      </p>
    </>
  );
}

// The public directory: what this instance hosts, filtered as you type.
// Only campaigns that chose to be listed appear — and none before its
// coordination has named it (a template name is no identity to advertise).
function Annuaire() {
  const [campaigns, setCampaigns] = useState<
    { slug: string; name: string }[] | null
  >(null);
  const [baseDomain, setBaseDomain] = useState("");
  const [failed, setFailed] = useState(false);
  const [q, setQ] = useState("");

  useEffect(() => {
    (async () => {
      try {
        const rep = await API.publicCampaigns();
        setCampaigns(rep.campaigns);
        setBaseDomain(rep.base_domain);
      } catch {
        // said out loud rather than an empty list passing for "none"
        setFailed(true);
      }
    })();
  }, []);

  if (failed) {
    return (
      <>
        <h2>Les campagnes hébergées</h2>
        <p className="gris">L'annuaire n'a pas pu être chargé.</p>
      </>
    );
  }
  if (campaigns === null) return null;
  if (campaigns.length === 0) {
    return (
      <>
        <h2>Les campagnes hébergées</h2>
        <p className="gris">Aucune campagne référencée pour l'instant.</p>
      </>
    );
  }

  const needle = q.trim().toLowerCase();
  const shown = campaigns.filter(
    (c) =>
      c.name.toLowerCase().includes(needle) ||
      c.slug.toLowerCase().includes(needle),
  );
  return (
    <>
      <h2>Les campagnes hébergées</h2>
      <div className="carte">
        <p>
          <label>
            Rechercher une campagne
            <input
              type="search"
              value={q}
              onChange={(e) => setQ(e.target.value)}
            />
          </label>
        </p>
        <CompteurResultats shown={shown.length} total={campaigns.length} />
        <ul>
          {shown.map((c) => (
            <li key={c.slug}>
              <a href={`https://${c.slug}.${baseDomain}/`}>{c.name}</a>{" "}
              <code>
                {c.slug}.{baseDomain}
              </code>
            </li>
          ))}
        </ul>
      </div>
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
  const [busy, done] = useSubmitGuard();

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    // a REF, not `sending`: aria-disabled keeps the button clickable, and
    // two submits in one tick both read the state of the render they were
    // created in — one sign-in then spends two of its ten attempts
    if (busy()) return;
    setError(null);
    setSending(true);
    try {
      onSignedIn(await API.signIn(email.trim(), password));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      done();
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
        <button type="submit" aria-disabled={sending || undefined}>
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
