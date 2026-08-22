import { useState } from "react";
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
   * This volunteer's OWN overlay — only what they rewrote themselves.
   */
  templates: Templates;
  /**
   * The adopted campaign's layer, under the volunteer's: the same two-layer
   * screen as team mode, one layer renamed. The campaign's text is the
   * PLACEHOLDER of an empty box, never its value — filled in, it would be a
   * frozen copy, and the campaign's next correction would stop arriving.
   * Empty for a browser that never adopted, and the image's texts then show.
   */
  campaignTemplates: Templates;
  onTemplates: (templates: Templates) => Promise<Templates>;
  onMessage: (m: Message) => void;
  // awaited: the word beside the button appears once the parent's writes
  // have landed, not when the click did
  onSave: (
    cfg: Campaign,
    personalNote: string,
    appelTelephonique: boolean,
  ) => void | Promise<void>;
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
  campaignTemplates,
  onTemplates,
  onMessage,
  onSave,
}: CampaignTabProps) {
  // whether the LAST press got as far as the parent's answer — shown only
  // once `dirty` has nothing left to warn about, so a failed save (which
  // leaves the draft dirty) never reads as saved
  const [savedSaid, setSavedSaid] = useState(false);
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
          onClick={async () => {
            setSavedSaid(false);
            // the CURRENT parent reports its own failures and never
            // rejects; the widened signature invites one that does, and an
            // uncaught rejection in an async handler surfaces nowhere a
            // volunteer looks. Explicit, per the house rule: the fault is
            // shown, not swallowed — and « Enregistré. » is not said.
            try {
              await onSave(draft, note, appelTelephonique);
            } catch (e) {
              onErreur(e instanceof Error ? e.message : String(e));
              return;
            }
            // the parent reports its own failure and the draft then stays
            // DIRTY, which is the branch shown below — this word appears
            // only once the marker has nothing left to warn about
            setSavedSaid(true);
          }}
        >
          Enregistrer
        </button>
        {/* persistent: the marker appears as a text change, not as a node
            mounting with its warning already written. Three states, dirty
            first: a form retyped after a save is unsaved again, whatever
            was said in between. */}
        <span
          className={dirty ? "gris" : "confirmation"}
          role="status"
          style={{ marginLeft: ".6rem" }}
        >
          {dirty
            ? "modifications non enregistrées"
            : savedSaid
              ? "Enregistré."
              : ""}
        </span>
      </div>
      {/* The same editor the account version uses, one level down: here the
          inherited layer is the adopted campaign's — LIVE, refreshed from
          its site — and the save is an IndexedDB write rather than a route.
          Its own card and its own save button, like the logo above — six
          long texts have no business riding on the button that stores nine
          short fields. */}
      <ModelesMessages
        niveau="navigateur"
        propres={templates}
        // the adopted campaign's layer; an empty box falls back to it, then
        // to the shipped text — the exact inheritance team mode shows
        herites={campaignTemplates}
        onSave={onTemplates}
        onEnregistre={() => {}}
        onError={(e) => onErreur(e instanceof Error ? e.message : String(e))}
        onMessage={onMessage}
      />
    </>
  );
}
