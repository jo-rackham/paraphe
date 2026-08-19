import { ChampLogo, ChampsCampagne } from "./common.tsx";
import { ModelesMessages } from "./Templates.tsx";
import type { Campaign, Message, Templates } from "./types.ts";

// Fully controlled: the draft state lives in Browser (this tab is
// unmounted by every tab switch, and typed work must survive one).
interface CampaignTabProps {
  draft: Campaign;
  note: string;
  dirty: boolean;
  /** the logo as a data URI, "" for none — stored, not drafted */
  logo: string;
  onEdit: (draft: Campaign) => void;
  onNote: (note: string) => void;
  /** "" removes it */
  onLogo: (dataUri: string) => void;
  onErreur: (message: string) => void;
  /**
   * Whether this campaign telephones the mayors it writes to. OPT-IN, so the
   * answer is no until somebody says otherwise: the email asked for a call
   * and the letter announced one whatever the campaign actually did, which
   * is a promise made to elected officials on behalf of people who never
   * made it.
   */
  appelTelephonique: boolean;
  onAppelTelephonique: (yes: boolean) => void;
  /**
   * The message templates this browser holds, over the six the image carries.
   *
   * ONE overlay and not two, unlike team mode. There the campaign's layer is
   * LIVE — a coordination corrects its letter and every team that did not
   * rewrite it gets the correction — so the two are kept apart and the
   * inherited one is only ever a placeholder. Here nothing is live by
   * promise: adopting a campaign COPIES its texts, exactly as it copies its
   * nine fields, and after that they are this browser's. Showing them as the
   * value is the honest reading, and pretending they are inherited would
   * promise an update that can never arrive.
   */
  templates: Templates;
  onTemplates: (templates: Templates) => Promise<Templates>;
  onMessage: (m: Message) => void;
  onSave: (
    cfg: Campaign,
    personalNote: string,
    appelTelephonique: boolean,
  ) => void;
}

export function CampaignTab({
  draft,
  note,
  dirty,
  logo,
  onEdit,
  onNote,
  onLogo,
  onErreur,
  appelTelephonique,
  onAppelTelephonique,
  templates,
  onTemplates,
  onMessage,
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
        {/* h2 group titles: right under the tab's h1 */}
        <ChampsCampagne
          values={draft}
          groupe="h2"
          onEdit={(key, value) => onEdit({ ...draft, [key]: value })}
        />
        {/* stored on choice, like everything else here: in this mode it
            never leaves the browser, so there is nothing to send */}
        <ChampLogo
          logo={logo}
          onChoisi={onLogo}
          onRetire={() => onLogo("")}
          onErreur={onErreur}
        />
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
        <p>
          <label>
            <input
              type="checkbox"
              checked={appelTelephonique}
              onChange={(e) => onAppelTelephonique(e.target.checked)}
            />{" "}
            J'appellerai les maires que je contacte
          </label>
        </p>
        <p className="gris">
          Coché, l'email propose un échange téléphonique et le courrier annonce
          un appel. Décoché, aucun message ne promet d'appel — ne le cochez que
          si vous comptez vraiment téléphoner.
        </p>
        <button
          type="button"
          onClick={() => onSave(draft, note, appelTelephonique)}
        >
          Enregistrer
        </button>
        {/* persistent: the marker appears as a text change, not as a node
            mounting with its warning already written */}
        <span className="gris" role="status" style={{ marginLeft: ".6rem" }}>
          {dirty ? "modifications non enregistrées" : ""}
        </span>
      </div>
      {/* The same editor the account version uses, one level down: here the
          texts inherited are the adopted campaign's, and the save is an
          IndexedDB write rather than a route. Its own card and its own save
          button, like the logo above — six long texts have no business
          riding on the button that stores nine short fields. */}
      <ModelesMessages
        niveau="navigateur"
        propres={templates}
        // the IMAGE's: there is no live layer between this browser and a
        // campaign, so an empty box falls back to the shipped text
        herites={{}}
        onSave={onTemplates}
        onEnregistre={() => {}}
        onError={(e) => onErreur(e instanceof Error ? e.message : String(e))}
        onMessage={onMessage}
      />
    </>
  );
}
