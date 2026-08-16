import { type ReactNode, useState } from "react";
import * as API from "./api.ts";
import { Alerte, useSubmitGuard } from "./common.tsx";
import type { Me, Message } from "./types.ts";

// The sign-in form, shared by the campaign screen and the instance
// administration. One copy: the two forms were already the same fields, and
// a second implementation of the link flow would be a second place for it to
// go wrong.
//
// It lives here rather than in common.tsx on purpose — common.tsx is shared
// with the account-less browser version, which has no API to talk to and
// must not carry its client.
//
// Two paths, both offered:
//   - the password, which the lead reads out and which works when no relay
//     does;
//   - a link by email, the only thing left to whoever has forgotten it —
//     shown only where the server says it can send one.

export function FormulaireConnexion({
  magicLink,
  onSignedIn,
  onAttempt,
  children,
}: {
  magicLink: boolean;
  onSignedIn: (m: Me) => void;
  /**
   * A new attempt is starting, so whatever the SCREEN was saying is now
   * behind us. The screen has a live region of its own — the one carrying
   * « ce lien n'est plus valable » — and leaving it up beside this form's
   * own refusal put two `role="alert"` regions on screen with contradictory
   * text, both announced.
   */
  onAttempt?: () => void;
  /** Anything the screen wants under the form (a hint, a way back). */
  children?: ReactNode;
}) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [message, setMessage] = useState<Message | null>(null);
  // WHICH request is in flight, not merely that one is. Both buttons go
  // aria-disabled either way, but only the one that was pressed changes its
  // label: a single flag made the submit button announce « Connexion… » the
  // instant somebody asked for a link, which reads as the form having
  // misunderstood what they clicked.
  const [busy, setBusy] = useState<"signin" | "link" | null>(null);
  // The guard is a REF and the state is only what the screen shows: two
  // clicks in the same tick run two handlers built by the same render, and
  // both read the state as it was BEFORE either of them. On this form that
  // spends two of the three links an address is allowed in a quarter of an
  // hour — and the first arrives dead, because minting the second deleted
  // it. The helper is the project's; this is one more caller.
  const [alreadyGoing, done] = useSubmitGuard();

  const report = (err: unknown) =>
    setMessage({
      tone: "erreur",
      text: err instanceof Error ? err.message : String(err),
    });

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (alreadyGoing()) return; // aria-disabled greys it but keeps it live
    setMessage(null);
    onAttempt?.();
    setBusy("signin");
    try {
      onSignedIn(await API.signIn(email.trim(), password));
    } catch (err) {
      report(err);
    } finally {
      done();
      setBusy(null);
    }
  };

  const askForLink = async () => {
    if (alreadyGoing()) return;
    // FIRST, before any branch can return: pressing the button IS the new
    // attempt, whether or not it gets as far as the server. Called after
    // the empty-address check, it left the screen's own alert standing
    // beside this form's — two live regions, contradicting each other, in
    // the very case a reader is most likely to hit (a spent link, then the
    // button pressed before typing).
    onAttempt?.();
    const address = email.trim();
    if (address === "") {
      // The link button is not a submit, so the browser validates nothing:
      // said here rather than sent as an empty address the server refuses.
      //
      // `done()` before returning: the guard ACQUIRES when it is asked, so a
      // branch that leaves without releasing it leaves the form refusing
      // every click after this one — a screen that answers nothing, for
      // somebody who has just been told to type their address.
      done();
      setMessage({
        tone: "erreur",
        text: "Indiquez votre adresse email pour recevoir un lien.",
      });
      return;
    }
    setMessage(null);
    setBusy("link");
    try {
      const { message: answer } = await API.requestLink(address);
      // The server's own sentence, verbatim: it is the SAME whether or not
      // an account bears this address, and rewording it per case here is
      // exactly how this screen would become a roster of the team.
      setMessage({ tone: "ok", text: answer });
    } catch (err) {
      report(err);
    } finally {
      done();
      setBusy(null);
    }
  };

  return (
    <form className="carte etroite" onSubmit={submit}>
      <Alerte message={message} />
      <p>
        <label>
          Adresse email
          <input
            type="text"
            autoComplete="username"
            required
            value={email}
            onChange={(e) => {
              setEmail(e.target.value);
              // « un lien vient de partir » under a field that now shows
              // another address describes something that did not happen to
              // the address being read, so it goes.
              //
              // A REFUSAL stays. Clearing everything took « trop de
              // tentatives pour cette adresse » off the screen the moment
              // its reader started correcting what they assumed was a typo
              // — and that ceiling is per address, which is precisely what
              // they were about to change.
              setMessage((shown) => (shown?.tone === "erreur" ? shown : null));
            }}
          />
        </label>
      </p>
      <p>
        <label>
          Mot de passe
          <input
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
      </p>
      {/* aria-disabled and never disabled: `disabled` on the button holding
          the focus drops that focus to <body>, in every browser. The guard
          against a second click is in the handlers above. */}
      <button type="submit" aria-disabled={busy !== null || undefined}>
        {busy === "signin" ? "Connexion…" : "Se connecter"}
      </button>
      {magicLink && (
        <p className="gris">
          Mot de passe oublié ?{" "}
          {/* type="button": a submit here would validate the password field
              and refuse to send a link to someone who does not have one */}
          <button
            type="button"
            className="lien"
            aria-disabled={busy !== null || undefined}
            onClick={askForLink}
          >
            {busy === "link" ? "Envoi…" : "Recevoir un lien par email"}
          </button>
        </p>
      )}
      {children}
    </form>
  );
}
