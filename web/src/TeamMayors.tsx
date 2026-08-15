import { useCallback, useEffect, useRef, useState } from "react";
import * as API from "./api.ts";
import { CompteurResultats, STATUSES, TableMaires } from "./common.tsx";
import { LigneCarte } from "./LigneCarte.tsx";
import * as M from "./messages.ts";
import type { Facets, MayorCard } from "./types.ts";

export function ListeServeur({
  onError,
  onOpen,
}: {
  onError: (e: unknown) => void;
  onOpen: (i: string) => void;
}) {
  const [q, setQ] = useState("");
  const [rank, setRank] = useState("has_endorsed");
  const [status, setStatus] = useState("");
  const [dept, setDept] = useState("");
  const [democracy, setDemocracy] = useState(false);
  const [rows, setRows] = useState<MayorCard[]>([]);
  const [total, setTotal] = useState(0);
  const [next, setNext] = useState<string | null>(null);
  const [failed, setFailed] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [facets, setFacets] = useState<Facets>({
    departments: [],
    by_rank: {},
  });

  useEffect(() => {
    API.facets().then(setFacets).catch(onError);
  }, [onError]);

  // the filter changes: back to zero. A request counter keeps a slow
  // answer to the old filter from crushing the new list.
  const latest = useRef(0);
  const criteria = { q, rank, status, department: dept, democracy };
  const key = JSON.stringify(criteria);

  useEffect(() => {
    const mine = ++latest.current;
    setLoading(true);
    const timer = setTimeout(async () => {
      try {
        const r = await API.mayors(JSON.parse(key));
        if (mine !== latest.current) return;
        setRows(r.rows);
        setTotal(r.total);
        setNext(r.next);
        setFailed(null);
      } catch (e) {
        if (mine === latest.current) onError(e);
      } finally {
        if (mine === latest.current) setLoading(false);
      }
    }, 250); // time to finish typing
    return () => clearTimeout(timer);
  }, [key, onError]);

  const sentinel = useRef(null);
  const loadNext = useCallback(async () => {
    if (next === null || loading) return;
    const mine = latest.current;
    setLoading(true);
    try {
      const r = await API.mayors({ ...JSON.parse(key), after: next });
      if (mine !== latest.current) return;
      // Deduplicate: OFFSET pagination runs over data the team modifies at
      // the same time, and a card entering or leaving the filter between
      // two pages made a mayor appear twice — or skip, which, in a
      // campaign whose object is coverage, means a mayor never contacted.
      setRows((old) => {
        const seen = new Set(old.map((m) => m.insee_code));
        return [...old, ...r.rows.filter((m) => !seen.has(m.insee_code))];
      });
      setNext(r.next);
    } catch (e) {
      if (mine === latest.current) {
        onError(e);
        // STOP. Without this the cursor stays put, the sentinel is still
        // on screen, and the effect fires again the moment loading
        // clears: a passing API failure became sixty requests a second,
        // which is what keeps the API from recovering.
        setNext(null);
        setFailed(mine === latest.current ? key : null);
      }
    } finally {
      // guarded like the main effect: a stale answer must not reopen the
      // door while another request is in flight
      if (mine === latest.current) setLoading(false);
    }
  }, [next, loading, key, onError]);

  // infinite scroll: the next page loads when the bottom of the list
  // enters the viewport, without a click
  useEffect(() => {
    const target = sentinel.current;
    if (!target || next === null) return undefined;
    const obs = new IntersectionObserver(
      (entries) => {
        if (entries[0].isIntersecting) loadNext();
      },
      { rootMargin: "300px" },
    );
    obs.observe(target);
    return () => obs.disconnect();
  }, [next, loadNext]);

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
            Vivier
            <select value={rank} onChange={(e) => setRank(e.target.value)}>
              {Object.entries(M.RANKS).map(([k, v]) => (
                <option key={k} value={k}>
                  {v} ({facets.by_rank[k] ?? 0})
                </option>
              ))}
            </select>
          </label>
        </div>
        <div>
          <label>
            Statut
            <select value={status} onChange={(e) => setStatus(e.target.value)}>
              <option value="">— tous —</option>
              {Object.entries(STATUSES).map(([k, [l]]) => (
                <option key={k} value={k}>
                  {l}
                </option>
              ))}
            </select>
          </label>
        </div>
        <div>
          <label>
            Département
            <select value={dept} onChange={(e) => setDept(e.target.value)}>
              <option value="">— tous —</option>
              {facets.departments.map((d) => (
                <option key={d} value={d}>
                  {d}
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
            Thème démocratique
          </label>
        </div>
      </form>

      {rank !== "has_endorsed" && (
        <p className="alerte">
          Vous êtes sorti du vivier prioritaire. Le message généré s'adapte au
          rang : on n'y remercie personne d'un parrainage qui n'a pas eu lieu.
        </p>
      )}

      <CompteurResultats shown={rows.length} total={total} />
      <div className="carte">
        <TableMaires>
          {rows.map((m) => (
            <LigneCarte key={m.insee_code} m={m} onOpen={onOpen} />
          ))}
        </TableMaires>
        <div ref={sentinel} />
        {loading && (
          <p className="gris" role="status" style={{ textAlign: "center" }}>
            Chargement…
          </p>
        )}
        {failed !== null && (
          <p className="alerte" role="alert" style={{ textAlign: "center" }}>
            Chargement interrompu.{" "}
            <button
              type="button"
              className="lien"
              onClick={() => {
                setFailed(null);
                API.mayors(JSON.parse(key))
                  .then((r) => {
                    setRows(r.rows);
                    setTotal(r.total);
                    setNext(r.next);
                  })
                  .catch(onError);
              }}
            >
              Réessayer
            </button>
          </p>
        )}
        {failed === null && next === null && rows.length > 0 && (
          <p className="gris" style={{ textAlign: "center" }}>
            — fin de la liste —
          </p>
        )}
        {rows.length === 0 && !loading && (
          <p className="gris">
            Aucun maire avec ces critères.{" "}
            <button
              type="button"
              className="lien"
              onClick={() => {
                setQ("");
                setRank("has_endorsed");
                setStatus("");
                setDept("");
                setDemocracy(false);
              }}
            >
              Réinitialiser les filtres
            </button>
          </p>
        )}
      </div>
    </>
  );
}
