import { formatBytes, LISTS, type ListKey, type Progress } from "./data.ts";

export interface DownloadState extends Progress {
  key: ListKey;
}

export function Progression({ state }: { state: DownloadState }) {
  const { key, received, total } = state;
  const pct = total ? Math.round((received / total) * 100) : null;
  return (
    // no live region here: the card mounts together with its sentence,
    // which assistive technology may not announce. The announcement is the
    // persistent sr-only region in Browser; this card is the visual.
    <div className="carte chargement">
      <p style={{ margin: 0 }}>
        <strong>Téléchargement de la {LISTS[key].name}…</strong>{" "}
        <span className="gris">
          {formatBytes(received)}
          {total ? ` sur ${formatBytes(total)}` : ""}
        </span>
      </p>
      <div
        className="jauge"
        role="progressbar"
        aria-label={`Téléchargement de la ${LISTS[key].name}`}
        aria-valuemin={0}
        aria-valuemax={100}
        // undefined while the size is unknown: an indeterminate bar is
        // exactly what a progressbar without aria-valuenow means
        aria-valuenow={pct ?? undefined}
      >
        <div
          className={pct === null ? "barre indeterminee" : "barre"}
          style={pct === null ? undefined : { width: `${pct}%` }}
        />
      </div>
      <p className="gris" style={{ margin: ".3rem 0 0" }}>
        {LISTS[key].detail} — une fois chargée, elle reste dans ce navigateur et
        ne sera plus retéléchargée.
      </p>
    </div>
  );
}
