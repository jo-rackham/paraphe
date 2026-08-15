import { useEffect, useState } from "react";
import type { DownloadState } from "./BrowserProgression.tsx";
import { CompteurResultats, LigneMaire, TableMaires } from "./common.tsx";
import type { ListKey } from "./data.ts";
import * as M from "./messages.ts";
import type { Mayor, Tracking } from "./types.ts";

interface ListeProps {
  mayors: Mayor[];
  tracking: Record<string, Tracking>;
  counts: Record<string, number>;
  q: string;
  setQ: (v: string) => void;
  rankFilter: string;
  setRankFilter: (v: string) => void;
  departments: string[];
  deptFilter: string;
  setDeptFilter: (v: string) => void;
  onChoose: (m: Mayor) => void;
  loadedList: ListKey | "personnel" | "demo" | null;
  onComplete: () => void;
  download: DownloadState | null;
}

export function Liste({
  mayors,
  tracking,
  counts,
  q,
  setQ,
  rankFilter,
  setRankFilter,
  departments,
  deptFilter,
  setDeptFilter,
  onChoose,
  loadedList,
  onComplete,
  download,
}: ListeProps) {
  const [shown, setShown] = useState(50);
  // the filter changes: back to the top, otherwise a 300-row window stays
  // open on a list that only has 12 left
  // the three filters are TRIGGERS — the effect reads none of them, it only
  // has to run when one changes. That is the whole behaviour.
  // biome-ignore lint/correctness/useExhaustiveDependencies: triggers, read by nothing
  useEffect(() => setShown(50), [q, rankFilter, deptFilter]);
  return (
    <>
      <h1>Les maires</h1>
      <form className="enligne carte" onSubmit={(e) => e.preventDefault()}>
        <div>
          <label>
            Recherche (commune, nom)
            <input
              type="text"
              value={q}
              onChange={(e) => setQ(e.target.value)}
            />
          </label>
        </div>
        <div>
          <label>
            Qui afficher
            <select
              value={rankFilter}
              onChange={(e) => setRankFilter(e.target.value)}
            >
              {Object.entries(M.RANKS).map(([k, v]) => (
                <option key={k} value={k}>
                  {v} ({counts[k] ?? 0})
                </option>
              ))}
              <option value="all">Tous ({counts.total})</option>
            </select>
          </label>
        </div>
        <div>
          <label>
            Département
            <select
              value={deptFilter}
              onChange={(e) => setDeptFilter(e.target.value)}
            >
              <option value="">— tous ({departments.length}) —</option>
              {departments.map((d) => (
                <option key={d} value={d}>
                  {d}
                </option>
              ))}
            </select>
          </label>
        </div>
      </form>
      {rankFilter !== "has_endorsed" && (
        <p className="alerte">
          Vous êtes sorti du vivier prioritaire. Le message généré s'adapte au
          rang : on n'y remercie personne d'un parrainage qui n'a pas eu lieu.
        </p>
      )}
      {loadedList === "light" && (
        <p className="alerte">
          <strong>Liste prioritaire</strong> ({counts.total} maires) — les 500
          signatures n'en sortiront pas seules : épuisée,{" "}
          <button
            type="button"
            className="lien"
            aria-disabled={download ? true : undefined}
            onClick={onComplete}
          >
            chargez les 34 826 maires de France
          </button>{" "}
          <span className="gris">
            (2 Mo, une seule fois ; votre suivi est conservé)
          </span>
          .
        </p>
      )}
      <CompteurResultats
        shown={Math.min(shown, mayors.length)}
        total={mayors.length}
      />
      <div className="carte">
        <TableMaires>
          {mayors.slice(0, shown).map((m) => (
            <LigneMaire
              key={m.insee_code as string}
              m={m}
              status={tracking[m.insee_code as string]?.status}
              onOpen={onChoose}
            />
          ))}
        </TableMaires>
        {mayors.length === 0 && (
          <p className="gris">
            Aucun maire avec ces critères.{" "}
            <button
              type="button"
              className="lien"
              onClick={() => {
                setQ("");
                setRankFilter("has_endorsed");
                setDeptFilter("");
              }}
            >
              Réinitialiser les filtres
            </button>
          </p>
        )}
        {shown < mayors.length && (
          <p style={{ textAlign: "center" }}>
            <button
              type="button"
              className="secondaire"
              onClick={() => setShown((c) => c + 50)}
            >
              Afficher la suite
            </button>
          </p>
        )}
      </div>
    </>
  );
}
