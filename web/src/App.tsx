import { useEffect, useState } from "react";
import type { Mode } from "./api.ts";
import * as API from "./api.ts";
import Browser from "./Browser.tsx";
import Instance from "./Instance.tsx";
import Team from "./Team.tsx";

// Two modes, one application.
//
// "team": an API answers behind the page, the work is shared and walled
// off per local team — the only mode that keeps two volunteers from
// writing to the same mayor.
//
// "browser": nothing behind the page (GitHub Pages publication, or a file
// opened locally), everything lives in the browser and no data is ever
// transmitted.
//
// The server decides, not a build flag: a version built with the wrong
// flag is only noticed in production.

export default function App() {
  const [mode, setMode] = useState<Mode | null>(null);
  const [attempt, setAttempt] = useState(0);

  // `attempt` is a TRIGGER, not a value this effect reads. "Réessayer" bumps
  // it, and that is the only thing that re-runs the detection. Removing it —
  // which the rule asks for — leaves the button doing nothing at all.
  // biome-ignore lint/correctness/useExhaustiveDependencies: a trigger, read by nothing
  useEffect(() => {
    let alive = true;
    setMode(null);
    API.detectMode().then((m) => {
      if (alive) setMode(m);
    });
    return () => {
      alive = false;
    };
  }, [attempt]);

  if (mode === null)
    return (
      <main>
        <p>Chargement…</p>
      </main>
    );
  if (mode.kind === "outage") {
    // above all no fallback to browser mode: the work would go into the
    // browser without ever reaching the team
    return (
      <main>
        <h1>Serveur injoignable</h1>
        <p className="alerte erreur">{mode.message}</p>
        <p>
          Votre travail est enregistré sur le serveur de la campagne : rien
          n'est perdu, mais il faut attendre qu'il réponde.{" "}
          <button type="button" onClick={() => setAttempt((n) => n + 1)}>
            Réessayer
          </button>
        </p>
      </main>
    );
  }
  if (mode.kind === "instance") return <Instance config={mode.config} />;
  return mode.kind === "team" ? <Team config={mode.config} /> : <Browser />;
}
