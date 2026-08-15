import {
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import * as API from "./api.ts";
import {
  Alerte,
  CAMPAIGN_FIELDS,
  type CardDraft,
  Chip,
  CompteurResultats,
  campaignLabel,
  Emoji,
  Fiche,
  Guide,
  Hexagone,
  LigneMaire,
  label,
  NavOnglets,
  PiedDePage,
  RenderGuard,
  ROLES,
  SkipLink,
  STATUSES,
  TableMaires,
  useViewFocus,
} from "./common.tsx";
import * as M from "./messages.ts";
import type {
  Account,
  Dashboard as DashboardData,
  Facets,
  MayorCard,
  Me,
  Message,
  ServerConfig,
  TeamData,
} from "./types.ts";

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

  const report = useCallback((e: unknown) => {
    setMessage({
      tone: "erreur",
      text: e instanceof Error ? e.message : String(e),
    });
  }, []);

  useEffect(() => {
    (async () => {
      try {
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
        if (!(e instanceof API.APIError) || e.code !== 401) report(e);
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
    return (
      <main>
        <p role="status">Chargement…</p>
      </main>
    );
  if (!me) {
    return (
      <Coquille cfg={cfg}>
        <Alerte message={message} onClose={() => setMessage(null)} />
        <Connexion
          cfg={cfg}
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
    <Coquille cfg={cfg} me={me} tab={tab} setTab={setTab} onSignOut={signOut}>
      <Alerte message={message} onClose={() => setMessage(null)} />
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
  children: ReactNode;
}

function Coquille({
  cfg,
  me,
  tab,
  setTab,
  onSignOut,
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
        <span className="marque">
          <Hexagone />
          <span>
            paraphe
            <br />
            <span className="sous">{cfg.campaign?.candidat}</span>
          </span>
        </span>
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
      </header>
      <div className="rayures" aria-hidden="true" />
      <RenderGuard>
        <main id="contenu" tabIndex={-1}>
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
}: {
  cfg: ServerConfig;
  onSignedIn: (m: Me) => void;
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
      <h1>Connexion</h1>
      {cfg.no_account && (
        <p className="alerte erreur">
          <strong>Aucun compte n'existe encore.</strong> La coordination doit
          démarrer l'application avec PARAPHE_ADMIN_EMAIL et
          PARAPHE_ADMIN_PASSWORD pour créer le premier accès.
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
        <p className="gris">
          Votre référent vous a communiqué un accès. Le mot de passe n'est
          affiché qu'une fois à sa création : s'il est perdu, il faut en
          regénérer un.
        </p>
      </form>
    </>
  );
}

function ReserveePar({ mayor, me }: { mayor: MayorCard; me: Account }) {
  if (!mayor.volunteer) {
    return (
      <p className="gris">
        Fiche libre — enregistrer un statut vous l'attribue.
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

interface TableauProps {
  cfg: ServerConfig;
  me: Me;
  onError: (e: unknown) => void;
  onOpen: (insee: string) => void;
  onMessage: (m: Message) => void;
}

/**
 * One goal tile: the number is the information, the bar its silhouette
 * against the goal. The bar is decoration — the text beside it already
 * says "12 sur 500" — so assistive technology skips it.
 */
function Tuile({
  label,
  value,
  goal,
}: {
  label: string;
  value: number;
  goal?: number;
}) {
  const pct = goal ? Math.min(100, Math.round((value / goal) * 100)) : null;
  return (
    <div className="tuile">
      <p className="valeur">
        {value.toLocaleString("fr")}
        {goal !== undefined && <span className="objectif"> sur {goal}</span>}
      </p>
      <p className="libelle">{label}</p>
      {pct !== null && (
        <div className="jauge" aria-hidden="true">
          <div className="barre" style={{ width: `${pct}%` }} />
        </div>
      )}
    </div>
  );
}

function Tableau({ cfg, me, onError, onOpen, onMessage }: TableauProps) {
  const [data, setData] = useState<DashboardData | null>(null);
  const [dept, setDept] = useState("");
  const [rank, setRank] = useState("has_endorsed");
  const [democracy, setDemocracy] = useState(false);
  const [sending, setSending] = useState(false);

  const reload = useCallback(async () => {
    try {
      setData(await API.dashboard());
    } catch (e) {
      onError(e);
    }
  }, [onError]);

  useEffect(() => {
    reload();
  }, [reload]);

  if (!data) return <p role="status">Chargement…</p>;

  const take = async () => {
    setSending(true);
    try {
      const r = await API.takeBatch({ department: dept, rank, democracy });
      onMessage(
        r.taken > 0
          ? { tone: "ok", text: `${r.taken} maire(s) vous sont attribués.` }
          : { tone: "erreur", text: r.message },
      );
      await reload();
    } catch (e) {
      onError(e);
    } finally {
      setSending(false);
    }
  };

  return (
    <>
      <h1>Mon tableau de bord</h1>

      <div className="carte">
        <h2 style={{ marginTop: 0 }}>Où en est la campagne</h2>
        <p className="gris">
          Volumes de toute la campagne, sans aucun nom. Il faut 500
          présentations, dans au moins 30 départements, 50 au plus par
          département.
        </p>
        <div className="tuiles">
          <Tuile
            label="Promesses et signatures"
            value={(data.stats.promised ?? 0) + (data.stats.signed ?? 0)}
            goal={500}
          />
          <Tuile
            label="Départements avec une promesse"
            value={data.departments_covered}
            goal={30}
          />
          <Tuile
            label="Mes fiches à traiter"
            value={
              data.mine.filter(
                (m) =>
                  !m.status ||
                  m.status === "to_contact" ||
                  m.status === "to_call_back",
              ).length
            }
          />
        </div>
        <table>
          <tbody>
            {(cfg.statuses ?? []).map((s) => (
              <tr key={s.key}>
                <td>
                  <Chip status={s.key} />
                </td>
                <td>{data.stats[s.key] ?? 0}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="carte">
        <h2 style={{ marginTop: 0 }}>Prendre un lot</h2>
        <p className="gris">
          {data.batch_size} maires vous sont réservés, les mieux notés d'abord.
          Personne d'autre ne les recevra.
        </p>
        <form
          className="enligne"
          onSubmit={(e) => {
            e.preventDefault();
            take();
          }}
        >
          <div>
            <label>
              Département
              <select value={dept} onChange={(e) => setDept(e.target.value)}>
                <option value="">— tout mon périmètre —</option>
                {data.departments.map((d) => (
                  <option key={d} value={d}>
                    {d}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <div>
            <label>
              Vivier
              <select value={rank} onChange={(e) => setRank(e.target.value)}>
                {(cfg.ranks ?? []).map((r) => (
                  <option key={r.key} value={r.key}>
                    {r.label} ({data.by_rank[r.key] ?? 0})
                  </option>
                ))}
              </select>
            </label>
          </div>
          <div>
            <label>
              <input
                type="checkbox"
                checked={democracy}
                onChange={(e) => setDemocracy(e.target.checked)}
              />{" "}
              A parrainé une candidature sur le fonctionnement démocratique
            </label>
          </div>
          <button type="submit" disabled={sending}>
            {sending ? "Attribution…" : "Prendre un lot"}
          </button>
        </form>
      </div>

      <h2>Mes maires ({data.mine.length})</h2>
      {data.mine.length === 0 ? (
        <p className="gris">Aucun pour l'instant — prenez un lot ci-dessus.</p>
      ) : (
        <div className="carte">
          <TableMaires>
            {data.mine.map((m) => (
              <LigneCarte key={m.insee_code} m={m} onOpen={onOpen} />
            ))}
          </TableMaires>
        </div>
      )}

      {data.team.length > 0 && (
        <>
          <h2>Mon équipe</h2>
          <div className="carte">
            <table>
              <thead>
                <tr>
                  <th scope="col">Bénévole</th>
                  <th scope="col">Fiches</th>
                  <th scope="col">Contactées</th>
                </tr>
              </thead>
              <tbody>
                {data.team.map((e) => (
                  <tr key={e.who}>
                    <td>
                      {e.who}
                      {e.who === me.account.name ? " (vous)" : ""}
                    </td>
                    <td>{e.n}</td>
                    <td>{e.done}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}

      {data.departments_with_promise.length > 0 && (
        <>
          <h2>Départements acquis</h2>
          <div className="carte">
            <p>
              {data.departments_with_promise.map((d) => (
                <span
                  key={d.key}
                  className="chip chip-vert"
                  style={{ marginRight: ".3rem" }}
                >
                  {d.key} : {d.n}
                </span>
              ))}
            </p>
          </div>
        </>
      )}

      <p>
        <a className="bouton secondaire" href={API.exportUrl()}>
          <Emoji>⬇ </Emoji>Exporter le suivi de mon équipe (CSV)
        </a>
      </p>
    </>
  );
}

/** The shared row, fed with this mode's status and reservation columns. */
function LigneCarte({
  m,
  onOpen,
}: {
  m: MayorCard;
  onOpen: (insee: string) => void;
}) {
  return (
    <LigneMaire
      m={m}
      status={m.status}
      volunteer={m.volunteer_name}
      onOpen={() => onOpen(m.insee_code)}
    />
  );
}

function ListeServeur({
  onError,
  onOpen,
}: {
  onError: (e: unknown) => void;
  onOpen: (i: string) => void;
}) {
  const [q, setQ] = useState("");
  const [rank, setRank] = useState("has_endorsed");
  const [status, setStatus] = useState("");
  const [dept, setDept] = useState("");
  const [democracy, setDemocracy] = useState(false);
  const [rows, setRows] = useState<MayorCard[]>([]);
  const [total, setTotal] = useState(0);
  const [next, setNext] = useState<string | null>(null);
  const [failed, setFailed] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [facets, setFacets] = useState<Facets>({
    departments: [],
    by_rank: {},
  });

  useEffect(() => {
    API.facets().then(setFacets).catch(onError);
  }, [onError]);

  // the filter changes: back to zero. A request counter keeps a slow
  // answer to the old filter from crushing the new list.
  const latest = useRef(0);
  const criteria = { q, rank, status, department: dept, democracy };
  const key = JSON.stringify(criteria);

  useEffect(() => {
    const mine = ++latest.current;
    setLoading(true);
    const timer = setTimeout(async () => {
      try {
        const r = await API.mayors(JSON.parse(key));
        if (mine !== latest.current) return;
        setRows(r.rows);
        setTotal(r.total);
        setNext(r.next);
        setFailed(null);
      } catch (e) {
        if (mine === latest.current) onError(e);
      } finally {
        if (mine === latest.current) setLoading(false);
      }
    }, 250); // time to finish typing
    return () => clearTimeout(timer);
  }, [key, onError]);

  const sentinel = useRef(null);
  const loadNext = useCallback(async () => {
    if (next === null || loading) return;
    const mine = latest.current;
    setLoading(true);
    try {
      const r = await API.mayors({ ...JSON.parse(key), after: next });
      if (mine !== latest.current) return;
      // Deduplicate: OFFSET pagination runs over data the team modifies at
      // the same time, and a card entering or leaving the filter between
      // two pages made a mayor appear twice — or skip, which, in a
      // campaign whose object is coverage, means a mayor never contacted.
      setRows((old) => {
        const seen = new Set(old.map((m) => m.insee_code));
        return [...old, ...r.rows.filter((m) => !seen.has(m.insee_code))];
      });
      setNext(r.next);
    } catch (e) {
      if (mine === latest.current) {
        onError(e);
        // STOP. Without this the cursor stays put, the sentinel is still
        // on screen, and the effect fires again the moment loading
        // clears: a passing API failure became sixty requests a second,
        // which is what keeps the API from recovering.
        setNext(null);
        setFailed(mine === latest.current ? key : null);
      }
    } finally {
      // guarded like the main effect: a stale answer must not reopen the
      // door while another request is in flight
      if (mine === latest.current) setLoading(false);
    }
  }, [next, loading, key, onError]);

  // infinite scroll: the next page loads when the bottom of the list
  // enters the viewport, without a click
  useEffect(() => {
    const target = sentinel.current;
    if (!target || next === null) return undefined;
    const obs = new IntersectionObserver(
      (entries) => {
        if (entries[0].isIntersecting) loadNext();
      },
      { rootMargin: "300px" },
    );
    obs.observe(target);
    return () => obs.disconnect();
  }, [next, loadNext]);

  return (
    <>
      <h1>Les maires</h1>
      <form className="enligne carte" onSubmit={(e) => e.preventDefault()}>
        <div>
          <label>
            Recherche (commune, nom)
            <input
              type="text"
              value={q}
              onChange={(e) => setQ(e.target.value)}
            />
          </label>
        </div>
        <div>
          <label>
            Vivier
            <select value={rank} onChange={(e) => setRank(e.target.value)}>
              {Object.entries(M.RANKS).map(([k, v]) => (
                <option key={k} value={k}>
                  {v} ({facets.by_rank[k] ?? 0})
                </option>
              ))}
            </select>
          </label>
        </div>
        <div>
          <label>
            Statut
            <select value={status} onChange={(e) => setStatus(e.target.value)}>
              <option value="">— tous —</option>
              {Object.entries(STATUSES).map(([k, [l]]) => (
                <option key={k} value={k}>
                  {l}
                </option>
              ))}
            </select>
          </label>
        </div>
        <div>
          <label>
            Département
            <select value={dept} onChange={(e) => setDept(e.target.value)}>
              <option value="">— tous —</option>
              {facets.departments.map((d) => (
                <option key={d} value={d}>
                  {d}
                </option>
              ))}
            </select>
          </label>
        </div>
        <div>
          <label>
            <input
              type="checkbox"
              checked={democracy}
              onChange={(e) => setDemocracy(e.target.checked)}
            />{" "}
            Thème démocratique
          </label>
        </div>
      </form>

      {rank !== "has_endorsed" && (
        <p className="alerte">
          Vous êtes sorti du vivier prioritaire. Le message généré s'adapte au
          rang : on n'y remercie personne d'un parrainage qui n'a pas eu lieu.
        </p>
      )}

      <CompteurResultats shown={rows.length} total={total} />
      <div className="carte">
        <TableMaires>
          {rows.map((m) => (
            <LigneCarte key={m.insee_code} m={m} onOpen={onOpen} />
          ))}
        </TableMaires>
        <div ref={sentinel} />
        {loading && (
          <p className="gris" role="status" style={{ textAlign: "center" }}>
            Chargement…
          </p>
        )}
        {failed !== null && (
          <p className="alerte" role="alert" style={{ textAlign: "center" }}>
            Chargement interrompu.{" "}
            <button
              type="button"
              className="lien"
              onClick={() => {
                setFailed(null);
                API.mayors(JSON.parse(key))
                  .then((r) => {
                    setRows(r.rows);
                    setTotal(r.total);
                    setNext(r.next);
                  })
                  .catch(onError);
              }}
            >
              Réessayer
            </button>
          </p>
        )}
        {failed === null && next === null && rows.length > 0 && (
          <p className="gris" style={{ textAlign: "center" }}>
            — fin de la liste —
          </p>
        )}
        {rows.length === 0 && !loading && (
          <p className="gris">
            Aucun maire avec ces critères.{" "}
            <button
              type="button"
              className="lien"
              onClick={() => {
                setQ("");
                setRank("has_endorsed");
                setStatus("");
                setDept("");
                setDemocracy(false);
              }}
            >
              Réinitialiser les filtres
            </button>
          </p>
        )}
      </div>
    </>
  );
}

// Labels of the campaign keys. A campaign opened from a hosting request
// arrives with not a word filled in: this is where its coordination fills
// it — not in environment variables it has no hand on.
/** The banner names the missing keys: a volunteer reads a label, not a column. */

function ConfigurationCampagne({
  cfg,
  onCfg,
  onError,
  onMessage,
}: {
  cfg: ServerConfig;
  onCfg: (c: ServerConfig) => void;
  onError: (e: unknown) => void;
  onMessage: (m: Message) => void;
}) {
  const [values, setValues] = useState<Record<string, string>>(cfg.campaign);
  const [batchSize, setBatchSize] = useState(String(cfg.batch_size));
  const [sending, setSending] = useState(false);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setSending(true);
    try {
      const r = await API.updateCampaign(values, Number(batchSize));
      onCfg({
        ...cfg,
        campaign: r.campaign,
        batch_size: r.batch_size,
        unfilled: r.unfilled,
      });
      onMessage({
        tone: "ok",
        text:
          r.unfilled.length === 0
            ? "Campagne enregistrée. Les messages sont prêts à partir."
            : `Campagne enregistrée. Restent à remplir : ${r.unfilled.join(", ")}.`,
      });
    } catch (err) {
      onError(err);
    } finally {
      setSending(false);
    }
  };

  return (
    <form className="carte" onSubmit={save}>
      <h2 style={{ marginTop: 0 }}>La campagne</h2>
      <p className="gris">
        Ces valeurs remplissent les messages envoyés aux maires. Tant qu'il en
        manque, l'application le dit sur chaque page et le publipostage de masse
        refuse de tourner.
      </p>
      {CAMPAIGN_FIELDS.map((f, i) => (
        <div key={f.key}>
          {f.group !== CAMPAIGN_FIELDS[i - 1]?.group && (
            <h3 className="groupe">{f.group}</h3>
          )}
          <p>
            {/* associated by id, not nested: a textarea nested in its label
                makes its own CONTENT part of the label's text */}
            <label htmlFor={`champ-${f.key}`}>{f.label}</label>
            {f.long ? (
              <textarea
                id={`champ-${f.key}`}
                rows={3}
                placeholder={f.example}
                aria-describedby={f.hint ? `champ-${f.key}-aide` : undefined}
                value={values[f.key] ?? ""}
                onChange={(e) =>
                  setValues({ ...values, [f.key]: e.target.value })
                }
              />
            ) : (
              <input
                id={`champ-${f.key}`}
                type="text"
                placeholder={f.example}
                aria-describedby={f.hint ? `champ-${f.key}-aide` : undefined}
                value={values[f.key] ?? ""}
                onChange={(e) =>
                  setValues({ ...values, [f.key]: e.target.value })
                }
              />
            )}
            {f.hint && (
              <span className="gris aide" id={`champ-${f.key}-aide`}>
                {f.hint}
              </span>
            )}
          </p>
        </div>
      ))}
      <p>
        <label>
          Maires attribués par « prendre un lot »
          <input
            type="number"
            min={1}
            max={100}
            value={batchSize}
            onChange={(e) => setBatchSize(e.target.value)}
          />
        </label>
      </p>
      <button type="submit" disabled={sending}>
        {sending ? "Enregistrement…" : "Enregistrer la campagne"}
      </button>
    </form>
  );
}

function GestionEquipe({
  onError,
  me,
  cfg,
  onCfg,
  onMessage,
}: {
  onError: (e: unknown) => void;
  me: Me;
  cfg: ServerConfig;
  onCfg: (c: ServerConfig) => void;
  onMessage: (m: Message) => void;
}) {
  const [data, setData] = useState<TeamData | null>(null);
  const [created, setCreated] = useState<{
    email: string;
    name: string;
    role: string;
    password: string;
  } | null>(null);
  const [draft, setDraft] = useState({
    email: "",
    name: "",
    role: "volunteer",
    team_id: "",
  });
  const [team, setTeam] = useState<{ name: string; departments: string[] }>({
    name: "",
    departments: [],
  });

  const reload = useCallback(async () => {
    try {
      setData(await API.team());
    } catch (e) {
      onError(e);
    }
  }, [onError]);
  useEffect(() => {
    reload();
  }, [reload]);

  if (!data) return <p role="status">Chargement…</p>;
  const coordination = me.account.role === "coordination";

  const createAccount = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      const r = await API.createAccount({
        email: draft.email,
        name: draft.name,
        role: coordination ? draft.role : undefined,
        team_id:
          coordination && draft.team_id ? Number(draft.team_id) : undefined,
      });
      setCreated(r);
      setDraft({ email: "", name: "", role: "volunteer", team_id: "" });
      await reload();
    } catch (err) {
      onError(err);
    }
  };

  return (
    <>
      <h1>Mon équipe</h1>

      {coordination && (
        <ConfigurationCampagne
          cfg={cfg}
          onCfg={onCfg}
          onError={onError}
          onMessage={onMessage}
        />
      )}

      {/* The announcement is a PERSISTENT text-only region beside the
          card, never the card itself: a live region is reliable only when
          its content changes inside an existing node, and it must hold no
          interactive control — status implies aria-atomic, so any rerender
          would re-read the whole card, password included. */}
      <span role="status" className="sr-only">
        {created
          ? `Un accès vient d'être créé pour ${created.name}. Le mot de ` +
            "passe provisoire est affiché à l'écran, à transmettre de vive voix."
          : ""}
      </span>
      {created && (
        <div className="carte alerte">
          <p>
            <strong>
              Accès créé pour {created.name} ({created.email}).
            </strong>
          </p>
          <p>
            Mot de passe provisoire — <strong>affiché une seule fois</strong>, à
            transmettre de vive voix :
          </p>
          <p className="grand-tel">{created.password}</p>
          <button
            type="button"
            className="lien"
            onClick={() => setCreated(null)}
          >
            j'ai noté
          </button>
        </div>
      )}

      <div className="carte">
        <h2 style={{ marginTop: 0 }}>Ouvrir un accès</h2>
        <form className="enligne" onSubmit={createAccount}>
          <div>
            <label>
              Nom
              <input
                type="text"
                required
                value={draft.name}
                onChange={(e) => setDraft({ ...draft, name: e.target.value })}
              />
            </label>
          </div>
          <div>
            <label>
              Adresse email
              <input
                type="text"
                required
                value={draft.email}
                onChange={(e) => setDraft({ ...draft, email: e.target.value })}
              />
            </label>
          </div>
          {coordination && (
            <>
              <div>
                <label>
                  Rôle
                  <select
                    value={draft.role}
                    onChange={(e) =>
                      setDraft({ ...draft, role: e.target.value })
                    }
                  >
                    <option value="volunteer">Bénévole</option>
                    <option value="lead">Référent</option>
                    <option value="coordination">Coordination</option>
                  </select>
                </label>
              </div>
              <div>
                <label>
                  Équipe
                  <select
                    value={draft.team_id}
                    onChange={(e) =>
                      setDraft({ ...draft, team_id: e.target.value })
                    }
                  >
                    <option value="">— nationale —</option>
                    {data.teams.map((g) => (
                      <option key={g.id} value={g.id}>
                        {g.name}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
            </>
          )}
          <button type="submit">Créer</button>
        </form>
        {!coordination && (
          <p className="gris">
            En tant que référent, vous ouvrez des accès bénévoles dans votre
            équipe.
          </p>
        )}
      </div>

      <h2>Les accès ({data.accounts.length})</h2>
      <div className="carte">
        <table>
          <thead>
            <tr>
              <th scope="col">Nom</th>
              <th scope="col">Adresse</th>
              <th scope="col">Rôle</th>
              <th scope="col">Équipe</th>
              <th scope="col">
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {data.accounts.map((c) => (
              // muted, not faded: opacity .5 halved every contrast in the
              // row, réactiver button included — and made the state a
              // colour-only signal. The word carries it now.
              <tr key={c.email} className={c.active ? undefined : "inactif"}>
                <td>
                  {c.name}
                  {!c.active && <span className="gris"> (désactivé)</span>}
                </td>
                <td className="gris">{c.email}</td>
                <td>{label(ROLES, c.role)}</td>
                <td>{c.team ?? "—"}</td>
                <td>
                  {c.email !== me.account.email && (
                    <button
                      type="button"
                      className="lien"
                      onClick={async () => {
                        try {
                          await API.toggleAccount(c.email);
                          await reload();
                        } catch (e) {
                          onError(e);
                        }
                      }}
                    >
                      {c.active ? "désactiver" : "réactiver"}
                      {/* nine identical buttons in a column: the name says
                          whose access this one touches */}
                      <span className="sr-only"> {c.name}</span>
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {coordination && (
        <>
          <h2>Les équipes locales</h2>
          <div className="carte">
            <form
              className="enligne"
              onSubmit={async (e) => {
                e.preventDefault();
                try {
                  await API.createTeam(team.name, team.departments);
                  setTeam({ name: "", departments: [] });
                  await reload();
                } catch (err) {
                  onError(err);
                }
              }}
            >
              <div>
                <label>
                  Nom de l'équipe
                  <input
                    type="text"
                    required
                    value={team.name}
                    onChange={(e) => setTeam({ ...team, name: e.target.value })}
                  />
                </label>
              </div>
              <div>
                <label>
                  Départements (plusieurs possibles)
                  <select
                    multiple
                    size={5}
                    value={team.departments}
                    onChange={(e) =>
                      setTeam({
                        ...team,
                        departments: [...e.target.selectedOptions].map(
                          (o) => o.value,
                        ),
                      })
                    }
                  >
                    {data.departments.map((d) => (
                      <option key={d} value={d}>
                        {d}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
              <button type="submit">Créer l'équipe</button>
            </form>
            <p className="gris">
              Une équipe ne pioche que dans ses départements et ne voit que son
              propre travail. Sans départements, elle travaille partout.
            </p>
            <table>
              <thead>
                <tr>
                  <th scope="col">Équipe</th>
                  <th scope="col">Départements</th>
                  <th scope="col">Membres</th>
                  <th scope="col">Fiches</th>
                </tr>
              </thead>
              <tbody>
                {data.teams.map((g) => (
                  <tr key={g.id}>
                    <td>{g.name}</td>
                    <td className="gris">
                      {g.departments || "toute la France"}
                    </td>
                    <td>{g.members}</td>
                    <td>{g.reserved}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </>
  );
}

interface ProfilProps {
  me: Me;
  cfg: ServerConfig;
  onError: (e: unknown) => void;
  onSaved: (personalNote: string) => void;
}

function Profil({ me, cfg, onError, onSaved }: ProfilProps) {
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
