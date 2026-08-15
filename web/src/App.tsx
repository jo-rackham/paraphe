import { useEffect, useState } from "react";
import type { Mode } from "./api.ts";
import * as API from "./api.ts";
import Browser from "./Browser.tsx";
import { useViewFocus } from "./common.tsx";
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

  const outage = mode?.kind === "outage" ? mode : null;
  // the modes own their titles; this hook only speaks for the two screens
  // App renders itself — and it moves focus to « Serveur injoignable »
  useViewFocus(
    mode === null ? "chargement" : mode.kind,
    outage ? "Serveur injoignable" : null,
  );

  if (mode === null || outage) {
    // One shell for loading AND outage: the alert region exists from the
    // first paint, and the outage message lands in it as a TEXT change —
    // an alert inserted together with its text is dropped by some
    // assistive technology, and this message is addressed precisely to
    // the volunteer who cannot see the screen.
    return (
      <main>
        <p className={outage ? "alerte erreur" : "sr-only"}>
          <span role="alert">{outage ? outage.message : ""}</span>
        </p>
        {mode === null ? (
          <p role="status">Chargement…</p>
        ) : (
          <>
            {/* above all no fallback to browser mode: the work would go
                into the browser without ever reaching the team */}
            <h1>Serveur injoignable</h1>
            <p>
              Votre travail est enregistré sur le serveur de la campagne : rien
              n'est perdu, mais il faut attendre qu'il réponde.{" "}
              <button type="button" onClick={() => setAttempt((n) => n + 1)}>
                Réessayer
              </button>
            </p>
          </>
        )}
      </main>
    );
  }
  if (mode.kind === "instance") return <Instance config={mode.config} />;
  return mode.kind === "team" ? <Team config={mode.config} /> : <Browser />;
}
