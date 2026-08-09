import {
  Component, useEffect, useRef, useState,
  type ErrorInfo, type ReactNode, type RefObject,
} from "react";
import { marked } from "marked";
import * as M from "./messages.ts";
import GUIDE from "../../GUIDE.md?raw";
import type { Campaign, Mayor, Message, Note } from "./types.ts";

// Vocabulary and components shared by both modes. The card above all: it
// is what calls the message engine, and the rank drives the template
// there. Two copies of this screen would be two occasions to thank someone
// for an endorsement they never made.

export const STATUSES: Record<string, [string, string]> = {
  to_contact: ["À contacter", "#e2e8f0"],
  email_sent: ["Email envoyé", "#bfdbfe"],
  letter_sent: ["Courrier envoyé", "#c7d2fe"],
  to_call_back: ["À rappeler", "#fde68a"],
  promised: ["Promesse de présentation", "#bbf7d0"],
  signed: ["A signé (publié par le CC)", "#86efac"],
  promised_elsewhere: ["Déjà promis à un autre candidat", "#fed7aa"],
  refused: ["Refus", "#fecaca"],
  do_not_contact: ["Ne plus contacter", "#e5e5e5"],
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
 * list. Two copies used to exist and had already drifted — they disagreed on
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
    hint: "Se lit à l'intérieur de la phrase : « au nom de Marie Dupont, "
      + "médecin de campagne…, qui sollicite les présentations pour 2027 ». "
      + "C'est la seule ligne qui dit au maire de qui il s'agit.",
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

export function Hexagone() {
  return (
    <svg width="26" height="29" viewBox="0 0 26 29" aria-hidden="true">
      <path d="M13 1 24.3 7.5v13L13 27 1.7 20.5v-13z" fill="none"
        stroke="#ffd400" strokeWidth="2.2" strokeLinejoin="round" />
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
  { children: ReactNode }, { error: Error | null }
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
        <p><button onClick={() => this.setState({ error: null })}>Continuer</button></p>
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
      <span className="chip" style={{ background: "#fef08a" }}
        title="Statut inconnu de cette version de l'application">{status} ⚠</span>
    );
  }
  const [label, colour] = known;
  return <span className="chip" style={{ background: colour }}>{label}</span>;
}

export function PiedDePage(
  { children, sourceUrl }: { children?: ReactNode; sourceUrl?: string },
) {
  return (
    <footer>
      <p className="officiel">
        <strong>Site non officiel.</strong> Initiative citoyenne indépendante,
        sans lien avec le Conseil constitutionnel, le ministère de l'Intérieur
        ni aucune administration.
      </p>
      {children}
      {sourceUrl && (
        <p><a href={sourceUrl} rel="noreferrer">Code source</a> — logiciel libre.</p>
      )}
    </footer>
  );
}

export function Alerte(
  { message, onClose }: { message?: Message | null; onClose?: () => void },
) {
  if (!message) return null;
  return (
    <p className={message.tone === "erreur" ? "alerte erreur" : "alerte"}>
      {message.text}{" "}
      {onClose && <button className="lien" onClick={onClose}>fermer</button>}
    </p>
  );
}

// GUIDE.md is a repository file, not user input: rendering it to HTML
// gives nobody a hold on the page.
const GUIDE_HTML = marked.parse(GUIDE) as string;

export function Guide() {
  return (
    <div className="carte guide"
      dangerouslySetInnerHTML={{ __html: GUIDE_HTML }} />
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
   * The PRISTINE RENDER this text was a rewrite of. Listing what the
   * render derives from was tried and missed a field every time: keying
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

export function Fiche({ mayor, cfg, personalNote, signer, status: initialStatus,
  notes = [], onBack, onStatus, header, drafts }: CardProps) {
  const [status, setStatus] = useState(initialStatus ?? "to_contact");
  const [statusError, setStatusError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  let rendered: {
    subject: string; body: string; letter: string; phone: string;
    address: string; letterHead: string;
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
  // "discovery message" while the send button was armed with a thank-you
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
  // and missed a personal touch written after the card was first opened.
  const pristine = { subject: rendered?.subject ?? "", body: rendered?.body ?? "" };
  // The render, PLUS the identity it is addressed to. No email template
  // carries the mayor's name — {salutation} is a gender and {commune_de} a
  // place — so two successors at the same INSEE render identically, and
  // the render alone would hand one's rewrite to the other.
  const basis = JSON.stringify({ ...pristine, error, who: cardWho(mayor) });
  const who = cardWho(mayor);
  const kept = () => drafts?.current[mayor.insee_code as string];
  // The email follows the render it was a rewrite of; the note follows
  // the PERSON, since it derives from nothing else — saving an unrelated
  // personal touch used to throw away a call note taken minutes earlier.
  const freshEmail = () => {
    const k = kept();
    return k && k.basis === basis ? { subject: k.subject, body: k.body } : pristine;
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
        subject, body, note, basis, who,
        touched: subject !== pristine.subject || body !== pristine.body,
      };
    }
  });

  const save = async () => {
    setStatusError(null);
    setSaving(true);
    try {
      await onStatus(status, note);
      setNote("");
    } catch (e) {
      setStatusError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <p><button className="lien" onClick={onBack}>← retour à la liste</button></p>
      <h1>{mayor.title} {mayor.first_name} {mayor.last_name}</h1>
      <p><strong>{mayor.commune}</strong> ({mayor.department})</p>
      {header}

      <div className="carte">
        <p style={{ margin: ".2rem 0" }}>
          <strong>Pourquoi cette personne :</strong>{" "}
          {rank === "has_endorsed"
            ? `a parrainé ${M.readableHistory(mayor.endorsement_history ?? "")
              || mayor.recent_candidate}`
            : rank === "commune_has_endorsed"
              ? <>sa commune l'a fait sous {M.proseName(mayor.predecessor ?? "")} —{" "}
                <span className="gris">lui/elle n'a rien parrainé : ne le
                  remerciez de rien</span></>
              : <span className="gris">aucun historique connu — message de
                découverte</span>}
        </p>
        <p className="grand-tel">☎ {mayor.phone || "non renseigné"}</p>
        <p style={{ margin: ".2rem 0" }}>
          <strong>Ouverture :</strong> {mayor.town_hall_hours || "non renseigné"}
        </p>
        <p style={{ margin: ".2rem 0" }}>
          <strong>Email :</strong> {mayor.email || "non renseigné"}
        </p>
      </div>

      {error
        ? <p className="alerte erreur">Message non générable : {error}</p>
        : (
          <>
            <details open>
              <summary>✉️ Email</summary>
              <div className="dedans">
                {regenerated && (
                  <p className="alerte">
                    <strong>Message régénéré.</strong> La campagne ou les
                    informations de ce maire ont changé depuis votre
                    réécriture : le texte ci-dessous a été reconstruit, et ce
                    que vous aviez écrit n'a pas été conservé.
                  </p>
                )}
                {valid.length === 0 && (
                  <p className="alerte">Aucune adresse exploitable — passez par
                    le courrier ou le téléphone.</p>
                )}
                <p><label>Objet
                  <input type="text" value={subject}
                    onChange={(e) => { setSubject(e.target.value); setRegenerated(false); }} />
                </label></p>
                <p><label>Message
                  <textarea rows={16} value={body}
                    onChange={(e) => { setBody(e.target.value); setRegenerated(false); }} />
                </label></p>
                <p>
                  <button onClick={() => {
                    navigator.clipboard.writeText(`${subject}\n\n${body}`);
                  }}>📋 Copier</button>{" "}
                  {valid.length > 0 && (
                    <a className="bouton secondaire" href={
                      `mailto:${encodeURIComponent(valid[0])}`
                      + `?subject=${encodeURIComponent(subject)}`
                      + `&body=${encodeURIComponent(body)}`
                    }>✉️ Ouvrir dans ma messagerie</a>
                  )}
                </p>
              </div>
            </details>

            <details>
              <summary>📮 Courrier</summary>
              <div className="dedans">
                {badAddress && <p className="alerte">Adresse inutilisable : {badAddress}.</p>}
                <pre className="lettre">{rendered!.address}{"\n\n"}{rendered!.letterHead}{"\n\n"}{rendered!.letter}</pre>
                <button onClick={() => window.print()}>🖨️ Imprimer</button>
              </div>
            </details>

            <details>
              <summary>☎️ Téléphone</summary>
              <div className="dedans"><pre>{rendered!.phone}</pre></div>
            </details>
          </>
        )}

      <div className="carte">
        <h2 style={{ marginTop: 0 }}>Après le contact</h2>
        <Alerte message={statusError ? { tone: "erreur", text: statusError } : null} />
        <p>
          <label>Statut
            <select value={status} onChange={(e) => setStatus(e.target.value)}>
              {Object.entries(STATUSES).map(([k, [l]]) => (
                <option key={k} value={k}>{l}</option>
              ))}
            </select>
          </label>
        </p>
        <p><label>Note
          <textarea rows={3} value={note} onChange={(e) => setNote(e.target.value)} />
        </label></p>
        <button onClick={save} disabled={saving}>
          {saving ? "Enregistrement…" : "Enregistrer"}
        </button>
        {notes.length > 0 && (
          <>
            <h2>Historique</h2>
            {notes.map((n, i) => (
              <div className="note-item" key={i}>
                <span className="gris">
                  {n.ts} → {(STATUSES[n.status] ?? ["?"])[0]}
                  {n.volunteer ? ` — ${n.volunteer}` : ""}
                </span>
                {n.note && <><br />{n.note}</>}
              </div>
            ))}
          </>
        )}
      </div>
    </>
  );
}
