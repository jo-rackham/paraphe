import { useCallback, useEffect, useState } from "react";
import * as API from "./api.ts";
import { Chip, Emoji, TableMaires, useSubmitGuard } from "./common.tsx";
import { LigneCarte } from "./LigneCarte.tsx";
import type {
  Dashboard as DashboardData,
  Me,
  Message,
  ServerConfig,
} from "./types.ts";

interface TableauProps {
  cfg: ServerConfig;
  me: Me;
  onError: (e: unknown) => void;
  onOpen: (insee: string) => void;
  onMessage: (m: Message) => void;
}

/**
 * One goal tile: the number is the information, the bar its silhouette
 * against the goal. The bar is decoration — the text beside it already
 * says "12 sur 500" — so assistive technology skips it.
 */
function Tuile({
  label,
  value,
  goal,
}: {
  label: string;
  value: number;
  goal?: number;
}) {
  const pct = goal ? Math.min(100, Math.round((value / goal) * 100)) : null;
  return (
    <div className="tuile">
      <p className="valeur">
        {value.toLocaleString("fr")}
        {goal !== undefined && <span className="objectif"> sur {goal}</span>}
      </p>
      <p className="libelle">{label}</p>
      {pct !== null && (
        <div className="jauge" aria-hidden="true">
          <div className="barre" style={{ width: `${pct}%` }} />
        </div>
      )}
    </div>
  );
}

export function Tableau({ cfg, me, onError, onOpen, onMessage }: TableauProps) {
  const [data, setData] = useState<DashboardData | null>(null);
  const [dept, setDept] = useState("");
  const [rank, setRank] = useState("has_endorsed");
  const [democracy, setDemocracy] = useState(false);
  const [sending, setSending] = useState(false);
  const [busy, done] = useSubmitGuard();

  const reload = useCallback(async () => {
    try {
      setData(await API.dashboard());
    } catch (e) {
      onError(e);
    }
  }, [onError]);

  useEffect(() => {
    reload();
  }, [reload]);

  if (!data) return <p role="status">Chargement…</p>;

  const take = async () => {
    if (busy()) return; // a REF: state is a render behind
    setSending(true);
    try {
      const r = await API.takeBatch({ department: dept, rank, democracy });
      onMessage(
        r.taken > 0
          ? { tone: "ok", text: `${r.taken} maire(s) vous sont attribués.` }
          : { tone: "erreur", text: r.message },
      );
      await reload();
    } catch (e) {
      onError(e);
    } finally {
      done();
      setSending(false);
    }
  };

  return (
    <>
      <h1>Mon tableau de bord</h1>

      <div className="carte">
        <h2 style={{ marginTop: 0 }}>Où en est la campagne</h2>
        <p className="gris">
          Volumes de toute la campagne, sans aucun nom. Il faut 500
          présentations, dans au moins 30 départements, 50 au plus par
          département.
        </p>
        <div className="tuiles">
          <Tuile
            label="Promesses et signatures"
            value={(data.stats.promised ?? 0) + (data.stats.signed ?? 0)}
            goal={500}
          />
          <Tuile
            label="Départements avec une promesse"
            value={data.departments_covered}
            goal={30}
          />
          <Tuile
            label="Mes fiches à traiter"
            value={
              data.mine.filter(
                (m) =>
                  !m.status ||
                  m.status === "to_contact" ||
                  m.status === "to_call_back",
              ).length
            }
          />
        </div>
        <table>
          <tbody>
            {(cfg.statuses ?? []).map((s) => (
              <tr key={s.key}>
                <td>
                  <Chip status={s.key} />
                </td>
                <td>{data.stats[s.key] ?? 0}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="carte">
        <h2 style={{ marginTop: 0 }}>Prendre un lot</h2>
        <p className="gris">
          {data.batch_size} maires vous sont réservés, les mieux notés d'abord.
          Personne d'autre ne les recevra.
        </p>
        <form
          className="enligne"
          onSubmit={(e) => {
            e.preventDefault();
            take();
          }}
        >
          <div>
            <label>
              Département
              <select value={dept} onChange={(e) => setDept(e.target.value)}>
                <option value="">— tout mon périmètre —</option>
                {data.departments.map((d) => (
                  <option key={d} value={d}>
                    {d}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <div>
            <label>
              Vivier
              <select value={rank} onChange={(e) => setRank(e.target.value)}>
                {(cfg.ranks ?? []).map((r) => (
                  <option key={r.key} value={r.key}>
                    {r.label} ({data.by_rank[r.key] ?? 0})
                  </option>
                ))}
              </select>
            </label>
          </div>
          <div>
            <label>
              <input
                type="checkbox"
                checked={democracy}
                onChange={(e) => setDemocracy(e.target.checked)}
              />{" "}
              A parrainé une candidature sur le fonctionnement démocratique
            </label>
          </div>
          <button type="submit" aria-disabled={sending || undefined}>
            {sending ? "Attribution…" : "Prendre un lot"}
          </button>
        </form>
      </div>

      <h2>Mes maires ({data.mine.length})</h2>
      {data.mine.length === 0 ? (
        <p className="gris">Aucun pour l'instant — prenez un lot ci-dessus.</p>
      ) : (
        <div className="carte">
          <TableMaires>
            {data.mine.map((m) => (
              <LigneCarte key={m.insee_code} m={m} onOpen={onOpen} />
            ))}
          </TableMaires>
        </div>
      )}

      {data.team.length > 0 && (
        <>
          <h2>Mon équipe</h2>
          <div className="carte">
            <table>
              <thead>
                <tr>
                  <th scope="col">Bénévole</th>
                  <th scope="col">Fiches</th>
                  <th scope="col">Contactées</th>
                </tr>
              </thead>
              <tbody>
                {data.team.map((e) => (
                  <tr key={e.who}>
                    <td>
                      {e.who}
                      {e.who === me.account.name ? " (vous)" : ""}
                    </td>
                    <td>{e.n}</td>
                    <td>{e.done}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}

      {data.departments_with_promise.length > 0 && (
        <>
          <h2>Départements acquis</h2>
          <div className="carte">
            <p>
              {data.departments_with_promise.map((d) => (
                <span
                  key={d.key}
                  className="chip chip-vert"
                  style={{ marginRight: ".3rem" }}
                >
                  {d.key} : {d.n}
                </span>
              ))}
            </p>
          </div>
        </>
      )}

      {/* LA CAMPAGNE, pas l'équipe : `routeExport` ne porte aucun prédicat
          d'équipe — le fichier a toujours contenu toute la campagne. Une
          coordination qui croit exporter le travail d'une équipe en tire des
          totaux faux, et un référent croit y trouver un périmètre. */}
      <p>
        <a className="bouton secondaire" href={API.exportUrl()}>
          <Emoji>⬇ </Emoji>Exporter le suivi de la campagne (CSV)
        </a>
      </p>
      <p className="gris">
        Le fichier couvre toute la campagne, pas seulement votre équipe. Les
        noms des bénévoles des autres équipes n'y sont pas, comme à l'écran.
      </p>
    </>
  );
}
