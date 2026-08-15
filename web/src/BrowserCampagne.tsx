import { ChampsCampagne } from "./common.tsx";
import type { Campaign } from "./types.ts";

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

export function CampaignTab({
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
        {/* h2 group titles: right under the tab's h1 */}
        <ChampsCampagne
          values={draft}
          groupe="h2"
          onEdit={(key, value) => onEdit({ ...draft, [key]: value })}
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
