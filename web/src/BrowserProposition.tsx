import { campaignLabel } from "./common.tsx";
import * as M from "./messages.ts";
import { type ConfiguredOffer, instanceDomain } from "./prefill.ts";

/**
 * The campaign a link proposes, shown before anything is written.
 *
 * The values below are what a mayor will read, and the contacts are what
 * they will answer to. A volunteer who did not expect this screen is
 * exactly the person who must see it.
 */
export function Proposition({
  offer,
  onAccept,
  onRefuse,
}: {
  offer: ConfiguredOffer;
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
      {/* THE TEXTS THEMSELVES, when this campaign has rewritten any. The nine
          values above fill the messages; these ARE the messages, so a link
          that carries them is proposing more than a name and a telephone
          number. Said in a sentence rather than shown in full: six templates
          of two thousand characters on a confirmation screen is a screen
          nobody reads, and the volunteer can read every one of them, and
          change them, on « Ma campagne » the moment they accept. */}
      {Object.keys(offer.templates).length > 0 && (
        <p>
          <strong>
            Cette campagne fournit aussi ses propres textes de messages
          </strong>{" "}
          ({Object.keys(offer.templates).length} sur 6). Ils remplaceront les
          textes fournis avec l'application. Vous pourrez les lire et les
          modifier dans « Ma campagne ».
        </p>
      )}
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
