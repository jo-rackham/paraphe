import { useRef, useState } from "react";
import { Emoji } from "./common.tsx";
import { LISTS, type ListKey } from "./data.ts";

interface DonneesProps {
  counts: Record<string, number>;
  onExport: () => void;
  onImport: (f: File, merge: boolean) => void;
  onCsv: (f: File) => void;
  onDemo: () => void;
  onErase: () => void;
  onDownload: (key: ListKey) => void;
  loadedList: ListKey | "personnel" | "demo" | null;
}

export function Donnees({
  counts,
  onExport,
  onImport,
  onCsv,
  onDemo,
  onErase,
  onDownload,
  loadedList,
}: DonneesProps) {
  const importField = useRef<HTMLInputElement>(null);
  const csvField = useRef<HTMLInputElement>(null);
  const [merge, setMerge] = useState(true);
  return (
    <>
      <h1>Mes données</h1>
      <div className="carte">
        <p>
          Tout est dans ce navigateur, dans une base locale. Rien n'est envoyé,
          rien n'est sauvegardé ailleurs —{" "}
          <strong>vider les données du site les supprime définitivement</strong>
          . Exportez régulièrement.
        </p>
        <p className="gris">{counts.total} maires chargés.</p>
        <p>
          <button type="button" onClick={onExport}>
            <Emoji>⬇ </Emoji>Exporter (JSON)
          </button>{" "}
          <button
            type="button"
            className="secondaire"
            onClick={() => importField.current?.click()}
          >
            <Emoji>⬆ </Emoji>Importer une sauvegarde
          </button>
        </p>
        <p>
          <label>
            <input
              type="checkbox"
              checked={merge}
              onChange={(e) => setMerge(e.target.checked)}
            />{" "}
            Fusionner plutôt qu'écraser
          </label>
          <br />
          <span className="gris">
            Fusionner garde votre travail et ne reprend que ce qui est plus
            récent : c'est ce qu'il faut quand deux bénévoles s'échangent leurs
            fichiers.
          </span>
        </p>
        <input
          ref={importField}
          type="file"
          accept=".json,application/json"
          hidden
          onChange={(e) => {
            const f = e.target.files?.[0];
            if (f) onImport(f, merge);
          }}
        />
      </div>

      <div className="carte">
        <h2 style={{ marginTop: 0 }}>Changer de liste</h2>
        <p className="gris">
          Chargée actuellement :{" "}
          {loadedList
            ? loadedList === "demo"
              ? "un jeu de données FICTIVES"
              : loadedList === "personnel"
                ? "votre propre fichier"
                : // read from a backup the user supplies: an unknown key
                  // must not throw where the screen says something
                  (LISTS[loadedList]?.name ?? "une liste inconnue")
            : "aucune"}
          . Changer de liste ne touche pas à votre suivi : il est indexé par
          code INSEE.
        </p>
        <p>
          <button type="button" onClick={() => onDownload("light")}>
            <Emoji>⬇ </Emoji>Liste prioritaire
          </button>{" "}
          <button type="button" onClick={() => onDownload("complete")}>
            <Emoji>⬇ </Emoji>Base complète
          </button>
        </p>
        <p>
          <button
            type="button"
            className="secondaire"
            onClick={() => csvField.current?.click()}
          >
            <Emoji>📂 </Emoji>Charger un CSV
          </button>{" "}
          <button type="button" className="secondaire" onClick={onDemo}>
            Données fictives
          </button>
        </p>
        <input
          ref={csvField}
          type="file"
          accept=".csv,text/csv"
          hidden
          onChange={(e) => {
            const f = e.target.files?.[0];
            if (f) onCsv(f);
          }}
        />
      </div>

      <div className="carte">
        <h2 style={{ marginTop: 0 }}>Tout effacer</h2>
        <p className="gris">
          À faire en fin de campagne : les notes portent sur des personnes
          nommées.
        </p>
        <button
          type="button"
          className="secondaire"
          onClick={() => {
            if (
              confirm(
                "Effacer définitivement la liste, le suivi et la configuration ?",
              )
            )
              onErase();
          }}
        >
          Effacer ce navigateur
        </button>
      </div>
    </>
  );
}
