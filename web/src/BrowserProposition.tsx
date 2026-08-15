import { campaignLabel } from "./common.tsx";
import * as M from "./messages.ts";
import { instanceDomain, type Offer } from "./prefill.ts";

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
