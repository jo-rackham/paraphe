import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Accueil } from "./BrowserAccueil.tsx";
import { CampaignTab } from "./BrowserCampagne.tsx";
import { Donnees } from "./BrowserDonnees.tsx";
import { Liste } from "./BrowserListe.tsx";
import { type DownloadState, Progression } from "./BrowserProgression.tsx";
import { Proposition } from "./BrowserProposition.tsx";
import {
  Alerte,
  type CardDraft,
  EMPTY_CFG,
  Fiche,
  focusContenu,
  Guide,
  Marque,
  NavOnglets,
  PiedDePage,
  RenderGuard,
  rescueFocusAfterCommit,
  SkipLink,
  ThemeToggle,
  useSubmitGuard,
  useViewFocus,
} from "./common.tsx";
import { LISTS, type ListKey, loadList, type Progress } from "./data.ts";
import * as DB from "./db.ts";
import { DEMO_SET } from "./demo.ts";
import * as M from "./messages.ts";
import {
  ADOPTED_KEYS,
  type ConfiguredOffer,
  fetchCampaign,
  inlineLogo,
  type Offer,
  offeredTemplates,
  ownCampaign,
  readAdoption,
  requestedSlug,
  sameAdoptedFields,
  sameTemplates,
  servingInstanceHome,
  untouchedCampaign,
} from "./prefill.ts";
import { navigate, useView } from "./route.tsx";
import type { Campaign, Mayor, Message, Tracking } from "./types.ts";

// Browser mode: no data leaves the computer. The only network request is
// downloading the list published next to the application.

// Addresses here too, and the mode that needs them most: nothing behind
// this page, so a visitor who presses « précédent » on a card leaves for
// wherever they came from — with their notes still in IndexedDB and no
// screen showing them. A card lives under the list, `/liste/<insee>`.
const BROWSER_VIEWS = ["liste", "guide", "donnees", "campagne"] as const;

const VIEW_TITLES: Record<string, string> = {
  liste: "Les maires",
  guide: "Guide",
  donnees: "Mes données",
  campagne: "Ma campagne",
};

export default function Browser() {
  const [mayors, setMayors] = useState<Mayor[]>([]);
  const [tracking, setTracking] = useState<Record<string, Tracking>>({});
  /**
   * Revising a history line, and REFRESHING WHAT THE SCREEN HOLDS WHEN IT IS
   * REFUSED.
   *
   * A refusal here means one thing: this browser holds a version the screen
   * has not seen, because another window wrote. This mode reads its store
   * ONCE, at load — no card opening re-reads it — so « rouvrez la fiche »
   * sent the volunteer to the list and back to exactly what they had, and the
   * second attempt was refused the same way. The only gesture that worked was
   * a full reload, which the sentence never mentioned.
   *
   * Re-read on the refusal and the tool heals itself: the history under the
   * message is the true one, and the volunteer can see what changed and
   * decide. The refusal is re-raised — the card owns the sentence.
   */
  const reviseNote = async (
    insee: string,
    act: (insee: string) => Promise<Tracking>,
  ) => {
    try {
      const revised = await act(insee);
      setTracking((s) => ({ ...s, [insee]: revised }));
    } catch (e) {
      const fresh = await DB.readTracking(insee);
      if (fresh) setTracking((s) => ({ ...s, [insee]: fresh }));
      throw e;
    }
  };
  const [cfg, setCfg] = useState<Campaign>(EMPTY_CFG);
  // The logo as a DATA URI, never a URL. This mode promises that nothing
  // leaves the browser, and a remote address in the header would make that
  // false at every load — so a logo adopted from ?org= is downloaded once,
  // at the moment the volunteer accepts, and kept in IndexedDB like the
  // rest. A new key in an existing store: no VERSION bump, and the export
  // carries it because it walks the settings.
  const [logo, setLogo] = useState("");
  const [personalNote, setPersonalNote] = useState("");
  // THIS VOLUNTEER'S OWN overlay — only what they rewrote themselves, and
  // nothing a campaign handed over. Stored under `modeles`, a key in the
  // existing `settings` store, so no VERSION bump and the export carries it.
  const [templates, setTemplates] = useState<M.Templates>({});
  // THE CAMPAIGN'S LAYER, under the volunteer's: local → campaign → image,
  // the exact resolution team mode already lives by. It is a CACHE of what
  // the adopted campaign says (`modeles_campagne`), refreshed whenever the
  // origin answers — so a coordination's correction reaches every browser
  // that did not rewrite that file — and standing at its last known state on
  // a static publication, offline, or after an adoption by link.
  //
  // Without it a campaign that had rewritten its letter spoke with two
  // voices: one to the volunteers with an account, one to the volunteers
  // without, and nothing on either screen saying which.
  const [campaignTemplates, setCampaignTemplates] = useState<M.Templates>({});
  // OPT-IN, so `false` until this volunteer says otherwise. The email used to
  // ask for a telephone exchange and the letter to announce a call whatever
  // the campaign actually did — a promise to elected officials made by a tool
  // on behalf of somebody who never made it.
  const [appelTelephonique, setAppelTelephonique] = useState(false);
  const {
    view,
    card: routedCard,
    go: setTab,
    hrefOf,
  } = useView(BROWSER_VIEWS, "liste");
  const tab = view === "liste" && routedCard ? "fiche" : view;
  // The card comes from what this browser HOLDS — no network in this mode,
  // by promise. So a shared link resolves against the list already
  // downloaded; an INSEE outside it lands on the list rather than on an
  // empty card, and the visitor can widen the pool themselves. Fetching the
  // full file on their behalf would be a download nobody asked for, in the
  // one mode whose whole claim is that nothing happens without them.
  const chosen = routedCard
    ? (mayors.find((m) => m.insee_code === routedCard) ?? null)
    : null;
  useEffect(() => {
    if (routedCard && mayors.length > 0 && !chosen) {
      navigate([], { replace: true });
    }
  }, [routedCard, mayors.length, chosen]);
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
  const [offer, setOffer] = useState<ConfiguredOffer | null>(null);
  const [offerError, setOfferError] = useState<string | null>(null);
  // Where the campaign came from, when it came from this origin. ITS OWN
  // SLOT and not the page's Alerte: the list download is already in flight
  // when this is decided and lands a second later with « N maires chargés »,
  // which took the sentence off the screen — the same clobber `offerError`
  // above exists for, one writer along. Nine values that go out in every
  // message to a mayor are worth saying the provenance of.
  const [adopted, setAdopted] = useState<string | null>(null);
  // The campaign a ?org= link NAMES, held until the volunteer asks for it.
  // Fetching it on load put a request to <slug>.<instance> in the network
  // tab before any click — the one thing this mode promises does not happen,
  // and the promise says it must be verifiable there rather than asserted.
  // A link shared publicly would otherwise ring its instance, with the
  // visitor's address, every time the page opened.
  const [pendingSlug, setPendingSlug] = useState<string | null>(null);
  const [fetching, setFetching] = useState(false);
  const [looking, doneLooking] = useSubmitGuard();
  // The « Ma campagne » draft lives HERE, not in the tab: the tab is
  // unmounted by a tab switch, and an unsaved draft is the volunteer's
  // work — it must survive a look at the Guide. No effect resyncs it from
  // `cfg` either: an effect cannot tell an adoption from a save landing,
  // and it reverts keystrokes typed while a save is in flight. The
  // writers below (accept, import, erase) reset it explicitly instead.
  const [draft, setDraft] = useState<Campaign>(EMPTY_CFG);
  const [noteDraft, setNoteDraft] = useState("");
  const [appelDraft, setAppelDraft] = useState(false);
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
      // a list landing unmounts the Accueil — and the very button that
      // asked for it; from « Mes données » the button survives, and the
      // rescue is then a no-op
      rescueFocusAfterCommit();
    }
  }, []);

  /**
   * The nine fields and the mark, written as an adoption writes them — and
   * by the refresh that keeps them following the campaign, which must not
   * touch the template overlay. May throw; `adopt` owns the sentence.
   *
   * The logo is downloaded HERE and kept as a data URI: this mode makes no
   * request afterwards, and holding the campaign's URL would put a call to
   * the instance in every page load — exactly what « aucune donnée ne
   * quitte ce navigateur » promises does not happen. A failure costs the
   * picture and nothing else, so it does not undo the adoption.
   */
  const adoptFields = useCallback(
    async (taken: ConfiguredOffer, current: Campaign) => {
      // WHO SIGNS DOES NOT TRAVEL. Seven of the nine describe the candidate and
      // how to reach the campaign, and they exist to be handed over; the other
      // two name a PERSON. Adopted with the rest, every message this volunteer
      // produced went out over the coordination's name and role — the person
      // who happened to fill the form — and nothing on screen said so. Team
      // mode never showed it: there, each account supplies its own.
      const campaign = { ...taken.campaign };
      for (const k of M.PERSONAL_CAMPAIGN_KEYS) campaign[k] = current[k] ?? "";
      await DB.writeSetting("campagne", campaign);
      setCfg(campaign);
      setDraft(campaign);
      // WHAT THIS ADOPTION WROTE, remembered — the reference that lets the
      // FIELDS follow the campaign: still byte-for-byte what the adoption
      // wrote means the volunteer typed none of it, and the campaign's next
      // correction may land. The templates need no snapshot any more: the
      // campaign's texts live in their own LAYER, which follows on its own,
      // and the local overlay is only ever the volunteer's writing.
      await DB.writeSetting("adoption", {
        slug: taken.slug,
        campaign: Object.fromEntries(
          ADOPTED_KEYS.map((k) => [k, taken.campaign[k] ?? ""]),
        ),
      });
      if (taken.logo) {
        try {
          const inlined = await inlineLogo(taken.logo);
          await DB.writeSetting("logo", inlined);
          setLogo(inlined);
        } catch {
          setLogo("");
        }
      }
    },
    [],
  );

  /**
   * Takes a campaign into this browser: the nine values, the mark, and the
   * campaign's texts as a LAYER of their own.
   *
   * ONE writer for the two doors — the campaign this origin is, taken as
   * the default, and the campaign a link offered and the volunteer
   * accepted. They differ in whether anything was asked, never in what is
   * written, and written twice they would differ in the logo, which is the
   * half that is easy to forget.
   */
  const adopt = useCallback(
    async (taken: ConfiguredOffer, current: Campaign) => {
      // a rejected write (quota, private window, blocked base) must be SAID:
      // without the catch this returns having done nothing at all
      try {
        await adoptFields(taken, current);
        // THE CAMPAIGN'S TEXTS ARE A LAYER, never merged into the local
        // overlay: local → campaign → image, the exact resolution team mode
        // already lives by, so an empty local file INHERITS the campaign's
        // as it changes. Adopting also clears the local overlay — the screen
        // says a campaign replaces these texts, and what is local is only
        // ever this volunteer's own writing.
        await DB.writeSetting("modeles_campagne", taken.templates);
        setCampaignTemplates(taken.templates);
        await DB.writeSetting("modeles", {});
        setTemplates({});
        return true;
      } catch (e) {
        setMessage({
          tone: "erreur",
          text: `Reprise impossible : ${e instanceof Error ? e.message : String(e)}`,
        });
        return false;
      }
    },
    [adoptFields],
  );

  useEffect(() => {
    (async () => {
      const [m, s, c, a, g, l, tel, mod, adopRaw, layerRaw] = await Promise.all(
        [
          DB.loadMayors(),
          DB.loadTracking(),
          DB.readSetting<Campaign>("campagne", EMPTY_CFG),
          DB.readSetting<string>("argument", ""),
          DB.readSetting<unknown>("logo", ""),
          DB.readSetting<ListKey | "personnel" | "demo" | null>("liste", null),
          // opt-in: a database written before this setting existed answers
          // false, which is the answer that promises nothing
          DB.readSetting<boolean>("appel_telephonique", false),
          // absent in a database written before this existed: `{}` is the
          // shipped texts, which is what those volunteers were already
          // reading. Read back through the SAME filter as the wire
          // (`offeredTemplates`): a restored backup, another tab or an older
          // version can have written anything here, and a stored overlay is
          // judged like an offered one.
          DB.readSetting<unknown>("modeles", {}),
          // what the last adoption wrote — the reference that lets the nine
          // FIELDS follow the campaign; judged by `readAdoption`
          DB.readSetting<unknown>("adoption", null),
          // the campaign's own layer, as last seen from its site
          DB.readSetting<unknown>("modeles_campagne", {}),
        ],
      );
      setMayors(m);
      setTracking(s);
      setCfg(c);
      setPersonalNote(a);
      setAppelTelephonique(tel === true);
      setTemplates(offeredTemplates(mod));
      setCampaignTemplates(offeredTemplates(layerRaw));
      // checked on the way OUT too: a database written before this
      // guard existed, or by another tab, is not this code's doing
      setLogo(DB.usableLogo(g) ? g : "");
      setDraft(c);
      setNoteDraft(a);
      setAppelDraft(tel === true);
      setLoadedList(l);
      setReady(true);
      // first launch: the priority list loads by default — it is light and
      // covers the bulk of the work. The full base waits to be asked for.
      if (m.length === 0) fetchList("light");

      const slug = requestedSlug(window.location.search);
      // THE CAMPAIGN THIS ORIGIN IS, taken as the default and not offered.
      //
      // Served under /navigateur/ by `<slug>.paraphe.org`, this build is
      // that campaign's own account-less version: whoever opens it wants
      // its texts, and asking them to accept the texts of the site they are
      // already standing on is a question with one answer. It read as a
      // tool that had failed to fill anything in — twice, from the same
      // reader, which is how a design gets found out.
      //
      // The offer stays for the case it was written for: a `?org=` naming
      // ANOTHER campaign is a link, and a link is shown before it is
      // applied. Naming THIS one is not a second opinion, it is the same
      // campaign arriving by a longer road.
      //
      // On an untouched campaign, like everything else that writes these
      // nine values: what the volunteer typed is theirs. And on a campaign
      // this browser may still be FOLLOWING — adopted and never retouched —
      // because the answer decides whether there is anything to follow.
      const fresh = untouchedCampaign(c);
      const localTpl = offeredTemplates(mod);
      const cachedTpl = offeredTemplates(layerRaw);
      const adoption = readAdoption(adopRaw);
      // Asked UNCONDITIONALLY: the campaign's layer is live, and which
      // browsers may follow is decided by the ANSWER, not by guessing from
      // the store — a browser adopted before the layer or the snapshot
      // existed carries nothing that says so. It is the one request that
      // names nothing, to the origin that already served this page; on an
      // apex, a static host or a captive portal it answers « no campaign
      // here », which is silence.
      let own: Offer | null = null;
      try {
        own = await ownCampaign();
      } catch (e) {
        // The campaign that served this page ANSWERED, and this build
        // could not take what it said. Absence is silent — an apex, a
        // static host — but an answer refused is not: silence here left
        // « Prénom NOM » on screen looking exactly like a tool that had
        // failed to substitute anything. Not fatal, so it is a sentence
        // and a way to act rather than the error boundary. Two sentences,
        // because the situations differ: on a fresh campaign nothing was
        // filled in; on a followed one, what is here stands.
        const why = e instanceof Error ? e.message : String(e);
        setOfferError(
          fresh
            ? "Les textes de cette campagne n'ont pas pu être repris " +
                `automatiquement. ${why} Vous pouvez les saisir ` +
                "vous-même dans « Ma campagne »."
            : "Les textes de cette campagne n'ont pas pu être vérifiés " +
                `depuis son site. ${why} Ceux enregistrés dans ce ` +
                "navigateur restent en vigueur.",
        );
      }
      if (own && (!slug || slug === own.slug)) {
        // The nine fields, or NULL for a campaign still at its template
        // values: the server omits the block rather than spread « Prénom
        // NOM », and refused wholesale — the old 409 — it took the
        // campaign's own TEXTS down with it. The layer below follows either
        // way; only what touches the nine needs the fields.
        const offered = own.campaign;
        if (offered !== null && fresh) {
          if (await adopt({ ...own, campaign: offered }, c)) {
            setAdopted(
              `Les textes de la campagne « ${own.name} » sont repris depuis ` +
                "son site. Ils restent dans ce navigateur, et vous pouvez " +
                "les modifier dans « Ma campagne ».",
            );
          }
          return;
        }
        // THE CAMPAIGN'S OWN BROWSER VERSION FOLLOWS ITS CAMPAIGN.
        //
        // The campaign's texts are a LAYER under the volunteer's overlay —
        // local → campaign → image, the resolution team mode already lives
        // by — and THE LAYER FOLLOWS THE ORIGIN UNCONDITIONALLY: it
        // overwrites nothing (the overlay masks it file by file), and the
        // campaign that served this very page is the evident default for
        // what an empty file inherits. Gated on the nine fields being
        // intact, one locally edited description silenced every template
        // the campaign wrote — measured on production, and reported as
        // « ne charge toujours pas par défaut ». Only the FIELDS keep the
        // snapshot condition, because they are what a refresh overwrites.
        const snapshot =
          adoption !== null && adoption.slug === own.slug ? adoption : null;
        const fieldsUntouched =
          offered !== null &&
          (sameAdoptedFields(c, offered) ||
            (snapshot !== null && sameAdoptedFields(c, snapshot.campaign)));
        try {
          let followed = false;
          // MIGRATION, once: before the layer existed, adopting MERGED the
          // campaign's texts into the local overlay. An overlay that is
          // byte for byte what the campaign says (or what the old
          // snapshot copied) was never the volunteer's writing — it moves
          // under the layer, where it goes back to inheriting.
          if (
            Object.keys(cachedTpl).length === 0 &&
            Object.keys(localTpl).length > 0 &&
            (sameTemplates(localTpl, own.templates) ||
              (snapshot !== null &&
                sameTemplates(localTpl, snapshot.templates)))
          ) {
            await DB.writeSetting("modeles", {});
            setTemplates({});
          }
          if (!sameTemplates(cachedTpl, own.templates)) {
            await DB.writeSetting("modeles_campagne", own.templates);
            setCampaignTemplates(own.templates);
            followed = true;
          }
          if (offered !== null && fieldsUntouched) {
            if (!sameAdoptedFields(c, offered)) {
              // reachable only through the snapshot branch above: the
              // campaign corrected a field the volunteer never touched
              await adoptFields({ ...own, campaign: offered }, c);
              followed = true;
            } else {
              // arm the snapshot for the day the campaign edits a field: a
              // browser from before it existed has none, and without one
              // that first edit would read as the volunteer's
              await DB.writeSetting("adoption", {
                slug: own.slug,
                campaign: Object.fromEntries(
                  ADOPTED_KEYS.map((k) => [k, offered[k] ?? ""]),
                ),
              });
            }
          }
          if (followed) {
            setAdopted(
              `Les textes de la campagne « ${own.name} » ont été mis à ` +
                "jour depuis son site. Ils restent dans ce navigateur, " +
                "et vous pouvez les modifier dans « Ma campagne ».",
            );
          }
        } catch (e) {
          // a rejected write must be said, like the adoption's own
          setMessage({
            tone: "erreur",
            text: `Mise à jour impossible : ${e instanceof Error ? e.message : String(e)}`,
          });
        }
        // A campaign with no fields yet holds the visit here too: there is
        // nothing a ?org= link naming this same campaign could add, and its
        // « déjà enregistrée » sentence would be about fields nobody has.
        if (fieldsUntouched || offered === null) return;
        // past here the campaign's nine fields are the volunteer's own
        // writing, and a ?org= link is answered exactly as before — its
        // sentence speaks about the fields, which is what a link would
        // have replaced
      }
      // Offered ONLY on a campaign nobody has touched. "Not complete" was
      // the wrong test: a volunteer who had filled eight fields of nine —
      // their own name under « Qui signe les emails », their own phone —
      // was offered a link that replaced all nine, and `signataire` is the
      // signature at the bottom of every email to a mayor.
      // …and REMEMBERED, never fetched here: see pendingSlug above.
      if (slug && untouchedCampaign(c)) setPendingSlug(slug);
      // …and SAID when it is not offered. A link that names a campaign and
      // produces nothing at all is the same silence twice over: whoever
      // sent it believes it worked, and whoever opened it reads the
      // example values as the campaign's own. It is not a refusal to
      // reverse — the values already here are this volunteer's, and a link
      // does not overwrite them — so it is a sentence and a way to act,
      // in the slot the broken-link message already owns.
      else if (slug) {
        setOfferError(
          `Ce lien propose la campagne « ${slug} », mais une campagne est ` +
            "déjà enregistrée dans ce navigateur et un lien ne la remplace " +
            "pas. Ouvrez « Ma campagne » pour la voir, ou effacez-la depuis " +
            "« Mes données » avant de rouvrir le lien.",
        );
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
  }, [fetchList, adopt, adoptFields]);

  const unfilled = useMemo(() => M.unfilledKeys(cfg), [cfg]);
  // What is missing about the CAMPAIGN, as opposed to about the person
  // sending. The two are told apart because only the second is the reader's
  // to fix, and a campaign adopted from its own site is missing exactly the
  // second.
  const campaignUnfilled = useMemo(
    () => unfilled.filter((k) => !M.PERSONAL_CAMPAIGN_KEYS.includes(k)),
    [unfilled],
  );

  // Derived, never stored: whether the draft differs from what is saved.
  // Without it, a keystroke landing during a save stayed on screen under a
  // green « Campagne enregistrée » banner while the base held the older
  // value — indistinguishable from saved work until the next reload.
  const dirty =
    JSON.stringify(draft) !== JSON.stringify(cfg) ||
    noteDraft !== personalNote ||
    appelDraft !== appelTelephonique;

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
    } finally {
      rescueFocusAfterCommit();
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
    rescueFocusAfterCommit();
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
      const [m, s, c, a, g, l, tel, mod, layerRaw] = await Promise.all([
        DB.loadMayors(),
        DB.loadTracking(),
        DB.readSetting<Campaign>("campagne", EMPTY_CFG),
        DB.readSetting<string>("argument", ""),
        // the logo travels in the backup like every other setting, and
        // « fusionner » protects the one you hold, key by key
        DB.readSetting<unknown>("logo", ""),
        // the backup carries `liste` too, and not reading it back left the
        // banner offering to load "all 34 826" — which would replace the
        // list just imported
        DB.readSetting<ListKey | "personnel" | "demo" | null>("liste", null),
        // …and it carries this one, so a restored backup keeps its answer
        // rather than silently reverting to « we do not telephone »
        DB.readSetting<boolean>("appel_telephonique", false),
        // absent in a database written before this existed: `{}` is the
        // shipped texts, which is what those volunteers were already reading.
        // Read back through the SAME filter as the wire (`offeredTemplates`):
        // a restored backup, another tab or an older version can have written
        // anything here, and a stored overlay is judged like an offered one.
        DB.readSetting<unknown>("modeles", {}),
        // the campaign's layer travels in the backup like the overlay above
        DB.readSetting<unknown>("modeles_campagne", {}),
      ]);
      setMayors(m);
      setTracking(s);
      setCfg(c);
      setPersonalNote(a);
      setAppelTelephonique(tel === true);
      setTemplates(offeredTemplates(mod));
      setCampaignTemplates(offeredTemplates(layerRaw));
      // checked on the way OUT too: a database written before this
      // guard existed, or by another tab, is not this code's doing
      setLogo(DB.usableLogo(g) ? g : "");
      setDraft(c);
      setNoteDraft(a);
      setAppelDraft(tel === true);
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

  // No `!ready` early return: the shell — and above all the live regions
  // inside it — must exist from the FIRST render. `setReady(true)` and the
  // first download land in the same React batch, so a region living only
  // in the ready tree mounts together with its first message, which some
  // assistive technology never announces.
  return (
    <>
      <SkipLink />
      <div className="tricolore" aria-hidden="true">
        <i />
        <i />
        <i />
      </div>
      <header>
        {/* the logo is a data URI held in IndexedDB: this mode fetches
            nothing remote, and its whole promise rests on that */}
        <Marque
          logo={logo ? { url: logo, type: "" } : null}
          sous="version navigateur"
          onHome={() => setTab("liste")}
        />
        <NavOnglets
          tabs={[
            ["liste", "Les maires"],
            ["guide", "Guide"],
            ["donnees", "Mes données"],
            ["campagne", "Ma campagne"],
          ]}
          tab={tab}
          onTab={setTab}
          hrefOf={hrefOf}
        />
        <span className="qui">aucune donnée ne quitte ce navigateur</span>
        <ThemeToggle />
      </header>
      <div className="rayures" aria-hidden="true" />
      <RenderGuard>
        <main id="contenu" tabIndex={-1}>
          <Alerte message={message} onClose={() => setMessage(null)} />
          {/* persistent, like Alerte: a broken ?org= link resolves in the
              same breath as the first render, and the role sits on a
              text-only span with the button beside it */}
          {/* The WHOLE sentence, not a prefix plus a detail: two things
              land here now — a link whose campaign could not be fetched,
              and a link that names one this browser will not replace — and
              a fixed « ne propose aucune campagne » contradicted the
              second. Each writer says what happened. */}
          <p className={offerError ? "alerte erreur" : "sr-only"}>
            <span role="alert">{offerError ?? ""}</span>
            {offerError && (
              <>
                {" "}
                <button
                  type="button"
                  className="lien"
                  onClick={() => {
                    // this button unmounts with the message
                    focusContenu();
                    setOfferError(null);
                  }}
                >
                  fermer
                </button>
              </>
            )}
          </p>
          {/* Pre-exists its text, holds no control while it is empty, and
              is DISMISSIBLE: it states where nine values came from, which is
              worth reading once and not on every visit. */}
          <p className={adopted ? "alerte" : "sr-only"}>
            <span role="status">{adopted ?? ""}</span>
            {adopted && (
              <>
                {" "}
                <button
                  type="button"
                  className="lien"
                  onClick={() => {
                    // this button unmounts with the message
                    focusContenu();
                    setAdopted(null);
                  }}
                >
                  j'ai noté
                </button>
              </>
            )}
          </p>
          {/* persistent region: the start of a download is announced by a
              text CHANGE here, not by the card mounting with its sentence —
              completion is announced by the Alerte above */}
          <span role="status" className="sr-only">
            {download
              ? `Téléchargement de la ${LISTS[download.key].name} en cours.`
              : ""}
          </span>
          {!ready ? (
            <p role="status">Chargement…</p>
          ) : (
            <>
              {/* A ?org= link SAYS a campaign is on offer; it does not go and
              get it. Nothing leaves this browser until the volunteer asks,
              which is what the network tab has to show. */}
              {pendingSlug && !offer && untouchedCampaign(draft) && (
                <section className="carte">
                  <h2>Ce lien propose une campagne</h2>
                  <p>
                    Le lien que vous avez ouvert nomme la campagne «&nbsp;
                    {pendingSlug}&nbsp;». Rien ne lui a encore été demandé :
                    l'afficher enverra une requête à son serveur, la première et
                    la seule.
                  </p>
                  <button
                    type="button"
                    aria-disabled={fetching || undefined}
                    onClick={async () => {
                      if (looking()) return;
                      setFetching(true);
                      // this button unmounts once the offer is in
                      focusContenu();
                      try {
                        setOffer(await fetchCampaign(pendingSlug));
                      } catch (err) {
                        // its own slot: the list download resolves a second
                        // later and overwrote this one, so a broken link
                        // failed in total silence
                        setOfferError(
                          "Ce lien ne propose aucune campagne : " +
                            (err instanceof Error ? err.message : String(err)),
                        );
                        setPendingSlug(null);
                      } finally {
                        setFetching(false);
                        doneLooking();
                      }
                    }}
                  >
                    Voir cette proposition
                  </button>
                </section>
              )}
              {/* the draft, not cfg: the draft covers everything the volunteer
            has — saved (it starts from cfg and follows every writer) or
            still being typed under this very banner */}
              {offer && untouchedCampaign(draft) && (
                <Proposition
                  offer={offer}
                  onRefuse={() => {
                    // the card — and the clicked button — unmounts
                    focusContenu();
                    setOffer(null);
                    // …and the ask that led to it, or refusing the offer put
                    // « Voir cette proposition » straight back on screen
                    setPendingSlug(null);
                    // and it must STAY refused: left in the address bar, ?org=
                    // brought the offer back at every reload
                    const url = new URL(window.location.href);
                    url.searchParams.delete("org");
                    window.history.replaceState({}, "", url);
                  }}
                  onAccept={async () => {
                    // the card — and the clicked button — unmounts
                    focusContenu();
                    if (await adopt(offer, cfg)) {
                      setOffer(null);
                      setMessage({
                        tone: "ok",
                        text:
                          `Campagne « ${offer.name} » reprise. Elle reste dans ce ` +
                          "navigateur, et vous pouvez la modifier dans « Ma campagne ».",
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
                  to the work.

                  TWO SENTENCES, because two different things are missing and
                  only one of them is the reader's to fix. A campaign taken
                  from its own site arrives complete except the two keys that
                  name a PERSON — those are deliberately not adopted — so the
                  unconfigured-campaign wording would send its reader looking
                  for a candidate already on their screen, and would never say
                  that the signature at the bottom is theirs to give. */}
                  {campaignUnfilled.length > 0 ? (
                    <>
                      <strong>Campagne non configurée.</strong> Les messages
                      contiennent encore des valeurs d'exemple :{" "}
                      <strong>n'envoyez rien</strong> avant d'avoir rempli
                      l'onglet « Ma campagne ».
                    </>
                  ) : (
                    <>
                      <strong>Signez de votre nom.</strong> Les textes de la
                      campagne sont là, mais c'est <strong>vous</strong> qui
                      envoyez : renseignez votre nom et votre qualité dans « Ma
                      campagne ». Sans cela les messages partiraient sous un nom
                      d'exemple.
                    </>
                  )}
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
                      // the shared row type carries an optional code; a row
                      // without one is not a card to address
                      if (m.insee_code) navigate(["liste", m.insee_code]);
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
                  phoneOutreach={appelTelephonique}
                  // the campaign's layer, then this volunteer's own overlay,
                  // over the image's — the resolution team mode already
                  // lives by, one layer renamed: this mode has no team, it
                  // has a volunteer.
                  templates={[campaignTemplates, templates]}
                  drafts={cardDrafts}
                  status={tracking[chosen.insee_code as string]?.status}
                  notes={(
                    tracking[chosen.insee_code as string]?.notes ?? []
                  ).map((n) => ({ ...n, volunteer: null }))}
                  // Every note here was written in this browser, by whoever is
                  // reading it: there is no author to tell from a colleague,
                  // and nothing to refuse.
                  noteRights={() => ({ edit: true, delete: true })}
                  onEditNote={(n, i, text) =>
                    reviseNote(chosen.insee_code as string, (insee) =>
                      DB.editNote(insee, i, n, text),
                    )
                  }
                  onDeleteNote={(n, i) =>
                    reviseNote(chosen.insee_code as string, (insee) =>
                      DB.deleteNote(insee, i, n),
                    )
                  }
                  onBack={() => navigate([])}
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
                    setAppelTelephonique(false);
                    setAppelDraft(false);
                    setPersonalNote("");
                    setLogo("");
                    setDraft(EMPTY_CFG);
                    setNoteDraft("");
                    // the store is cleared above; without these the screen
                    // kept rendering the erased campaign's texts until the
                    // next reload
                    setTemplates({});
                    setCampaignTemplates({});
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
                  logo={logo}
                  templates={templates}
                  campaignTemplates={campaignTemplates}
                  onMessage={setMessage}
                  // REFUSED BEFORE IT IS STORED, by the engine itself. There
                  // is no server here to reproduce its rules, so the rules are
                  // asked of the engine directly — against a mayor who does
                  // not exist, at both ranks. Written straight to IndexedDB
                  // once it renders: the card would show the error a moment
                  // later anyway, but « enregistré » followed by a broken card
                  // is a screen that lied first.
                  onTemplates={async (next) => {
                    const stored: M.Templates = {};
                    for (const [file, text] of Object.entries(next)) {
                      if (text.trim() !== "") stored[file] = text;
                    }
                    // judged as it will RENDER: the volunteer's overlay over
                    // the campaign's layer over the image's, because that is
                    // the set the card resolves
                    const why = M.invalidTemplate(
                      M.mergeTemplates(
                        M.SHIPPED_TEMPLATES,
                        campaignTemplates,
                        stored,
                      ),
                    );
                    if (why) throw new Error(why);
                    await DB.writeSetting("modeles", stored);
                    setTemplates(stored);
                    return stored;
                  }}
                  onEdit={setDraft}
                  onNote={setNoteDraft}
                  // Written straight away, not with the form: the file is
                  // already in memory, and a picture chosen then lost to a
                  // tab change is the kind of small betrayal that makes
                  // someone stop trusting the screen. "" removes it.
                  onLogo={async (dataUri) => {
                    try {
                      await DB.writeSetting("logo", dataUri);
                      setLogo(dataUri);
                    } catch (e) {
                      setMessage({
                        tone: "erreur",
                        text: e instanceof Error ? e.message : String(e),
                      });
                    }
                  }}
                  onErreur={(text) => setMessage({ tone: "erreur", text })}
                  appelTelephonique={appelDraft}
                  onAppelTelephonique={setAppelDraft}
                  onSave={async (next, note, appel) => {
                    try {
                      await DB.writeSetting("campagne", next);
                      await DB.writeSetting("argument", note);
                      await DB.writeSetting("appel_telephonique", appel);
                      // cfg only: the draft may already carry keystrokes typed
                      // while these writes are in flight, and they must stay —
                      // the `dirty` marker then reappears on its own
                      setCfg(next);
                      setPersonalNote(note);
                      setAppelTelephonique(appel);
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
            </>
          )}
        </main>
      </RenderGuard>
      {/* The way back, on every screen — the mirror of the link that brought
          them here, which every screen of the account version carries. Read
          from the page at render: an instance says at startup that it serves
          this build, and a static publication says nothing. */}
      <PiedDePage teamUrl={servingInstanceHome()}>
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
