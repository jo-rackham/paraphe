import { useRef } from "react";
import type { DownloadState } from "./BrowserProgression.tsx";
import { Emoji } from "./common.tsx";
import type { ListKey } from "./data.ts";

interface AccueilProps {
  onCsv: (f: File) => void;
  onDemo: () => void;
  onDownload: (key: ListKey) => void;
  download: DownloadState | null;
}

export function Accueil({ onCsv, onDemo, onDownload, download }: AccueilProps) {
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
            aria-disabled={download ? true : undefined}
            onClick={() => onDownload("light")}
          >
            <Emoji>⬇ </Emoji>Charger la liste prioritaire
          </button>{" "}
          <button
            type="button"
            className="secondaire"
            aria-disabled={download ? true : undefined}
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
