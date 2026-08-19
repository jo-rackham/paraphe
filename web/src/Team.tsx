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
  gestionLabel,
  httpUrl,
  LogoCampagne,
  Marque,
  NavOnglets,
  PiedDePage,
  RenderGuard,
  SkipLink,
  ThemeToggle,
  useViewFocus,
} from "./common.tsx";
import { navigate, useView } from "./route.tsx";
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
  profil: "Mon profil",
};

// The address bar names the screen. A volunteer's back gesture — the one
// they make without thinking, on a phone, in the middle of a card — used to
// leave the application entirely, with a rewritten email and a half-typed
// call note behind it. And a card now has an address, which is what makes
// « regarde la fiche de Saint-Marcel » a link between two volunteers: no
// card of a campaign is refused to a team of it any more, so that link
// works for whoever receives it.
const TEAM_VIEWS = ["guide", "tableau", "maires", "equipe", "profil"] as const;

export default function Team({ config }: { config: ServerConfig }) {
  const [cfg, setCfg] = useState(config);
  const [me, setMe] = useState<Me | null>(null);
  const { view, card: routedCard, go: setTab } = useView(TEAM_VIEWS, "guide");
  // « fiche » is not a view of its own in the address bar: a card lives
  // UNDER the list it came from, so `précédent` from a card lands on the
  // list rather than wherever the visitor happened to be before.
  const tab = view === "maires" && routedCard ? "fiche" : view;
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

  // THE CARD THE ADDRESS NAMES. One loader for the three ways in — a click,
  // a link somebody shared, and « précédent » — because a deep link that
  // renders an empty card is a link nobody sends twice.
  //
  // `loading` is a REF and holds the INSEE, not a boolean: React 19 runs
  // effects twice in development, and the two runs must not fire two
  // requests; and `onCardUpdated` replaces `chosen` after a status write,
  // which must not read as a new card to fetch.
  const loading = useRef<string | null>(null);
  useEffect(() => {
    if (!me || !routedCard) {
      loading.current = null;
      setChosen(null);
      return;
    }
    if (loading.current === routedCard) return;
    loading.current = routedCard;
    // CLEARED BEFORE THE FETCH, and this line is the whole point of the
    // ordering. Between two cards — which is what « précédent » and
    // « suivant » do, and what a shared link clicked from a card does —
    // `chosen` still held the PREVIOUS one while the next was in flight:
    // the screen showed A's commune under B's address, and every control on
    // it, « Enregistrer » included, was wired to A's INSEE. A status written
    // in that window lands on the wrong mayor, in a base the whole campaign
    // reads. Rendering nothing for a moment is the same shape as a cold
    // load, which this screen already accepts.
    setChosen(null);
    let alive = true;
    API.card(routedCard)
      .then((c) => {
        if (alive) setChosen(c);
      })
      .catch((e) => {
        if (!alive) return;
        report(e);
        // A card this account cannot read is not a screen to sit on: the
        // list is where they can act, and REPLACE so « précédent » does not
        // hand the same refusal back.
        loading.current = null;
        navigate(["maires"], { replace: true });
      });
    return () => {
      alive = false;
    };
  }, [me, routedCard, report]);

  // Session expired server-side: back to the form, saying so.
  useEffect(() => {
    const lost = () => {
      setMe(null);
      setChosen(null);
      // REPLACE, not push: the session died where the visitor stood, and
      // « précédent » onto the screen it died on is a dead end they did not
      // choose. Without this the tab stays on « fiche », whose card has just
      // been unmounted, and signing back in lands on an empty screen.
      navigate([], { replace: true });
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
      navigate([], { replace: true });
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
          : tab === "equipe"
            ? gestionLabel(me)
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
  // Opening a card is now going to its address; the effect above is what
  // fetches it. So a card reached by a click, by a shared link, and by
  // « précédent » all take the same path — three ways in and one loader,
  // rather than a click path that works and a deep link that renders empty.
  const openCard = (insee: string) => navigate(["maires", insee]);

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
                Ouvrir « {gestionLabel(me)} »
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
          // the volunteer's own answer wins; null means they never gave one,
          // and the campaign's default then applies AS IT CHANGES
          phoneOutreach={account.phone_outreach ?? cfg.phone_outreach ?? false}
          // the campaign's texts, then this volunteer's team's, over the
          // image's — resolved by the engine, in that order
          templates={[me.templates?.campaign, me.templates?.team]}
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
              // what this screen was showing: the server refuses the write
              // if the stored status has moved since, so a tab left open
              // cannot erase what somebody recorded in between. A card with
              // no status displays « à contacter », and that is what was
              // read.
              chosen.mayor.status ?? "to_contact",
            );
            setChosen(fresh);
          }}
        />
      )}

      {tab === "equipe" && (
        <GestionEquipe
          onError={report}
          me={me}
          onMe={setMe}
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
          onMessage={setMessage}
          onSaved={(personalNote: string, phoneOutreach: boolean | null) => {
            setMe((m) =>
              m
                ? {
                    ...m,
                    account: {
                      ...m.account,
                      personal_note: personalNote,
                      phone_outreach: phoneOutreach,
                    },
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
    ...(me?.may_manage
      ? [["equipe", gestionLabel(me)] as [string, string]]
      : []),
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
        <Marque
          logo={cfg.logo}
          // the CAMPAIGN's name, not its candidate's: what an administrator
          // moderated, what the annuaire lists, and what a campaign whose
          // candidate is not yet decided still has
          sous={cfg.organisation?.name}
          onHome={me && setTab ? () => setTab("guide") : undefined}
        />
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
      <PiedDePage
        sourceUrl={cfg.source_url}
        browserUrl={cfg.browser_version_url}
      >
        <p>
          Le travail est enregistré sur le serveur de la campagne. Vos notes et
          vos réservations restent dans votre équipe. Les statuts, eux, sont lus
          par toute la campagne — c'est ce qui évite que deux équipes appellent
          le même élu — avec le nom de l'équipe qui les a enregistrés, jamais
          celui de la personne.
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
  // The account-less way in. Checked here rather than rendered blind: this
  // is a setting an operator can leave empty, and an <a> with no href is a
  // link that looks like one and does nothing.
  const sansCompte = httpUrl(cfg.browser_version_url);
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
      {/* The other door, for whoever has no account and wants none. It sits
          ABOVE the team-request disclosure on purpose: unfolded, that form
          is nine fields long and would push this card off the screen.

          A plain <a>, not a button: /navigateur/ is a second build of this
          application, outside the single page — a real load, and « précédent »
          from there comes back here. The campaign travels in a QUERY; the
          fragment belongs to the sign-in link. */}
      {sansCompte && (
        <section className="carte">
          <h2>Sans compte, dans votre navigateur</h2>
          {/* The pre-fill is promised only where it will HAPPEN. A campaign
              still at its template values pre-fills nothing — the API
              refuses it with a 409, deliberately, rather than spread
              « Prénom NOM » to volunteers with no way of knowing — and the
              same /api/config that carries this link carries `unfilled`.
              Promised unconditionally, the sentence was a promise its reader
              discovered by paying for it. */}
          {cfg.unfilled?.length > 0 ? (
            <p>
              Cette campagne n'a pas encore rempli ses textes : la version
              navigateur s'ouvrira avec des valeurs d'exemple, à vous de les
              renseigner. <strong>N'envoyez rien</strong> avant.
            </p>
          ) : (
            <p>
              Les textes de cette campagne — candidat, contacts, signature — y
              sont déjà remplis, repris de cette page. Tout reste sur votre
              poste.
            </p>
          )}
          <p>
            En échange, <strong>rien n'est coordonné</strong> : cette version
            ignore qu'un autre bénévole a déjà appelé le même maire. Pour
            travailler à plusieurs, demandez un accès ci-dessous.
          </p>
          <p>
            <a className="bouton" href={sansCompte}>
              Ouvrir la version navigateur
            </a>
          </p>
        </section>
      )}
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

/**
 * The team that wrote the status shown, when it is not yours — the one
 * attribution a status carries from one team to another. Null when there is
 * nothing to say: your own team, or a card statused before the column existed.
 *
 * The account says `team_id: null` for « no team » where the card says `"0"`,
 * and the API's `MyTeam()` normalises the first into the second. Comparing
 * them without doing the same makes the national scope foreign to itself:
 * every coordinator then reads « enregistré par l'équipe nationale » on their
 * own writes.
 */
export function equipeAyantEcrit(mayor: MayorCard, me: Account): string | null {
  // text, so the national scope is `"0"` — truthy, where the number 0 is the
  // falsy value that would read « nobody wrote this » on every write of the
  // accounts that carry no team
  const ecrite = mayor.updated_by_team;
  if (!ecrite) return null;
  if (ecrite === String(me.team_id ?? 0)) return null;
  if (ecrite === "0") return "l'équipe nationale";
  // A team is never deleted, so the name is there; if it ever is not, saying
  // « nationale » would name a different team than the one that wrote.
  return mayor.updated_by_team_name
    ? `l'équipe ${mayor.updated_by_team_name}`
    : "une autre équipe";
}

/**
 * Who has this card, as TEXT — never as a truthy test. `"0"` is the national
 * scope, a real scope held by every account with no team and having no row in
 * `teams`, hence no name: read through `team_name` alone, a card the
 * coordination had taken showed as « personne n'est encore dessus », and the
 * volunteer who then tried to work it was the second caller. Same shape, same
 * remedy as `equipeAyantEcrit` one function down.
 */
export function equipeQuiTravaille(mayor: MayorCard): string | null {
  if (!mayor.taken_by) return null;
  if (mayor.taken_by === "0") return "l'équipe nationale";
  return mayor.team_name ? `l'équipe ${mayor.team_name}` : "une autre équipe";
}

// What the card says about who is on it. INFORMATIVE, every way: no card of a
// campaign is refused to a team of it and no write on one is refused either,
// so none of these sentences announces a refusal. They say what a volunteer
// needs in order to DECIDE, which is a different job from a wall — and a
// sentence that promised more than the server allowed was worse than the wall
// it replaced, because it was discovered by paying for it.
function EtatDeLaFiche({ mayor, me }: { mayor: MayorCard; me: Account }) {
  if (mayor.volunteer === me.email) {
    return <p className="gris">Cette fiche est la vôtre.</p>;
  }
  // A person is named only where the person is this account's to see:
  // elsewhere the server sends the team and no name.
  if (mayor.volunteer) {
    const ou = equipeQuiTravaille(mayor);
    return (
      <p className="gris">
        Prise par <strong>{mayor.volunteer_name ?? mayor.volunteer}</strong>
        {ou ? ` (${ou})` : ""}. Vous pouvez la consulter et enregistrer ce que
        vous apprenez ; accordez-vous avec {mayor.volunteer_name ?? "cette"}
        {mayor.volunteer_name ? "" : " personne"} pour ne pas appeler deux fois.
      </p>
    );
  }
  const ou = equipeQuiTravaille(mayor);
  if (ou) {
    return (
      <p className="gris">
        Travaillée par <strong>{ou}</strong>. Rien ne vous est interdit ici :
        vous pouvez la consulter et enregistrer ce que vous apprenez.
        Accordez-vous avec eux pour ne pas contacter le maire deux fois.
      </p>
    );
  }
  return (
    <p className="gris">
      Personne n'est encore dessus. Enregistrer un statut la note pour toute la
      campagne ; pour recevoir des fiches à traiter, prenez un lot depuis « Mon
      tableau ».
    </p>
  );
}

function ReserveePar({ mayor, me }: { mayor: MayorCard; me: Account }) {
  const autre = equipeAyantEcrit(mayor, me);
  return (
    <>
      <EtatDeLaFiche mayor={mayor} me={me} />
      {autre && <p className="gris">Dernier statut enregistré par {autre}.</p>}
    </>
  );
}
