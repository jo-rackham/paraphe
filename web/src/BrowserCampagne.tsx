import { ChampLogo, ChampsCampagne } from "./common.tsx";
import type { Campaign } from "./types.ts";

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
  onSave: (cfg: Campaign, personalNote: string) => void;
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
        <button type="button" onClick={() => onSave(draft, note)}>
          Enregistrer
        </button>
        {/* persistent: the marker appears as a text change, not as a node
            mounting with its warning already written */}
        <span className="gris" role="status" style={{ marginLeft: ".6rem" }}>
          {dirty ? "modifications non enregistrées" : ""}
        </span>
      </div>
    </>
  );
}
