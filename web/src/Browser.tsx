import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alerte,
  CAMPAIGN_FIELDS,
  type CardDraft,
  Chip,
  CompteurResultats,
  campaignLabel,
  EMPTY_CFG,
  Emoji,
  Fiche,
  Guide,
  Hexagone,
  NavOnglets,
  PiedDePage,
  RenderGuard,
  SkipLink,
  useViewFocus,
} from "./common.tsx";
import {
  formatBytes,
  LISTS,
  type ListKey,
  loadList,
  type Progress,
} from "./data.ts";
import * as DB from "./db.ts";
import { DEMO_SET } from "./demo.ts";
import * as M from "./messages.ts";
import {
  fetchCampaign,
  instanceDomain,
  type Offer,
  requestedSlug,
  untouchedCampaign,
} from "./prefill.ts";
import type { Campaign, Mayor, Message, Tracking } from "./types.ts";

// Browser mode: no data leaves the computer. The only network request is
// downloading the list published next to the application.

const VIEW_TITLES: Record<string, string> = {
  liste: "Les maires",
  guide: "Guide",
  donnees: "Mes données",
  campagne: "Ma campagne",
};

export default function Browser() {
  const [mayors, setMayors] = useState<Mayor[]>([]);
  const [tracking, setTracking] = useState<Record<string, Tracking>>({});
  const [cfg, setCfg] = useState<Campaign>(EMPTY_CFG);
  const [personalNote, setPersonalNote] = useState("");
  const [tab, setTab] = useState("liste");
  const [chosen, setChosen] = useState<Mayor | null>(null);
  const [q, setQ] = useState("");
  const [rankFilter, setRankFilter] = useState("has_endorsed");
  const [deptFilter, setDeptFilter] = useState("");
  const [message, setMessage] = useState<Message | null>(null);
  const [ready, setReady] = useState(false);
  const [download, setDownload] = useState<DownloadState | null>(null);
  const [loadedList, setLoadedList] = useState<
    ListKey | "personnel" | "demo" | null
  >(null);
  // ?org=<slug>: an OFFER, never applied on its own. Adopting a campaign
  // decides what thousands of mayors will read, so it is a decision the
  // volunteer takes with the candidate's name in front of them.
  const [offer, setOffer] = useState<Offer | null>(null);
  const [offerError, setOfferError] = useState<string | null>(null);
  // The « Ma campagne » draft lives HERE, not in the tab: the tab is
  // unmounted by a tab switch, and an unsaved draft is the volunteer's
  // work — it must survive a look at the Guide. No effect resyncs it from
  // `cfg` either: an effect cannot tell an adoption from a save landing,
  // and it reverts keystrokes typed while a save is in flight. The
  // writers below (accept, import, erase) reset it explicitly instead.
  const [draft, setDraft] = useState<Campaign>(EMPTY_CFG);
  const [noteDraft, setNoteDraft] = useState("");
  // unsent card work (rewritten email, call note), keyed by INSEE: the
  // card is unmounted by any tab click and must not lose it
  const cardDrafts = useRef<Record<string, CardDraft>>({});

  // safety: React strict mode mounts twice in development, and a double
  // a 2 MB download on a slow connection is paid by the user
  const inFlight = useRef<ListKey | null>(null);

  const fetchList = useCallback(async (key: ListKey) => {
    if (inFlight.current) return;
    inFlight.current = key;
    setDownload({ key, received: 0, total: 0 });
    try {
      const rows = await loadList(key, (p: Progress) =>
        setDownload({ key, ...p }),
      );
      await DB.replaceMayors(rows);
      await DB.writeSetting("liste", key);
      setMayors(await DB.loadMayors());
      setLoadedList(key);
      setMessage({
        tone: "ok",
        text:
          `${rows.length.toLocaleString("fr")} maires chargés — ` +
          `${LISTS[key].name}. Tout reste dans ce navigateur.`,
      });
    } catch (e) {
      setMessage({
        tone: "erreur",
        text: e instanceof Error ? e.message : String(e),
      });
    } finally {
      inFlight.current = null;
      setDownload(null);
    }
  }, []);

  useEffect(() => {
    (async () => {
      const [m, s, c, a, l] = await Promise.all([
        DB.loadMayors(),
        DB.loadTracking(),
        DB.readSetting<Campaign>("campagne", EMPTY_CFG),
        DB.readSetting<string>("argument", ""),
        DB.readSetting<ListKey | "personnel" | "demo" | null>("liste", null),
      ]);
      setMayors(m);
      setTracking(s);
      setCfg(c);
      setPersonalNote(a);
      setDraft(c);
      setNoteDraft(a);
      setLoadedList(l);
      setReady(true);
      // first launch: the priority list loads by default — it is light and
      // covers the bulk of the work. The full base waits to be asked for.
      if (m.length === 0) fetchList("light");

      const slug = requestedSlug(window.location.search);
      // Offered ONLY on a campaign nobody has touched. "Not complete" was
      // the wrong test: a volunteer who had filled eight fields of nine —
      // their own name under « Qui signe les emails », their own phone —
      // was offered a link that replaced all nine, and `signataire` is the
      // signature at the bottom of every email to a mayor.
      if (slug && untouchedCampaign(c)) {
        try {
          setOffer(await fetchCampaign(slug));
        } catch (err) {
          // its own slot: the list download resolves a second later and
          // overwrote this one, so a broken link failed in total silence
          setOfferError(err instanceof Error ? err.message : String(err));
        }
      }
    })().catch((e) => {
      // Rejecting instead of hanging changes nothing on screen without
      // this: `ready` stays false, the page renders "Chargement…" for
      // good, and the export that would rescue the work is behind it.
      // Reached by a stale tab blocking the upgrade, and by a browser
      // that refuses storage altogether (private window).
      setMessage({
        tone: "erreur",
        text: e instanceof Error ? e.message : String(e),
      });
      setReady(true);
    });
  }, [fetchList]);

  const unfilled = useMemo(() => M.unfilledKeys(cfg), [cfg]);

  // Derived, never stored: whether the draft differs from what is saved.
  // Without it, a keystroke landing during a save stayed on screen under a
  // green « Campagne enregistrée » banner while the base held the older
  // value — indistinguishable from saved work until the next reload.
  const dirty =
    JSON.stringify(draft) !== JSON.stringify(cfg) || noteDraft !== personalNote;

  // nine fields filled and the tab closed without « Enregistrer » is a
  // silent total loss — and a rewritten card email or a half-typed call
  // note is dearer still. The browser's own dialog is the only word we
  // get. The card store is a ref: read at event time, not render time.
  // `cfg` and `personalNote` are what `dirty` is computed from, so they
  // re-register the listener at the same moments `dirty` already does.
  // Redundant, and kept: this listener is the only warning before work is
  // lost, and re-registering it too often is the safe direction.
  // biome-ignore lint/correctness/useExhaustiveDependencies: redundant on purpose
  useEffect(() => {
    const warn = (e: BeforeUnloadEvent) => {
      // Every touched entry, whatever campaign it is taken under. A
      // campaign change does NOT condemn a rewrite: `ville_envoi` and the
      // long presentation appear in no email at all, so filtering on the
      // campaign let a still-intact rewrite be closed without a word. The
      // parent cannot re-render another card to know; over-warning is the
      // only acceptable bias.
      const worth = Object.values(cardDrafts.current).some(
        (d) => d.touched || d.note !== "",
      );
      if (dirty || worth) e.preventDefault();
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty, cfg, personalNote]);

  const filtered = useMemo(() => {
    const t = q.trim().toLowerCase();
    return mayors.filter((m) => {
      if (rankFilter !== "all" && M.rank(m) !== rankFilter) return false;
      if (deptFilter && m.department !== deptFilter) return false;
      if (!t) return true;
      return `${m.commune} ${m.last_name} ${m.first_name} ${m.department}`
        .toLowerCase()
        .includes(t);
    });
  }, [mayors, q, rankFilter, deptFilter]);

  // the departments actually present in the loaded list: offering all 101
  // while working on a priority list that covers 90 would have people
  // searching for mayors who are not there
  const departments = useMemo(() => {
    const seen = new Set<string>();
    for (const m of mayors) if (m.department) seen.add(m.department);
    return [...seen].sort((a, b) => a.localeCompare(b, "fr"));
  }, [mayors]);

  // A filter that survives a list change becomes invisible: the select no
  // longer has the matching option and shows "— tous —" while the state
  // still filters on it, giving "0 affiché(s) sur 0" without an
  // explanation.
  useEffect(() => {
    if (deptFilter && !departments.includes(deptFilter)) setDeptFilter("");
  }, [departments, deptFilter]);

  const counts = useMemo(() => {
    const c: Record<string, number> = { total: mayors.length };
    for (const m of mayors) {
      const r = M.rank(m);
      c[r] = (c[r] ?? 0) + 1;
    }
    for (const s of Object.values(tracking)) {
      c[s.status] = (c[s.status] ?? 0) + 1;
    }
    return c;
  }, [mayors, tracking]);

  const loadCsv = useCallback(async (file: File) => {
    try {
      // parseCsv checks the columns; here only the emptiness is left
      const rows = DB.parseCsv(await file.text());
      if (!rows.length) throw new Error("fichier vide");
      const stored = await DB.replaceMayors(rows);
      // Announced, and recorded: set by fetchList alone, the
      // application went on claiming the priority list while showing a
      // personal file — and offered to replace it with "all 34 826", which
      // would have destroyed the file the volunteer deliberately loaded.
      await DB.writeSetting("liste", "personnel");
      setLoadedList("personnel");
      setMayors(await DB.loadMayors());
      setMessage({
        tone: "ok",
        text: `${stored} maires chargés depuis votre disque.`,
      });
    } catch (e) {
      setMessage({
        tone: "erreur",
        text: `Chargement impossible : ${(e as Error).message}`,
      });
    }
  }, []);

  const loadDemo = useCallback(async () => {
    await DB.replaceMayors(DEMO_SET);
    await DB.writeSetting("liste", "demo");
    setLoadedList("demo");
    setMayors(await DB.loadMayors());
    setMessage({
      tone: "ok",
      text:
        `${DEMO_SET.length} maires FICTIFS chargés — pour découvrir l'outil. ` +
        "Chargez votre propre fichier avant toute campagne réelle.",
    });
  }, []);

  const exportAll = useCallback(async () => {
    const data = await DB.exportAll();
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(data, null, 1)], { type: "application/json" }),
    );
    const a = document.createElement("a");
    a.href = url;
    a.download = `paraphe-suivi-${new Date().toISOString().slice(0, 10)}.json`;
    a.click();
    URL.revokeObjectURL(url);
  }, []);

  const importAll = useCallback(async (file: File, merge: boolean) => {
    try {
      const report = await DB.importAll(JSON.parse(await file.text()), {
        merge,
      });
      const [m, s, c, a, l] = await Promise.all([
        DB.loadMayors(),
        DB.loadTracking(),
        DB.readSetting<Campaign>("campagne", EMPTY_CFG),
        DB.readSetting<string>("argument", ""),
        // the backup carries `liste` too, and not reading it back left the
        // banner offering to load "all 34 826" — which would replace the
        // list just imported
        DB.readSetting<ListKey | "personnel" | "demo" | null>("liste", null),
      ]);
      setMayors(m);
      setTracking(s);
      setCfg(c);
      setPersonalNote(a);
      setDraft(c);
      setNoteDraft(a);
      setLoadedList(l);
      setMessage({
        tone: "ok",
        text:
          `Import : ${report.mayors} maires, ${report.tracking} suivis repris` +
          (report.skipped
            ? `, ${report.skipped} ignorés (plus anciens ou illisibles)`
            : "") +
          (report.settings ? `, ${report.settings} réglages remplacés` : "") +
          (report.keptSettings
            ? `. La campagne du fichier (${report.keptSettings} réglages) n'a ` +
              "PAS été reprise : « fusionner » garde la vôtre. Décochez-le " +
              "pour prendre celle du fichier."
            : ""),
      });
    } catch (e) {
      setMessage({
        tone: "erreur",
        text: `Import impossible : ${(e as Error).message}`,
      });
    }
  }, []);

  useViewFocus(
    !ready ? "chargement" : tab,
    !ready
      ? null
      : tab === "fiche"
        ? chosen?.commune || "Fiche"
        : (VIEW_TITLES[tab] ?? "paraphe"),
  );

  if (!ready)
    return (
      <main>
        <p role="status">Chargement…</p>
      </main>
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
            <span className="sous">version navigateur</span>
          </span>
        </span>
        <NavOnglets
          tabs={[
            ["liste", "Les maires"],
            ["guide", "Guide"],
            ["donnees", "Mes données"],
            ["campagne", "Ma campagne"],
          ]}
          tab={tab}
          onTab={setTab}
        />
        <span className="qui">aucune donnée ne quitte ce navigateur</span>
      </header>
      <div className="rayures" aria-hidden="true" />
      <RenderGuard>
        <main id="contenu" tabIndex={-1}>
          <Alerte message={message} onClose={() => setMessage(null)} />
          {offerError && (
            <p className="alerte erreur" role="alert">
              Ce lien ne propose aucune campagne : {offerError}{" "}
              <button
                type="button"
                className="lien"
                onClick={() => setOfferError(null)}
              >
                fermer
              </button>
            </p>
          )}
          {/* the draft, not cfg: the draft covers everything the volunteer
            has — saved (it starts from cfg and follows every writer) or
            still being typed under this very banner */}
          {offer && untouchedCampaign(draft) && (
            <Proposition
              offer={offer}
              onRefuse={() => {
                setOffer(null);
                // and it must STAY refused: left in the address bar, ?org=
                // brought the offer back at every reload
                const url = new URL(window.location.href);
                url.searchParams.delete("org");
                window.history.replaceState({}, "", url);
              }}
              onAccept={async () => {
                // a rejected write (quota, private window, blocked base) must
                // be SAID: without the catch the button silently did nothing
                try {
                  await DB.writeSetting("campagne", offer.campaign);
                  setCfg(offer.campaign);
                  setDraft(offer.campaign);
                  setOffer(null);
                  setMessage({
                    tone: "ok",
                    text:
                      `Campagne « ${offer.name} » reprise. Elle reste dans ce ` +
                      "navigateur, et vous pouvez la modifier dans « Ma campagne ».",
                  });
                } catch (e) {
                  setMessage({
                    tone: "erreur",
                    text: `Reprise impossible : ${e instanceof Error ? e.message : String(e)}`,
                  });
                }
              }}
            />
          )}
          {unfilled.length > 0 && (
            <p className="alerte">
              {/* one line, not the list of nine labels: the campaign tab
                  marks each missing field itself, and a banner repeated on
                  every screen must stay short enough to leave the screen
                  to the work */}
              <strong>Campagne non configurée.</strong> Les messages contiennent
              encore des valeurs d'exemple : <strong>n'envoyez rien</strong>{" "}
              avant d'avoir rempli l'onglet « Ma campagne ».
              {tab !== "campagne" && (
                <>
                  {" "}
                  <button
                    type="button"
                    className="lien"
                    onClick={() => setTab("campagne")}
                  >
                    Ouvrir l'onglet
                  </button>
                </>
              )}
            </p>
          )}

          {/* persistent region: the start of a download is announced by a
              text CHANGE here, not by the card mounting with its sentence —
              completion is announced by the Alerte above */}
          <span role="status" className="sr-only">
            {download
              ? `Téléchargement de la ${LISTS[download.key].name} en cours.`
              : ""}
          </span>
          {download && <Progression state={download} />}

          {tab === "liste" &&
            (mayors.length === 0 ? (
              <Accueil
                onCsv={loadCsv}
                onDemo={loadDemo}
                onDownload={fetchList}
                download={download}
              />
            ) : (
              <Liste
                mayors={filtered}
                tracking={tracking}
                counts={counts}
                q={q}
                setQ={setQ}
                rankFilter={rankFilter}
                setRankFilter={setRankFilter}
                departments={departments}
                deptFilter={deptFilter}
                setDeptFilter={setDeptFilter}
                onChoose={(m) => {
                  setChosen(m);
                  setTab("fiche");
                }}
                loadedList={loadedList}
                onComplete={() => fetchList("complete")}
                download={download}
              />
            ))}

          {tab === "guide" && <Guide />}

          {tab === "fiche" && chosen && (
            <Fiche
              mayor={chosen}
              cfg={cfg}
              personalNote={personalNote}
              drafts={cardDrafts}
              status={tracking[chosen.insee_code as string]?.status}
              notes={(tracking[chosen.insee_code as string]?.notes ?? []).map(
                (n) => ({ ...n, volunteer: null }),
              )}
              onBack={() => setTab("liste")}
              onStatus={async (status, note) => {
                const insee = chosen.insee_code as string;
                const e = await DB.saveTracking(insee, status, note);
                setTracking((s) => ({ ...s, [insee]: e }));
              }}
            />
          )}

          {tab === "donnees" && (
            <Donnees
              counts={counts}
              onExport={exportAll}
              onImport={importAll}
              onCsv={loadCsv}
              onDemo={loadDemo}
              onDownload={fetchList}
              loadedList={loadedList}
              onErase={async () => {
                await DB.eraseAll();
                setMayors([]);
                setTracking({});
                setCfg(EMPTY_CFG);
                setPersonalNote("");
                setDraft(EMPTY_CFG);
                setNoteDraft("");
                // the drafts carry notes about named mayors: erased means erased
                cardDrafts.current = {};
                setLoadedList(null);
                setMessage({
                  tone: "ok",
                  text: "Tout a été effacé de ce navigateur.",
                });
              }}
            />
          )}

          {tab === "campagne" && (
            <CampaignTab
              draft={draft}
              note={noteDraft}
              dirty={dirty}
              onEdit={setDraft}
              onNote={setNoteDraft}
              onSave={async (next, note) => {
                try {
                  await DB.writeSetting("campagne", next);
                  await DB.writeSetting("argument", note);
                  // cfg only: the draft may already carry keystrokes typed
                  // while these writes are in flight, and they must stay —
                  // the `dirty` marker then reappears on its own
                  setCfg(next);
                  setPersonalNote(note);
                  setMessage({
                    tone: "ok",
                    text: "Campagne enregistrée dans ce navigateur.",
                  });
                } catch (e) {
                  setMessage({
                    tone: "erreur",
                    text: `Enregistrement impossible : ${e instanceof Error ? e.message : String(e)}`,
                  });
                }
              }}
            />
          )}
        </main>
      </RenderGuard>
      <PiedDePage>
        <p>
          <strong>Cette version ne coordonne rien.</strong> Elle ne sait pas si
          un autre bénévole a déjà appelé le même maire : elle convient à une
          personne seule ou à une toute petite équipe qui s'échange le fichier
          de suivi. Pour une campagne à plusieurs dizaines de personnes, la
          version serveur évite les doublons.
        </p>
      </PiedDePage>
    </>
  );
}

/**
 * The campaign a link proposes, shown before anything is written.
 *
 * The values below are what a mayor will read, and the contacts are what
 * they will answer to. A volunteer who did not expect this screen is
 * exactly the person who must see it.
 */
function Proposition({
  offer,
  onAccept,
  onRefuse,
}: {
  offer: Offer;
  onAccept: () => void;
  onRefuse: () => void;
}) {
  return (
    <div className="carte alerte">
      <h2 style={{ marginTop: 0 }}>Reprendre la campagne « {offer.name} » ?</h2>
      <p className="gris">
        Ce lien propose de remplir votre campagne avec les valeurs ci-dessous,
        publiées par l'instance{" "}
        <code>
          {offer.slug}.{instanceDomain()}
        </code>
        . Elles rempliront tous vos messages aux maires. Rien n'est enregistré
        tant que vous n'avez pas accepté.
      </p>
      <table>
        <tbody>
          {M.CAMPAIGN_KEYS.map((k) => (
            <tr key={k}>
              <td className="gris">{campaignLabel(k)}</td>
              <td>{offer.campaign[k]}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <p>
        <button type="button" onClick={onAccept}>
          Reprendre cette campagne
        </button>{" "}
        <button type="button" className="secondaire" onClick={onRefuse}>
          Non, je remplis moi-même
        </button>
      </p>
    </div>
  );
}

interface DownloadState extends Progress {
  key: ListKey;
}

function Progression({ state }: { state: DownloadState }) {
  const { key, received, total } = state;
  const pct = total ? Math.round((received / total) * 100) : null;
  return (
    // no live region here: the card mounts together with its sentence,
    // which assistive technology may not announce. The announcement is the
    // persistent sr-only region in Browser; this card is the visual.
    <div className="carte chargement">
      <p style={{ margin: 0 }}>
        <strong>Téléchargement de la {LISTS[key].name}…</strong>{" "}
        <span className="gris">
          {formatBytes(received)}
          {total ? ` sur ${formatBytes(total)}` : ""}
        </span>
      </p>
      <div
        className="jauge"
        role="progressbar"
        aria-label={`Téléchargement de la ${LISTS[key].name}`}
        aria-valuemin={0}
        aria-valuemax={100}
        // undefined while the size is unknown: an indeterminate bar is
        // exactly what a progressbar without aria-valuenow means
        aria-valuenow={pct ?? undefined}
      >
        <div
          className={pct === null ? "barre indeterminee" : "barre"}
          style={pct === null ? undefined : { width: `${pct}%` }}
        />
      </div>
      <p className="gris" style={{ margin: ".3rem 0 0" }}>
        {LISTS[key].detail} — une fois chargée, elle reste dans ce navigateur et
        ne sera plus retéléchargée.
      </p>
    </div>
  );
}

interface AccueilProps {
  onCsv: (f: File) => void;
  onDemo: () => void;
  onDownload: (key: ListKey) => void;
  download: DownloadState | null;
}

function Accueil({ onCsv, onDemo, onDownload, download }: AccueilProps) {
  const field = useRef<HTMLInputElement>(null);
  return (
    <>
      <h1>Aucune liste chargée</h1>
      <div className="carte">
        <p>
          Cette version fonctionne{" "}
          <strong>entièrement dans votre navigateur</strong>. La seule requête
          réseau qu'elle fasse est le téléchargement de la liste ci-dessous :
          aucune de vos notes, aucun nom de candidat, aucune action n'est jamais
          transmise.
        </p>
        <p>
          <button
            type="button"
            disabled={!!download}
            onClick={() => onDownload("light")}
          >
            <Emoji>⬇ </Emoji>Charger la liste prioritaire
          </button>{" "}
          <button
            type="button"
            className="secondaire"
            disabled={!!download}
            onClick={() => onDownload("complete")}
          >
            <Emoji>⬇ </Emoji>Charger tous les maires de France
          </button>
        </p>
        <p className="gris">
          Ou bien :{" "}
          <button
            type="button"
            className="lien"
            onClick={() => field.current?.click()}
          >
            charger mon propre fichier
          </button>{" "}
          (CSV produit par
          <code> task build</code>) ·{" "}
          <button type="button" className="lien" onClick={onDemo}>
            essayer avec des données fictives
          </button>
        </p>
        <input
          ref={field}
          type="file"
          accept=".csv,text/csv"
          hidden
          onChange={(e) => {
            const f = e.target.files?.[0];
            if (f) onCsv(f);
          }}
        />
      </div>
    </>
  );
}

interface ListeProps {
  mayors: Mayor[];
  tracking: Record<string, Tracking>;
  counts: Record<string, number>;
  q: string;
  setQ: (v: string) => void;
  rankFilter: string;
  setRankFilter: (v: string) => void;
  departments: string[];
  deptFilter: string;
  setDeptFilter: (v: string) => void;
  onChoose: (m: Mayor) => void;
  loadedList: ListKey | "personnel" | "demo" | null;
  onComplete: () => void;
  download: DownloadState | null;
}

function Liste({
  mayors,
  tracking,
  counts,
  q,
  setQ,
  rankFilter,
  setRankFilter,
  departments,
  deptFilter,
  setDeptFilter,
  onChoose,
  loadedList,
  onComplete,
  download,
}: ListeProps) {
  const [shown, setShown] = useState(50);
  // the filter changes: back to the top, otherwise a 300-row window stays
  // open on a list that only has 12 left
  // the three filters are TRIGGERS — the effect reads none of them, it only
  // has to run when one changes. That is the whole behaviour.
  // biome-ignore lint/correctness/useExhaustiveDependencies: triggers, read by nothing
  useEffect(() => setShown(50), [q, rankFilter, deptFilter]);
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
            Qui afficher
            <select
              value={rankFilter}
              onChange={(e) => setRankFilter(e.target.value)}
            >
              {Object.entries(M.RANKS).map(([k, v]) => (
                <option key={k} value={k}>
                  {v} ({counts[k] ?? 0})
                </option>
              ))}
              <option value="all">Tous ({counts.total})</option>
            </select>
          </label>
        </div>
        <div>
          <label>
            Département
            <select
              value={deptFilter}
              onChange={(e) => setDeptFilter(e.target.value)}
            >
              <option value="">— tous ({departments.length}) —</option>
              {departments.map((d) => (
                <option key={d} value={d}>
                  {d}
                </option>
              ))}
            </select>
          </label>
        </div>
      </form>
      {rankFilter !== "has_endorsed" && (
        <p className="alerte">
          Vous êtes sorti du vivier prioritaire. Le message généré s'adapte au
          rang : on n'y remercie personne d'un parrainage qui n'a pas eu lieu.
        </p>
      )}
      {loadedList === "light" && (
        <p className="alerte">
          <strong>Liste prioritaire</strong> ({counts.total} maires) — les 500
          signatures n'en sortiront pas seules : épuisée,{" "}
          <button
            type="button"
            className="lien"
            disabled={!!download}
            onClick={onComplete}
          >
            chargez les 34 826 maires de France
          </button>{" "}
          <span className="gris">
            (2 Mo, une seule fois ; votre suivi est conservé)
          </span>
          .
        </p>
      )}
      <CompteurResultats
        shown={Math.min(shown, mayors.length)}
        total={mayors.length}
      />
      <div className="carte">
        <table>
          <thead>
            <tr>
              <th scope="col">Commune</th>
              <th scope="col">Département</th>
              <th scope="col">Signal</th>
              <th scope="col">Statut</th>
            </tr>
          </thead>
          <tbody>
            {mayors.slice(0, shown).map((m) => (
              <tr key={m.insee_code as string}>
                <td>
                  <button
                    type="button"
                    className="lien"
                    onClick={() => onChoose(m)}
                  >
                    <strong>{m.commune}</strong>
                  </button>
                  <br />
                  <span className="gris">
                    {m.title} {m.first_name} {m.last_name}
                  </span>
                </td>
                <td>{m.department}</td>
                <td className="gris">
                  {M.rank(m) === "has_endorsed"
                    ? `${m.recent_candidate} (${m.recent_year})`
                    : M.RANKS[M.rank(m)]}
                </td>
                <td>
                  <Chip
                    status={
                      tracking[m.insee_code as string]?.status ?? "to_contact"
                    }
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {mayors.length === 0 && (
          <p className="gris">Aucun maire avec ces critères.</p>
        )}
        {shown < mayors.length && (
          <p style={{ textAlign: "center" }}>
            <button
              type="button"
              className="secondaire"
              onClick={() => setShown((c) => c + 50)}
            >
              Afficher la suite
            </button>
          </p>
        )}
      </div>
    </>
  );
}

interface DonneesProps {
  counts: Record<string, number>;
  onExport: () => void;
  onImport: (f: File, merge: boolean) => void;
  onCsv: (f: File) => void;
  onDemo: () => void;
  onErase: () => void;
  onDownload: (key: ListKey) => void;
  loadedList: ListKey | "personnel" | "demo" | null;
}

function Donnees({
  counts,
  onExport,
  onImport,
  onCsv,
  onDemo,
  onErase,
  onDownload,
  loadedList,
}: DonneesProps) {
  const importField = useRef<HTMLInputElement>(null);
  const csvField = useRef<HTMLInputElement>(null);
  const [merge, setMerge] = useState(true);
  return (
    <>
      <h1>Mes données</h1>
      <div className="carte">
        <p>
          Tout est dans ce navigateur, dans une base locale. Rien n'est envoyé,
          rien n'est sauvegardé ailleurs —{" "}
          <strong>vider les données du site les supprime définitivement</strong>
          . Exportez régulièrement.
        </p>
        <p className="gris">{counts.total} maires chargés.</p>
        <p>
          <button type="button" onClick={onExport}>
            <Emoji>⬇ </Emoji>Exporter (JSON)
          </button>{" "}
          <button
            type="button"
            className="secondaire"
            onClick={() => importField.current?.click()}
          >
            <Emoji>⬆ </Emoji>Importer une sauvegarde
          </button>
        </p>
        <p>
          <label>
            <input
              type="checkbox"
              checked={merge}
              onChange={(e) => setMerge(e.target.checked)}
            />{" "}
            Fusionner plutôt qu'écraser
          </label>
          <br />
          <span className="gris">
            Fusionner garde votre travail et ne reprend que ce qui est plus
            récent : c'est ce qu'il faut quand deux bénévoles s'échangent leurs
            fichiers.
          </span>
        </p>
        <input
          ref={importField}
          type="file"
          accept=".json,application/json"
          hidden
          onChange={(e) => {
            const f = e.target.files?.[0];
            if (f) onImport(f, merge);
          }}
        />
      </div>

      <div className="carte">
        <h2 style={{ marginTop: 0 }}>Changer de liste</h2>
        <p className="gris">
          Chargée actuellement :{" "}
          {loadedList
            ? loadedList === "demo"
              ? "un jeu de données FICTIVES"
              : loadedList === "personnel"
                ? "votre propre fichier"
                : // read from a backup the user supplies: an unknown key
                  // must not throw where the screen says something
                  (LISTS[loadedList]?.name ?? "une liste inconnue")
            : "aucune"}
          . Changer de liste ne touche pas à votre suivi : il est indexé par
          code INSEE.
        </p>
        <p>
          <button type="button" onClick={() => onDownload("light")}>
            <Emoji>⬇ </Emoji>Liste prioritaire
          </button>{" "}
          <button type="button" onClick={() => onDownload("complete")}>
            <Emoji>⬇ </Emoji>Base complète
          </button>
        </p>
        <p>
          <button
            type="button"
            className="secondaire"
            onClick={() => csvField.current?.click()}
          >
            <Emoji>📂 </Emoji>Charger un CSV
          </button>{" "}
          <button type="button" className="secondaire" onClick={onDemo}>
            Données fictives
          </button>
        </p>
        <input
          ref={csvField}
          type="file"
          accept=".csv,text/csv"
          hidden
          onChange={(e) => {
            const f = e.target.files?.[0];
            if (f) onCsv(f);
          }}
        />
      </div>

      <div className="carte">
        <h2 style={{ marginTop: 0 }}>Tout effacer</h2>
        <p className="gris">
          À faire en fin de campagne : les notes portent sur des personnes
          nommées.
        </p>
        <button
          type="button"
          className="secondaire"
          onClick={() => {
            if (
              confirm(
                "Effacer définitivement la liste, le suivi et la configuration ?",
              )
            )
              onErase();
          }}
        >
          Effacer ce navigateur
        </button>
      </div>
    </>
  );
}

// Fully controlled: the draft state lives in Browser (this tab is
// unmounted by every tab switch, and typed work must survive one).
interface CampaignTabProps {
  draft: Campaign;
  note: string;
  dirty: boolean;
  onEdit: (draft: Campaign) => void;
  onNote: (note: string) => void;
  onSave: (cfg: Campaign, personalNote: string) => void;
}

function CampaignTab({
  draft,
  note,
  dirty,
  onEdit,
  onNote,
  onSave,
}: CampaignTabProps) {
  return (
    <>
      <h1>Ma campagne</h1>
      <div className="carte">
        <p className="gris">
          Ces valeurs remplissent les messages. Elles restent dans ce
          navigateur.
        </p>
        {CAMPAIGN_FIELDS.map((f, i) => (
          <div key={f.key}>
            {f.group !== CAMPAIGN_FIELDS[i - 1]?.group && (
              // h2: right under the tab's h1 — h3 would skip a level here
              <h2 className="groupe">{f.group}</h2>
            )}
            <p>
              {/* associated by id, not nested: a textarea nested in its
                  label makes its own CONTENT part of the label's text */}
              <label htmlFor={`champ-${f.key}`}>{f.label}</label>
              {f.long ? (
                <textarea
                  id={`champ-${f.key}`}
                  rows={3}
                  placeholder={f.example}
                  aria-describedby={f.hint ? `champ-${f.key}-aide` : undefined}
                  value={draft[f.key]}
                  onChange={(e) =>
                    onEdit({ ...draft, [f.key]: e.target.value })
                  }
                />
              ) : (
                <input
                  id={`champ-${f.key}`}
                  type="text"
                  placeholder={f.example}
                  aria-describedby={f.hint ? `champ-${f.key}-aide` : undefined}
                  value={draft[f.key]}
                  onChange={(e) =>
                    onEdit({ ...draft, [f.key]: e.target.value })
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
            Votre touche personnelle (insérée dans vos emails)
            <textarea
              rows={3}
              value={note}
              onChange={(e) => onNote(e.target.value)}
            />
          </label>
        </p>
        <button type="button" onClick={() => onSave(draft, note)}>
          Enregistrer
        </button>
        {dirty && (
          <span className="gris" role="status" style={{ marginLeft: ".6rem" }}>
            modifications non enregistrées
          </span>
        )}
      </div>
    </>
  );
}
