import {
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import * as API from "./api.ts";
import { FormulaireConnexion } from "./Connexion.tsx";
import {
  Alerte,
  type CardDraft,
  Fiche,
  Guide,
  LogoCampagne,
  Marque,
  NavOnglets,
  PiedDePage,
  RenderGuard,
  SkipLink,
  ThemeToggle,
  useViewFocus,
} from "./common.tsx";
import { GestionEquipe } from "./TeamAdmin.tsx";
import { Tableau } from "./TeamDashboard.tsx";
import { ListeServeur } from "./TeamMayors.tsx";
import { Profil } from "./TeamProfile.tsx";
import { DemandeEquipe } from "./TeamRequest.tsx";
import type { Account, MayorCard, Me, Message, ServerConfig } from "./types.ts";

// Team mode: the work lives in PostgreSQL, shared and walled off per local
// team. This mode is what keeps two volunteers from writing to the same
// mayor — the browser version cannot.

const VIEW_TITLES: Record<string, string> = {
  guide: "Guide",
  tableau: "Mon tableau de bord",
  maires: "Les maires",
  equipe: "Mon équipe",
  profil: "Mon profil",
};

export default function Team({ config }: { config: ServerConfig }) {
  const [cfg, setCfg] = useState(config);
  const [me, setMe] = useState<Me | null>(null);
  const [tab, setTab] = useState("guide");
  // unsent card work (rewritten email, call note), keyed by INSEE: the
  // card is unmounted by any tab click — and by a lost session, exactly
  // when the note just failed to reach the server
  const cardDrafts = useRef<Record<string, CardDraft>>({});
  const lastAccount = useRef<string | null>(null);

  // a tab closed on a rewritten email or a half-typed call note is the
  // dearest text there is — the browser's dialog is the only word we get
  useEffect(() => {
    const warn = (e: BeforeUnloadEvent) => {
      // every touched entry, whatever campaign it is taken under: a
      // campaign field that appears in no email condemns nothing, and
      // filtering on it closed the tab on intact work without a word
      if (
        Object.values(cardDrafts.current).some(
          (d) => d.touched || d.note !== "",
        )
      ) {
        e.preventDefault();
      }
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, []);
  const [chosen, setChosen] = useState<API.Card | null>(null);
  const [message, setMessage] = useState<Message | null>(null);
  const [ready, setReady] = useState(false);

  const report = useCallback((e: unknown, whatNext?: string) => {
    const said = e instanceof Error ? e.message : String(e);
    setMessage({
      tone: "erreur",
      text: whatNext ? `${said} ${whatNext}` : said,
    });
  }, []);

  useEffect(() => {
    (async () => {
      // The link's own refusal outranks whatever the cookie's turn says
      // next. Written the other way round, a 502 or a dropped connection on
      // /api/me overwrote « ce lien n'est plus valable » with a network
      // error, and the reader went back to their inbox to click the same
      // dead link again.
      let linkSaidWhy = false;
      try {
        // The token a sign-in link carried, already out of the address bar
        // (main.tsx) and handed over exactly once: a second mount — the
        // outage screen recovering, StrictMode in development — gets
        // nothing, and asks the server for nothing.
        const token = API.consumeLinkToken();
        if (token) {
          try {
            const opened = await API.redeemLink(token);
            lastAccount.current = opened.account.email;
            setMe(opened);
            return;
          } catch (e) {
            // The API's own sentence — expired, already used, or naming an
            // account that is no longer active, one refusal for all three —
            // and what to do next, which its own words do not carry when
            // the failure was a network one: the token has been taken out
            // of the address bar by now, so the link in the inbox is the
            // only way back.
            report(e, "Rouvrez le lien reçu par email pour réessayer.");
            linkSaidWhy = true;
          }
        }
        const restored = await API.me();
        // seeded HERE too, not only on sign-in: the scenario the guard
        // exists for — shared computer, session lost overnight, someone
        // else signs in — is precisely the one where the session came
        // from a cookie, and lastAccount would still be null
        lastAccount.current = restored.account.email;
        setMe(restored);
      } catch (e) {
        // 401 on /api/me = not signed in yet: the normal state at first
        // load, not an outage
        if (!linkSaidWhy && (!(e instanceof API.APIError) || e.code !== 401)) {
          report(e);
        }
      } finally {
        setReady(true);
      }
    })();
  }, [report]);

  // Session expired server-side: back to the form, saying so.
  useEffect(() => {
    const lost = () => {
      setMe(null);
      setChosen(null);
      // without this the tab stays on "fiche", whose card has just been
      // unmounted: signing back in lands on an empty screen
      setTab("guide");
      setMessage({
        tone: "erreur",
        text:
          "Votre session a expiré. Reconnectez-vous — le travail déjà " +
          "enregistré est sur le serveur, il n'est pas perdu.",
      });
    };
    window.addEventListener(API.SESSION_LOST, lost);
    return () => window.removeEventListener(API.SESSION_LOST, lost);
  }, []);

  const signOut = async () => {
    try {
      await API.signOut();
    } finally {
      setMe(null);
      setChosen(null);
      setTab("guide");
      // the drafts carry notes about named mayors, and the next person on
      // this computer may be someone else entirely
      cardDrafts.current = {};
    }
  };

  // one view key per screen a volunteer can land on; the card's key
  // includes nothing of the mayor because tab leaves "fiche" before
  // another card can open
  useViewFocus(
    !ready ? "chargement" : !me ? "connexion" : tab,
    !ready
      ? null
      : !me
        ? "Connexion"
        : tab === "fiche"
          ? (chosen?.mayor.commune ?? "Fiche")
          : (VIEW_TITLES[tab] ?? "paraphe"),
  );

  if (!ready)
    // the shell, not a bare main: its live regions must exist BEFORE the
    // first message they will carry
    return (
      <Coquille cfg={cfg}>
        <p role="status">Chargement…</p>
      </Coquille>
    );
  if (!me) {
    return (
      <Coquille cfg={cfg} message={message} onMessage={setMessage}>
        <Connexion
          cfg={cfg}
          // a new attempt starts: whatever the SHELL was still saying — a
          // spent link, an expired session — is behind us, and leaving it
          // beside the form's own answer puts two live regions on screen
          // contradicting each other
          onAttempt={() => setMessage(null)}
          onSignedIn={async (m: Me) => {
            // Not the same person as before: their drafts are not yours.
            // signOut clears too, but a lost session skips signOut — and the
            // basis compares by VALUE, so identical campaign values alone
            // would otherwise hand Alice's card text to Bruno.
            if (
              lastAccount.current &&
              lastAccount.current !== m.account.email
            ) {
              cardDrafts.current = {};
            }
            lastAccount.current = m.account.email;
            setMe(m);
            setMessage(null);
            // the configuration may have changed since the page loaded
            const fresh = await API.detectMode();
            if (fresh.kind === "team") setCfg(fresh.config);
          }}
        />
      </Coquille>
    );
  }

  const account = me.account;
  const openCard = async (insee: string) => {
    try {
      setChosen(await API.card(insee));
      setTab("fiche");
    } catch (e) {
      report(e);
    }
  };

  return (
    <Coquille
      cfg={cfg}
      me={me}
      tab={tab}
      setTab={setTab}
      onSignOut={signOut}
      message={message}
      onMessage={setMessage}
    >
      {cfg.unfilled?.length > 0 && (
        <p className="alerte">
          {/* one line, not the list of nine labels: the campaign form marks
              each missing field itself, and this banner sits on every screen */}
          <strong>Campagne non configurée.</strong> Les messages contiennent
          encore des valeurs d'exemple : <strong>n'envoyez rien</strong> avant
          que la coordination ait renseigné la campagne.
          {me.may_manage && tab !== "equipe" && (
            <>
              {" "}
              <button
                type="button"
                className="lien"
                onClick={() => setTab("equipe")}
              >
                Ouvrir « Mon équipe »
              </button>
            </>
          )}
        </p>
      )}

      {tab === "guide" && (
        <>
          {/* no heading of its own: GUIDE.md opens on its h1, and a second
              one above it would be the page's only doubled title */}
          <p>
            <button type="button" onClick={() => setTab("tableau")}>
              Commencer — mon tableau de bord
            </button>
          </p>
          <Guide />
        </>
      )}

      {tab === "tableau" && (
        <Tableau
          cfg={cfg}
          me={me}
          onError={report}
          onOpen={openCard}
          onMessage={setMessage}
        />
      )}

      {tab === "maires" && <ListeServeur onError={report} onOpen={openCard} />}

      {tab === "fiche" && chosen && (
        <Fiche
          mayor={chosen.mayor}
          cfg={cfg.campaign}
          personalNote={account.personal_note}
          signer={account.name}
          drafts={cardDrafts}
          status={chosen.mayor.status}
          notes={chosen.notes ?? []}
          onBack={() => setTab("maires")}
          header={<ReserveePar mayor={chosen.mayor} me={account} />}
          onStatus={async (status: string, note: string) => {
            const fresh = await API.setStatus(
              chosen.mayor.insee_code,
              status,
              note,
            );
            setChosen(fresh);
          }}
        />
      )}

      {tab === "equipe" && (
        <GestionEquipe
          onError={report}
          me={me}
          cfg={cfg}
          onCfg={setCfg}
          onMessage={setMessage}
        />
      )}

      {tab === "profil" && (
        <Profil
          me={me}
          cfg={cfg}
          onError={report}
          onSaved={(personalNote: string) => {
            setMe((m) =>
              m
                ? {
                    ...m,
                    account: { ...m.account, personal_note: personalNote },
                  }
                : m,
            );
            setMessage({
              tone: "ok",
              text: "Votre touche personnelle est enregistrée.",
            });
          }}
        />
      )}
    </Coquille>
  );
}

interface CoquilleProps {
  cfg: ServerConfig;
  me?: Me | null;
  tab?: string;
  setTab?: (v: string) => void;
  onSignOut?: () => void;
  /**
   * The page-level message lives in the SHELL so its live region exists
   * from the very first render — loading screen included. A region that
   * only mounts with the signed-in tree appears together with its first
   * message when the two land in one React batch, and some assistive
   * technology never announces that.
   */
  message?: Message | null;
  onMessage?: (m: Message | null) => void;
  children: ReactNode;
}

function Coquille({
  cfg,
  me,
  tab,
  setTab,
  onSignOut,
  message,
  onMessage,
  children,
}: CoquilleProps) {
  const tabs: [string, string][] = [
    ["guide", "Guide"],
    ["tableau", "Mon tableau"],
    ["maires", "Les maires"],
    ...(me?.may_manage ? [["equipe", "Mon équipe"] as [string, string]] : []),
    ["profil", "Mon profil"],
  ];
  return (
    <>
      <SkipLink />
      <div className="tricolore" aria-hidden="true">
        <i />
        <i />
        <i />
      </div>
      <header>
        <Marque logo={cfg.logo} sous={cfg.campaign?.candidat} />
        {me && setTab && (
          <NavOnglets tabs={tabs} tab={tab ?? ""} onTab={setTab} />
        )}
        {me && (
          <span className="qui">
            {me.account.name}
            {me.account.team_name ? ` — ${me.account.team_name}` : ""}
            {" · "}
            <button type="button" className="lien" onClick={onSignOut}>
              déconnexion
            </button>
          </span>
        )}
        <ThemeToggle />
      </header>
      <div className="rayures" aria-hidden="true" />
      <RenderGuard>
        <main id="contenu" tabIndex={-1}>
          <Alerte
            message={message ?? null}
            onClose={onMessage ? () => onMessage(null) : undefined}
          />
          {children}
        </main>
      </RenderGuard>
      <PiedDePage sourceUrl={cfg.source_url}>
        <p>
          Le travail de l'équipe (statuts, notes, réservations) est enregistré
          sur le serveur de la campagne et partagé avec votre équipe locale.
        </p>
      </PiedDePage>
    </>
  );
}

function Connexion({
  cfg,
  onSignedIn,
  onAttempt,
}: {
  cfg: ServerConfig;
  onSignedIn: (m: Me) => void;
  onAttempt: () => void;
}) {
  // the disclosure that opens the public team-request form, below the
  // sign-in: whoever wants to gather a team around them has no account yet
  const [asking, setAsking] = useState(false);
  return (
    <>
      {/* The campaign's mark on the one page a volunteer reaches before the
          header carries anything of their own — and through the SAME
          component, so a media outage degrades here the way it degrades
          everywhere else instead of showing a broken image to a first-time
          visitor. */}
      <p className="logo-connexion">
        <LogoCampagne logo={cfg.logo} className="grand" />
      </p>
      <h1>Connexion</h1>
      <FormulaireConnexion
        magicLink={cfg.magic_link}
        onSignedIn={onSignedIn}
        onAttempt={onAttempt}
      >
        <p className="gris">
          Votre référent vous a communiqué un accès.
          {!cfg.magic_link && (
            <>
              {" "}
              Le mot de passe n'est affiché qu'une fois à sa création : s'il est
              perdu, il faut en regénérer un.
            </>
          )}
        </p>
      </FormulaireConnexion>
      {/* The toggle SURVIVES the form it opens: a disclosure that unmounts
          itself would drop focus to <body> on the way in and on the way
          back out. */}
      <p>
        <button
          type="button"
          className="lien"
          aria-expanded={asking}
          onClick={() => setAsking((v) => !v)}
        >
          {asking
            ? "Masquer la demande d'équipe"
            : "Pas encore d'accès ? Demander à créer une équipe"}
        </button>
      </p>
      {asking && <DemandeEquipe cfg={cfg} />}
    </>
  );
}

function ReserveePar({ mayor, me }: { mayor: MayorCard; me: Account }) {
  // « vous l'attribue » was true when writing a status claimed the card, and
  // it stopped being true when that claim was removed. Left standing it was
  // worse than a stale sentence: two volunteers each read that the fiche was
  // about to become theirs, both wrote, and both called the same mayor —
  // dressing the one case the new rule accepts up as a reservation. Taking a
  // lot is what reserves, and it is where this sends them.
  if (!mayor.volunteer) {
    return (
      <p className="gris">
        Fiche libre. Enregistrer un statut le note pour toute l'équipe, sans
        vous réserver la fiche : pour cela, prenez un lot depuis « Mon tableau
        ».
      </p>
    );
  }
  if (mayor.volunteer === me.email) {
    return <p className="gris">Cette fiche est la vôtre.</p>;
  }
  return (
    <p className="alerte">
      Réservée par <strong>{mayor.volunteer_name ?? mayor.volunteer}</strong>.
      Ne la contactez pas : votre enregistrement serait refusé.
    </p>
  );
}
