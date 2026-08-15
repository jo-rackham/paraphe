import { marked } from "marked";
import {
  Component,
  type ErrorInfo,
  type ReactNode,
  type RefObject,
  useEffect,
  useRef,
  useState,
} from "react";
import GUIDE from "../../GUIDE.md?raw";
import * as M from "./messages.ts";
import type { Campaign, Mayor, Message, Note } from "./types.ts";

// Vocabulary and components shared by both modes. The card above all: it
// is what calls the message engine, and the rank drives the template
// there. Two copies of this screen would be two occasions to thank someone
// for an endorsement they never made.

// label + chip tone. The tone names a CSS class (`chip-<tone>`) whose dot
// colour is declared per colour scheme — a hex here forced light-mode
// pastels onto the dark theme. The dot is redundant with the label, which
// alone carries the state.
export const STATUSES: Record<string, [string, string]> = {
  to_contact: ["À contacter", "gris"],
  email_sent: ["Email envoyé", "bleu"],
  letter_sent: ["Courrier envoyé", "indigo"],
  to_call_back: ["À rappeler", "ambre"],
  promised: ["Promesse de présentation", "vert"],
  signed: ["A signé (publié par le CC)", "vert-fort"],
  promised_elsewhere: ["Déjà promis à un autre candidat", "orange"],
  refused: ["Refus", "rouge"],
  do_not_contact: ["Ne plus contacter", "encre"],
};

// The data model is English, the screen is French. Rendering a value as it
// comes out of the API tells a volunteer "Rôle : volunteer" — and a status
// or a decision would read no better.
export const ROLES: Record<string, string> = {
  coordination: "Coordination",
  lead: "Référent",
  volunteer: "Bénévole",
  administration: "Administration de l'instance",
};

export const REQUEST_STATES: Record<string, string> = {
  pending: "En attente",
  accepted: "Acceptée",
  refused: "Refusée",
};

export const ORG_STATES: Record<string, string> = {
  active: "Active",
  suspended: "Suspendue",
};

/** A value with no label is shown as is rather than hidden. */
export const label = (table: Record<string, string>, key: string): string =>
  table[key] ?? key;

export const EMPTY_CFG: Campaign = {
  candidat: "Prénom NOM",
  candidat_description: "candidat(e) [courant / démarche], [profession]",
  candidat_description_longue:
    "Je suis [qui]. Je porte [quoi]. Je me présente parce que [pourquoi].",
  signataire: "Prénom Nom",
  signataire_qualite: "équipe de campagne de [candidat]",
  contact_tel: "06 00 00 00 00",
  contact_email: "contact@exemple.fr",
  site: "https://exemple.fr",
  ville_envoi: "Ville",
};

/**
 * The nine campaign fields, described once for both modes: the form, the
 * read-only recap and the "still on template values" banner all read this
 * list. Two copies would drift: they disagree on
 * what ville_envoi does.
 *
 * `group` is what removes the real ambiguity: a volunteer reading a flat list
 * cannot tell which lines describe the candidate and which describe them.
 */
export interface CampaignField {
  key: string;
  group: string;
  label: string;
  example: string;
  hint?: string;
  long?: boolean;
}

export const CAMPAIGN_FIELDS: CampaignField[] = [
  {
    key: "candidat",
    group: "Le candidat ou la candidate",
    label: "Son nom",
    example: "Marie Dupont",
  },
  {
    key: "candidat_description",
    group: "Le candidat ou la candidate",
    label: "Qui c'est, en une ligne",
    example: "médecin de campagne, engagée pour l'accès aux soins",
    hint:
      "Se lit à l'intérieur de la phrase : « au nom de Marie Dupont, " +
      "médecin de campagne…, qui sollicite les présentations pour 2027 ». " +
      "C'est la seule ligne qui dit au maire de qui il s'agit.",
  },
  {
    key: "candidat_description_longue",
    group: "Le candidat ou la candidate",
    label: "Sa présentation en deux ou trois phrases",
    example: "Je suis médecin depuis vingt ans dans le Cantal…",
    hint: "À la première personne. N'apparaît que dans le courrier.",
    long: true,
  },
  {
    key: "signataire",
    group: "Vous, qui écrivez",
    label: "Votre nom",
    example: "Camille Martin",
  },
  {
    key: "signataire_qualite",
    group: "Vous, qui écrivez",
    label: "En quelle qualité",
    example: "bénévole de la campagne",
  },
  {
    key: "contact_tel",
    group: "Vos coordonnées, au bas de chaque message",
    label: "Téléphone",
    example: "06 12 34 56 78",
  },
  {
    key: "contact_email",
    group: "Vos coordonnées, au bas de chaque message",
    label: "Email",
    example: "camille.martin@exemple.fr",
  },
  {
    key: "site",
    group: "Vos coordonnées, au bas de chaque message",
    label: "Site de la campagne",
    example: "https://exemple.fr",
  },
  {
    key: "ville_envoi",
    group: "Le courrier papier",
    label: "Ville d'où vous écrivez",
    example: "Rodez",
    hint: "Compose l'en-tête du courrier : « Rodez, le 9 août 2026 ».",
  },
];

export const campaignLabel = (key: string): string =>
  CAMPAIGN_FIELDS.find((f) => f.key === key)?.label ?? key;

/**
 * The nine campaign fields, rendered once for both modes. A field still on
 * its template value SAYS so, beside it: the shipped values look filled
 * ("Prénom NOM"), and the banner alone never said which lines were the
 * trap. The predicate is the engine's own `unfilledKeys` — the same
 * normalisation that decides whether the mass mailing refuses to run.
 *
 * `groupe`: the heading level of the group titles — h2 right under the
 * browser tab's h1, h3 under the team card's h2. The hierarchy has no
 * level to skip.
 */
export function ChampsCampagne({
  values,
  onEdit,
  groupe: Groupe,
}: {
  values: Record<string, string>;
  onEdit: (key: string, value: string) => void;
  groupe: "h2" | "h3";
}) {
  const unfilled = new Set(M.unfilledKeys(values));
  return (
    <>
      {CAMPAIGN_FIELDS.map((f, i) => {
        const example = unfilled.has(f.key);
        const described =
          [
            f.hint ? `champ-${f.key}-aide` : "",
            example ? `champ-${f.key}-exemple` : "",
          ]
            .filter(Boolean)
            .join(" ") || undefined;
        return (
          <div key={f.key}>
            {f.group !== CAMPAIGN_FIELDS[i - 1]?.group && (
              <Groupe className="groupe">{f.group}</Groupe>
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
                  className={example ? "exemple" : undefined}
                  aria-describedby={described}
                  value={values[f.key] ?? ""}
                  onChange={(e) => onEdit(f.key, e.target.value)}
                />
              ) : (
                <input
                  id={`champ-${f.key}`}
                  type="text"
                  placeholder={f.example}
                  className={example ? "exemple" : undefined}
                  aria-describedby={described}
                  value={values[f.key] ?? ""}
                  onChange={(e) => onEdit(f.key, e.target.value)}
                />
              )}
              {example && (
                <span className="gris aide" id={`champ-${f.key}-exemple`}>
                  <strong>Valeur d'exemple</strong> — remplacez-la : elle
                  partirait telle quelle dans les messages.
                </span>
              )}
              {f.hint && (
                <span className="gris aide" id={`champ-${f.key}-aide`}>
                  {f.hint}
                </span>
              )}
            </p>
          </div>
        );
      })}
    </>
  );
}

/**
 * Decorative pictogram (emoji, arrow): hidden from assistive technology —
 * the text beside it carries the meaning, and a screen reader saying
 * "enveloppe" before every button label is noise, not information.
 */
export function Emoji({ children }: { children: ReactNode }) {
  return <span aria-hidden="true">{children}</span>;
}

/** First focusable element of every page; its target is `<main id="contenu">`. */
export function SkipLink() {
  return (
    <a className="skip-link" href="#contenu">
      Aller au contenu
    </a>
  );
}

/**
 * The header's tab strip, one `<nav>` landmark: the active tab is carried
 * by `aria-current` (styled from the attribute — state and appearance
 * cannot disagree), which is also what announces it to a screen reader.
 */
export function NavOnglets({
  tabs,
  tab,
  onTab,
}: {
  tabs: [string, string][];
  tab: string;
  onTab: (key: string) => void;
}) {
  return (
    <nav aria-label="Navigation principale">
      {tabs.map(([key, name]) => (
        <button
          type="button"
          key={key}
          className="lien"
          aria-current={tab === key ? "page" : undefined}
          onClick={() => onTab(key)}
        >
          {name}
        </button>
      ))}
    </nav>
  );
}

/**
 * SPA view change: the clicked control often unmounts with the old view,
 * and keyboard or screen-reader focus silently falls back to the top of
 * the document. When `key` changes (never on the first view), the new
 * view's h1 takes focus, and the document title says where the volunteer
 * landed. `title: null` marks a transient screen (loading) that is not a
 * view at all.
 */
export function useViewFocus(key: string, title: string | null) {
  const shown = useRef<string | null>(null);
  useEffect(() => {
    if (title === null) return;
    document.title = `${title} — paraphe`;
    if (shown.current === key) return;
    const first = shown.current === null;
    shown.current = key;
    if (first) return; // page load: the browser's own focus is right
    const h = document.querySelector<HTMLElement>("main h1");
    if (h) {
      h.setAttribute("tabindex", "-1");
      // dropped once focus moves on: the attribute would otherwise stick
      // to an h1 reused across views, leaving it click-focusable for good
      h.addEventListener("blur", () => h.removeAttribute("tabindex"), {
        once: true,
      });
      h.focus();
    }
  }, [key, title]);
}

export function Hexagone() {
  return (
    <svg width="26" height="29" viewBox="0 0 26 29" aria-hidden="true">
      <path
        d="M13 1 24.3 7.5v13L13 27 1.7 20.5v-13z"
        fill="none"
        stroke="#ffd400"
        strokeWidth="2.2"
        strokeLinejoin="round"
      />
      <rect x="7" y="12.2" width="4" height="3.6" fill="#000091" />
      <rect x="11" y="12.2" width="4" height="3.6" fill="#ffffff" />
      <rect x="15" y="12.2" width="4" height="3.6" fill="#e1000f" />
    </svg>
  );
}

/**
 * Without this boundary, a rendering defect unmounts the whole React
 * tree: white screen, no message, and reloading reproduces it. A
 * volunteer has no way to understand that — they must at least be told
 * what to do.
 */
export class RenderGuard extends Component<
  { children: ReactNode },
  { error: Error | null }
> {
  constructor(props: { children: ReactNode }) {
    super(props);
    this.state = { error: null };
  }

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("screen failed:", error, info.componentStack);
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <main>
        <h1>Cet écran n'a pas pu s'afficher</h1>
        <p className="alerte erreur">{this.state.error.message}</p>
        <p>
          Votre travail n'est pas perdu. Revenez à la liste, ou rechargez la
          page ; si l'écran revient, signalez-le à la coordination avec le nom
          de la commune concernée.
        </p>
        <p>
          <button type="button" onClick={() => this.setState({ error: null })}>
            Continuer
          </button>
        </p>
      </main>
    );
  }
}

export function Chip({ status }: { status: string }) {
  // A status this version does not know is shown AS IS. Falling back to
  // "À contacter" asserted the opposite of a promise of presentation — the
  // one thing the campaign counts — and sent the volunteer back to someone
  // who had already said yes.
  const known = STATUSES[status];
  if (!known) {
    return (
      <span
        className="chip chip-inconnu"
        title="Statut inconnu de cette version de l'application"
      >
        {status} <Emoji>⚠</Emoji>
        {/* the title attribute never reaches a keyboard or touch user */}
        <span className="sr-only">
          {" "}
          (statut inconnu de cette version de l'application)
        </span>
      </span>
    );
  }
  const [label, tone] = known;
  return <span className={`chip chip-${tone}`}>{label}</span>;
}

/**
 * One row of a mayor list, shared by the three mayor tables (browser list,
 * server list, dashboard). Under 640 px the row lays out as a stacked card
 * (`table.maires`), and a changed display strips the implicit table
 * semantics — the explicit roles below keep them for assistive technology.
 */
export function LigneMaire({
  m,
  status,
  volunteer,
  onOpen,
}: {
  m: Mayor;
  status?: string | null;
  /** Team mode: who reserved the card, shown under its status. */
  volunteer?: string | null;
  onOpen: (m: Mayor) => void;
}) {
  // biome-ignore-start lint/a11y/noRedundantRoles: kept on purpose — display:grid at narrow widths strips the implicit roles
  return (
    <tr role="row">
      <td role="cell">
        <button type="button" className="lien" onClick={() => onOpen(m)}>
          <strong>{m.commune}</strong>
        </button>
        <br />
        <span className="gris">
          {m.title} {m.first_name} {m.last_name}
        </span>
      </td>
      <td role="cell" className="departement">
        {m.department}
      </td>
      <td role="cell" className="gris">
        {M.rank(m) === "has_endorsed"
          ? `${m.recent_candidate} (${m.recent_year})`
          : M.RANKS[M.rank(m)]}
      </td>
      <td role="cell">
        <Chip status={status ?? "to_contact"} />
        {volunteer && (
          <>
            <br />
            <span className="gris">{volunteer}</span>
          </>
        )}
      </td>
    </tr>
  );
  // biome-ignore-end lint/a11y/noRedundantRoles: single suppression site
}

/** The mayor table shell around `LigneMaire` rows — one copy of the four
 * column headers, and of the roles that survive the card layout. */
export function TableMaires({ children }: { children: ReactNode }) {
  // biome-ignore-start lint/a11y/noRedundantRoles: kept on purpose — display changes at narrow widths strip the implicit roles
  return (
    <table className="maires" role="table">
      <thead role="rowgroup">
        <tr role="row">
          <th scope="col" role="columnheader">
            Commune
          </th>
          <th scope="col" role="columnheader">
            Département
          </th>
          <th scope="col" role="columnheader">
            Signal
          </th>
          <th scope="col" role="columnheader">
            Statut
          </th>
        </tr>
      </thead>
      <tbody role="rowgroup">{children}</tbody>
    </table>
  );
  // biome-ignore-end lint/a11y/noRedundantRoles: single suppression site
}

export function PiedDePage({
  children,
  sourceUrl,
}: {
  children?: ReactNode;
  sourceUrl?: string;
}) {
  return (
    <footer>
      <p className="officiel">
        <strong>Site non officiel.</strong> Initiative citoyenne indépendante,
        sans lien avec le Conseil constitutionnel, le ministère de l'Intérieur
        ni aucune administration.
      </p>
      {children}
      {sourceUrl && (
        <p>
          <a href={sourceUrl} rel="noreferrer">
            Code source
          </a>{" "}
          — logiciel libre.
        </p>
      )}
    </footer>
  );
}

export function Alerte({
  message,
  onClose,
}: {
  message?: Message | null;
  onClose?: () => void;
}) {
  // Both regions live in the tree from the FIRST render, empty: assistive
  // technology reliably announces what changes inside an existing live
  // region, not a region inserted together with its text. The role sits on
  // a span holding the text alone — the "fermer" button stays outside it,
  // an interactive control inside a live region being re-read on every
  // mutation. One paragraph per role, since a role must not change either.
  const error = message?.tone === "erreur" ? message : null;
  const ok = message && message.tone !== "erreur" ? message : null;
  // A success confirms and may leave on its own; an error is the only word
  // about a failed write and stays until acted on. The live region spoke
  // when the text arrived, so removing it later loses nothing. `onClose`
  // goes through a ref, and the timer is keyed on the message's CONTENT:
  // parents pass fresh closures — and may pass fresh objects — on every
  // render, and depending on either would rearm the timer for ever.
  const close = useRef(onClose);
  close.current = onClose;
  const okKey = ok ? `${ok.tone}\n${ok.text}` : null;
  useEffect(() => {
    if (okKey === null) return undefined;
    const timer = setTimeout(() => close.current?.(), 7000);
    return () => clearTimeout(timer);
  }, [okKey]);
  const fermer = onClose && (
    <>
      {" "}
      <button type="button" className="lien" onClick={onClose}>
        fermer
      </button>
    </>
  );
  return (
    <>
      <p className={error ? "alerte erreur" : "sr-only"}>
        <span role="alert">{error?.text}</span>
        {error && fermer}
      </p>
      <p className={ok ? "alerte" : "sr-only"}>
        <span role="status">{ok?.text}</span>
        {ok && fermer}
      </p>
    </>
  );
}

/**
 * "N affiché(s) sur T" — visible at once, SPOKEN only when the number
 * settles: announced on every keystroke, a polite region turns a search
 * into a stream of numbers.
 */
export function CompteurResultats({
  shown,
  total,
}: {
  shown: number;
  total: number;
}) {
  const line = `${shown} affiché(s) sur ${total}.`;
  const [announced, setAnnounced] = useState("");
  useEffect(() => {
    const timer = setTimeout(() => setAnnounced(line), 600);
    return () => clearTimeout(timer);
  }, [line]);
  return (
    <>
      <p className="gris">{line}</p>
      <span role="status" className="sr-only">
        {announced}
      </span>
    </>
  );
}

// GUIDE.md is a repository file, not user input: rendering it to HTML
// gives nobody a hold on the page.
const GUIDE_HTML = marked.parse(GUIDE) as string;

export function Guide() {
  return (
    // GUIDE_HTML is GUIDE.md of this repository, imported with ?raw at BUILD
    // time and rendered by marked. No request, no user input and no third
    // party reaches it — the guide has ONE source, shared by the interface
    // and the mass mailing, and that is the point.
    <div
      className="carte guide"
      // biome-ignore lint/security/noDangerouslySetInnerHtml: repository content, inlined at build time
      dangerouslySetInnerHTML={{ __html: GUIDE_HTML }}
    />
  );
}

/**
 * A mayor's card: why them, how to reach them, what to write to them.
 * `notes`: shared history (team mode) or local (browser mode).
 * `onStatus(status, note)` may throw — the message is shown as is, which
 * is how an allocation conflict reaches the volunteer.
 */
/**
 * Unsent work on a card: the rewritten email, the note being typed during
 * a call. It is text addressed to a NAMED mayor — worth more than the
 * campaign draft that already survives tab clicks.
 */
export interface CardDraft {
  subject: string;
  body: string;
  note: string;
  /**
   * The PRISTINE RENDER this text is a rewrite of. Listing what the render
   * derives from misses a field every time: keying
   * on the INSEE alone revived a predecessor's letter on his successor's
   * card, and adding the mayor's identity still missed the rank — a list
   * rebuilt with a corrected false positive left « vous avez présenté »
   * armed in the mailto while the screen beside it announced a discovery
   * message. The render IS the derivation; nothing can fall out of it.
   */
  basis: string;
  /** The mayor's identity: the call note derives from nothing else. */
  who: string;
  /** The email differs from its pristine render. */
  touched: boolean;
}

const cardWho = (mayor: Mayor) =>
  `${mayor.insee_code}|${mayor.last_name}|${mayor.first_name}`;

/**
 * The ONE number a tel: link may dial, or "" for "render text only".
 * Directory strings can hold two numbers ("04 … / 06 …"), an extension
 * ("poste 25") or junk; stripped blindly they concatenate into a number
 * nobody meant — and any ten-digit prefix of that is somebody's real
 * phone. The first plausible run of digits is the number; outside 6-15
 * digits (E.164 ceiling, short overseas floors) there is no link at all.
 */
const dialable = (phone: string): string => {
  const run = phone.match(/\+?\d[\d\s.()-]*/)?.[0] ?? "";
  const digits = run.replace(/[^+\d]/g, "");
  const length = digits.replace("+", "").length;
  return length >= 6 && length <= 15 ? digits : "";
};

export interface CardProps {
  mayor: Mayor;
  cfg: Campaign;
  personalNote?: string;
  /**
   * Who signs the email and the phone script. In team mode this is the
   * volunteer: they send from their OWN address (GUIDE.md, the rule that
   * keeps the campaign out of the spam folder), so a message signed by the
   * campaign's single signatory arrives from one name under another. The
   * letter is unaffected — it is signed by the candidate.
   */
  signer?: string;
  status?: string | null;
  notes?: Note[];
  onBack: () => void;
  onStatus: (status: string, note: string) => void | Promise<void>;
  /** Team-mode banner: who reserved the card. */
  header?: ReactNode;
  /**
   * Draft store held by the PARENT, keyed by INSEE: the card is unmounted
   * by any tab click, and its unsent text must survive a look at the
   * Guide. An entry taken under another campaign object is discarded —
   * the same predicate that resets the fields below.
   */
  drafts?: RefObject<Record<string, CardDraft>>;
}

export function Fiche({
  mayor,
  cfg,
  personalNote,
  signer,
  status: initialStatus,
  notes = [],
  onBack,
  onStatus,
  header,
  drafts,
}: CardProps) {
  const [status, setStatus] = useState(initialStatus ?? "to_contact");
  const [statusError, setStatusError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);

  let rendered: {
    subject: string;
    body: string;
    letter: string;
    phone: string;
    address: string;
    letterHead: string;
  } | null = null;
  let error: string | null = null;
  try {
    rendered = {
      ...M.email(mayor, cfg, { personalNote, signer }),
      letter: M.letter(mayor, cfg, { personalNote }),
      phone: M.phoneScript(mayor, cfg, { personalNote, signer }),
      address: M.recipientAddress(mayor),
      letterHead: M.letterHeader(mayor, cfg),
    };
  } catch (e) {
    error = e instanceof Error ? e.message : String(e);
  }
  const { valid } = M.emailAddresses(mayor);
  const badAddress = M.incompleteAddress(mayor);
  // the SAME normalisation as the engine: the screen announced a
  // "discovery message" while the send button carries a thank-you
  const rank = M.rank(mayor);

  // The email fields are CONTROLLED, and reset when the card changes — or
  // the CAMPAIGN does. Uncontrolled, the "Copier" button read the edited
  // DOM while the mailto link kept the pristine text; keyed on the mayor
  // alone, adopting an offered campaign under an open card left the mailto
  // armed with template values while the letter showed the real candidate.
  // Seeded from the parent's draft store when it holds unsent work taken
  // under this same BASIS — same person, same campaign, same touch, same
  // signer, compared by value: an identity compare threw a kept draft
  // away on every reconnection (a fresh config object with equal values)
  // and would miss a personal touch written after the card is first opened.
  const pristine = {
    subject: rendered?.subject ?? "",
    body: rendered?.body ?? "",
  };
  // The render, PLUS the identity it is addressed to. No email template
  // carries the mayor's name — {salutation} is a gender and {commune_de} a
  // place — so two successors at the same INSEE render identically, and
  // the render alone would hand one's rewrite to the other.
  const basis = JSON.stringify({ ...pristine, error, who: cardWho(mayor) });
  const who = cardWho(mayor);
  const kept = () => drafts?.current[mayor.insee_code as string];
  // The email follows the render it is a rewrite of; the note follows
  // the PERSON, since it derives from nothing else — saving an unrelated
  // personal touch would throw away a call note taken minutes earlier.
  const freshEmail = () => {
    const k = kept();
    return k && k.basis === basis
      ? { subject: k.subject, body: k.body }
      : pristine;
  };
  const freshNote = () => {
    const k = kept();
    return k && k.who === who ? k.note : "";
  };
  const [subject, setSubject] = useState(() => freshEmail().subject);
  const [body, setBody] = useState(() => freshEmail().body);
  const [note, setNote] = useState(() => freshNote());
  // evaluated at MOUNT too, not only on a change under an open card: the
  // card is unmounted by the very tab click that goes and reloads the list
  const discarded = () => {
    const k = kept();
    return !!k && k.touched && k.who === who && k.basis !== basis;
  };
  const [regenerated, setRegenerated] = useState(discarded);
  const shown = useRef({ basis, who });
  if (shown.current.basis !== basis || shown.current.who !== who) {
    // a rewrite that cannot be kept is a loss: say so rather than swap
    // the text under the volunteer without a word
    setRegenerated(discarded());
    shown.current = { basis, who };
    const e = freshEmail();
    setSubject(e.subject);
    setBody(e.body);
    setNote(freshNote());
  }
  // mirrored on every change: the ref outlives the unmount a tab click
  // causes, so the rewritten text and the call note are still there when
  // the volunteer comes back
  useEffect(() => {
    if (drafts) {
      drafts.current[mayor.insee_code as string] = {
        subject,
        body,
        note,
        basis,
        who,
        touched: subject !== pristine.subject || body !== pristine.body,
      };
    }
  });

  const save = async () => {
    setStatusError(null);
    setSaved(false);
    setSaving(true);
    try {
      await onStatus(status, note);
      setNote("");
      setSaved(true);
    } catch (e) {
      setStatusError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <p>
        <button type="button" className="lien" onClick={onBack}>
          <Emoji>← </Emoji>retour à la liste
        </button>
      </p>
      <h1>
        {mayor.title} {mayor.first_name} {mayor.last_name}
      </h1>
      <p>
        <strong>{mayor.commune}</strong> ({mayor.department})
      </p>
      {header}

      <div className="carte">
        <p className="pourquoi">
          <strong>Pourquoi cette personne :</strong>{" "}
          {rank === "has_endorsed" ? (
            `a parrainé ${
              M.readableHistory(mayor.endorsement_history ?? "") ||
              mayor.recent_candidate
            }`
          ) : rank === "commune_has_endorsed" ? (
            <>
              sa commune l'a fait sous {M.proseName(mayor.predecessor ?? "")} —{" "}
              <span className="gris">
                lui/elle n'a rien parrainé : ne le remerciez de rien
              </span>
            </>
          ) : (
            <span className="gris">
              aucun historique connu — message de découverte
            </span>
          )}
        </p>
        <p className="grand-tel">
          <Emoji>☎ </Emoji>
          <span className="sr-only">Téléphone : </span>
          {/* the display keeps the printed string whole; the link exists
              only when ONE dialable number could be read out of it */}
          {mayor.phone && dialable(mayor.phone) ? (
            <a href={`tel:${dialable(mayor.phone)}`}>{mayor.phone}</a>
          ) : (
            mayor.phone || "non renseigné"
          )}
        </p>
        <p style={{ margin: ".2rem 0" }}>
          <strong>Ouverture :</strong>{" "}
          {mayor.town_hall_hours || "non renseigné"}
        </p>
        <p style={{ margin: ".2rem 0" }}>
          <strong>Email :</strong> {mayor.email || "non renseigné"}
        </p>
      </div>

      {error ? (
        <p className="alerte erreur">Message non générable : {error}</p>
      ) : (
        <>
          <details open>
            <summary>
              <Emoji>✉️ </Emoji>Email
            </summary>
            <div className="dedans">
              {regenerated && (
                <p className="alerte">
                  <strong>Message régénéré.</strong> La campagne ou les
                  informations de ce maire ont changé depuis votre réécriture :
                  le texte ci-dessous a été reconstruit, et ce que vous aviez
                  écrit n'a pas été conservé.
                </p>
              )}
              {valid.length === 0 && (
                <p className="alerte">
                  Aucune adresse exploitable — passez par le courrier ou le
                  téléphone.
                </p>
              )}
              <p>
                <label>
                  Objet
                  <input
                    type="text"
                    value={subject}
                    onChange={(e) => {
                      setSubject(e.target.value);
                      setRegenerated(false);
                    }}
                  />
                </label>
              </p>
              <p>
                <label>
                  Message
                  <textarea
                    rows={16}
                    value={body}
                    onChange={(e) => {
                      setBody(e.target.value);
                      setRegenerated(false);
                    }}
                  />
                </label>
              </p>
              <p>
                <button
                  type="button"
                  onClick={() => {
                    navigator.clipboard.writeText(`${subject}\n\n${body}`);
                  }}
                >
                  <Emoji>📋 </Emoji>Copier
                </button>{" "}
                {valid.length > 0 && (
                  <a
                    className="bouton secondaire"
                    href={
                      `mailto:${encodeURIComponent(valid[0])}` +
                      `?subject=${encodeURIComponent(subject)}` +
                      `&body=${encodeURIComponent(body)}`
                    }
                  >
                    <Emoji>✉️ </Emoji>Ouvrir dans ma messagerie
                  </a>
                )}
              </p>
            </div>
          </details>

          <details>
            <summary>
              <Emoji>📮 </Emoji>Courrier
            </summary>
            <div className="dedans">
              {badAddress && (
                <p className="alerte">Adresse inutilisable : {badAddress}.</p>
              )}
              <pre className="lettre">
                {rendered!.address}
                {"\n\n"}
                {rendered!.letterHead}
                {"\n\n"}
                {rendered!.letter}
              </pre>
              <button type="button" onClick={() => window.print()}>
                <Emoji>🖨️ </Emoji>Imprimer
              </button>
            </div>
          </details>

          <details>
            <summary>
              <Emoji>☎️ </Emoji>Téléphone
            </summary>
            <div className="dedans">
              <pre>{rendered!.phone}</pre>
            </div>
          </details>
        </>
      )}

      {notes.length > 0 && (
        <div className="carte">
          <h2 style={{ marginTop: 0 }}>Historique</h2>
          {/* The history is append-only and rendered whole, in order:
              nothing is inserted, removed or reordered, which is the case
              the rule exists for. Two notes can share a timestamp, so the
              index is the only key here that is actually unique. */}
          {notes.map((n, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: append-only, never reordered
            <div className="note-item" key={i}>
              <span className="gris">
                {n.ts} → {(STATUSES[n.status] ?? ["?"])[0]}
                {n.volunteer ? ` — ${n.volunteer}` : ""}
              </span>
              {n.note && (
                <>
                  <br />
                  {n.note}
                </>
              )}
            </div>
          ))}
        </div>
      )}

      {/* Sticky: the outcome of a contact is recorded WHILE on the phone,
          and these controls sat below sixteen rows of email. Last in the
          flow, pinned to the viewport bottom until scrolled to — same
          controls, same state, nothing is duplicated. */}
      <section className="barre-statut" aria-label="Après le contact">
        <Alerte
          message={statusError ? { tone: "erreur", text: statusError } : null}
        />
        <div className="champs">
          <label>
            Statut
            <select value={status} onChange={(e) => setStatus(e.target.value)}>
              {Object.entries(STATUSES).map(([k, [l]]) => (
                <option key={k} value={k}>
                  {l}
                </option>
              ))}
            </select>
          </label>
          <label className="note">
            Note
            <textarea
              rows={2}
              value={note}
              onChange={(e) => setNote(e.target.value)}
            />
          </label>
          <button type="button" onClick={save} disabled={saving}>
            {saving ? "Enregistrement…" : "Enregistrer"}
          </button>{" "}
          {/* always in the tree: a live region announces reliably only when
              its CONTENT changes, not when it appears with it */}
          <span role="status" className="gris">
            {saved ? "Enregistré." : ""}
          </span>
        </div>
      </section>
    </>
  );
}
