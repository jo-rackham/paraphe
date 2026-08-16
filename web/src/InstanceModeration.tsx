import { useEffect, useState } from "react";
import * as API from "./api.ts";
import {
  focusContenu,
  holdFocusThrough,
  label,
  ORG_STATES,
  REQUEST_STATES,
  useSubmitGuard,
} from "./common.tsx";
import type { Message, ModerationQueue, QueuedRequest } from "./types.ts";

export function Moderation({ onMessage }: { onMessage: (m: Message) => void }) {
  const [queue, setQueue] = useState<ModerationQueue | null>(null);
  const [busy, setBusy] = useState<number | null>(null);
  // `busy` says a decision is in flight, so EVERY pair can wear
  // aria-disabled: the guard below covers the whole queue, so a button that
  // stayed live would only swallow the click.
  const [deciding, decisionMade] = useSubmitGuard();
  const [reasons, setReasons] = useState<Record<number, string>>({});
  // The coordination password is returned only ONCE: it does not go back
  // to the database in the clear, and there is no way to retrieve it
  // afterwards.
  const [opened, setOpened] = useState<{
    address: string;
    coordination: string;
    password: string;
    invitation_sent?: boolean;
    invitation_error?: string;
  } | null>(null);

  const load = async () => {
    try {
      setQueue(await API.moderationQueue());
    } catch (e) {
      onMessage({ tone: "erreur", text: (e as Error).message });
    }
  };
  // `load` is rebuilt on every render, so listing it would re-fetch the
  // queue on every render, for ever. The queue is read once on mount, and
  // refreshed explicitly by `decide`.
  // biome-ignore lint/correctness/useExhaustiveDependencies: listing `load` would loop
  useEffect(() => {
    void load();
  }, []);

  const decide = async (d: QueuedRequest, decision: "accepted" | "refused") => {
    // A REF, because `busy` is a render behind: two clicks in the same tick
    // run two handlers built by the same render, both read it as null, and
    // both approve. Two campaigns open, two coordination passwords returned,
    // and `setOpened` keeps the LAST — so the first coordinator's password,
    // shown once and stored nowhere, is gone from the only screen it existed
    // on. `aria-disabled` is what the pair shows; this is what guards it.
    if (deciding()) return;
    setBusy(d.id);
    // captured BEFORE the round trip: the card leaves the queue when the
    // reload lands, not when the decision answers
    const restoreFocus = holdFocusThrough();
    try {
      const rep = await API.decideRequest(d.id, decision, reasons[d.id] ?? "");
      // The server has answered: the card leaves the pending list HERE, not
      // when the reload lands. `load` swallows its own failure into a message,
      // so a blip would otherwise leave the one-time password beside the very
      // request it answers — and a moderator discards a password that
      // contradicts the screen. It is shown once and never again.
      setQueue(
        (q) =>
          q && {
            ...q,
            requests: q.requests.map((x) =>
              x.id === d.id ? { ...x, state: decision } : x,
            ),
          },
      );
      if (rep.password && rep.address && rep.coordination) {
        setOpened({
          address: rep.address,
          coordination: rep.coordination,
          password: rep.password,
          invitation_sent: rep.invitation_sent,
          invitation_error: rep.invitation_error,
        });
      } else {
        onMessage({ tone: "ok", text: `Demande ${d.slug} refusée.` });
      }
      await load();
    } catch (e) {
      onMessage({ tone: "erreur", text: (e as Error).message });
    } finally {
      decisionMade();
      setBusy(null);
      // the decided card unmounts with the very button that decided it —
      // and it does so when `load()` above lands, which is why the focus is
      // held across the whole round trip rather than rescued at its end
      restoreFocus();
    }
  };

  // Direct creation ends on the same one-time password card as an approval:
  // one display, whatever the door the campaign came through.
  const create = async (creation: Parameters<typeof API.createCampaign>[0]) => {
    const rep = await API.createCampaign(creation);
    setOpened({
      address: rep.address,
      coordination: rep.coordination,
      password: rep.password,
      invitation_sent: rep.invitation_sent,
      invitation_error: rep.invitation_error,
    });
    await load();
  };

  if (!queue) return <p role="status">Chargement…</p>;
  const pending = queue.requests.filter((d) => d.state === "pending");
  const decided = queue.requests.filter((d) => d.state !== "pending");

  return (
    <>
      <h1>Demandes d'hébergement</h1>

      {/* The announcement is a PERSISTENT text-only region beside the
          card, never the card itself: a live region is reliable only when
          its content changes inside an existing node, and it must hold no
          interactive control — status implies aria-atomic, so any rerender
          would re-read the whole card, password included. */}
      <span role="status" className="sr-only">
        {opened
          ? `La campagne ${opened.address} vient d'être ouverte. ` +
            (opened.invitation_sent
              ? `Une invitation est partie à ${opened.coordination}. `
              : "") +
            // an invitation that did not leave changes what the
            // administrator has to do next, sighted or not
            (opened.invitation_error ? `${opened.invitation_error} ` : "") +
            "Le mot de passe de coordination est affiché à l'écran."
          : ""}
      </span>
      {opened && (
        <div className="carte">
          <h2>Campagne ouverte : {opened.address}</h2>
          {opened.invitation_sent ? (
            <p>
              Une invitation vient de partir à {opened.coordination} : le lien
              qu'elle contient ouvre l'accès, sans que vous ayez à transmettre
              quoi que ce soit.
            </p>
          ) : (
            <p>
              Transmettez ces accès à {opened.coordination}.
              {opened.invitation_error && (
                <>
                  {" "}
                  <strong>{opened.invitation_error}</strong>
                </>
              )}
            </p>
          )}
          <p>
            Le mot de passe n'est affiché <strong>qu'une seule fois</strong> —
            il n'est stocké nulle part en clair.
          </p>
          <p>
            <code>{opened.password}</code>
          </p>
          <button
            type="button"
            onClick={() => {
              // this button unmounts with its card: hand focus back first
              focusContenu();
              setOpened(null);
            }}
          >
            J'ai noté
          </button>
        </div>
      )}

      {pending.length === 0 && (
        <p className="gris">Aucune demande en attente.</p>
      )}
      {pending.map((d) => (
        <div className="carte" key={d.id}>
          <h2>{d.name}</h2>
          <p>
            <code>
              {d.slug}.{queue.base_domain}
            </code>{" "}
            — demandée par {d.requester_name} ({d.requester_email}), le {d.ts}
          </p>
          {d.message && <p>{d.message}</p>}
          <p className="gris">
            {d.listed
              ? "Souhaite apparaître dans l'annuaire public."
              : "Souhaite rester HORS de l'annuaire public."}
          </p>
          <p>
            <label>
              Motif (transmis au demandeur en cas de refus)
              <input
                type="text"
                value={reasons[d.id] ?? ""}
                onChange={(e) =>
                  setReasons({ ...reasons, [d.id]: e.target.value })
                }
              />
            </label>
          </p>
          <p>
            {/* one pair of buttons per request: the name says which.
                `busy !== null`, not `=== d.id`: ONE submit guard covers the
                whole queue, so while any card is in flight every other
                button would look live and have its click swallowed */}
            <button
              type="button"
              aria-disabled={busy !== null || undefined}
              onClick={() => decide(d, "accepted")}
            >
              Ouvrir la campagne
              <span className="sr-only"> {d.slug}</span>
            </button>{" "}
            <button
              type="button"
              className="lien"
              aria-disabled={busy !== null || undefined}
              onClick={() => decide(d, "refused")}
            >
              Refuser
              <span className="sr-only"> {d.slug}</span>
            </button>
          </p>
        </div>
      ))}

      <h2>Créer une campagne</h2>
      <p>
        Sans passer par le formulaire public : la campagne s'ouvre
        immédiatement, avec son compte de coordination.
      </p>
      <Creation
        baseDomain={queue.base_domain}
        onCreate={create}
        onMessage={onMessage}
      />

      <h2>Campagnes hébergées</h2>
      {/* in a card like every other table: it is also what lets a narrow
          screen scroll the table instead of the page */}
      <div className="carte">
        <table>
          <thead>
            <tr>
              <th scope="col">Adresse</th>
              <th scope="col">Nom</th>
              <th scope="col">État</th>
              <th scope="col">Depuis</th>
            </tr>
          </thead>
          <tbody>
            {queue.organisations.map((o) => (
              <tr key={o.id}>
                <td>
                  <code>
                    {o.slug}.{queue.base_domain}
                  </code>
                </td>
                <td>{o.name}</td>
                <td>{label(ORG_STATES, o.state)}</td>
                <td>{o.created_at}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <Traitees decided={decided} />
    </>
  );
}

// The creation form owns its fields so a submission can empty them: the
// password card above is what carries the result.
function Creation({
  baseDomain,
  onCreate,
  onMessage,
}: {
  baseDomain: string;
  onCreate: (c: {
    slug: string;
    name: string;
    coordination_email: string;
    coordination_name: string;
    listed: boolean;
  }) => Promise<void>;
  onMessage: (m: Message) => void;
}) {
  const [slug, setSlug] = useState("");
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [coordination, setCoordination] = useState("");
  const [listed, setListed] = useState(true);
  const [sending, setSending] = useState(false);
  const [creating, created] = useSubmitGuard();

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    // A REF: `sending` is a render behind, and this form OPENS A CAMPAIGN.
    // Two presses in one tick create two, and the password card keeps the
    // last of them.
    if (creating()) return;
    setSending(true);
    try {
      await onCreate({
        slug: slug.trim(),
        name: name.trim(),
        coordination_email: email.trim(),
        coordination_name: coordination.trim(),
        listed,
      });
      setSlug("");
      setName("");
      setEmail("");
      setCoordination("");
    } catch (err) {
      onMessage({ tone: "erreur", text: (err as Error).message });
    } finally {
      created();
      setSending(false);
    }
  };

  return (
    <form className="carte" onSubmit={submit}>
      <p>
        <label>
          Adresse (sous-domaine)
          <input
            type="text"
            required
            value={slug}
            onChange={(e) => setSlug(e.target.value)}
            aria-describedby="creation-adresse"
          />
        </label>
        <span id="creation-adresse" className="gris">
          {(slug.trim() || "votre-campagne") + "." + baseDomain}
        </span>
      </p>
      <p>
        <label>
          Nom de la campagne
          <input
            type="text"
            required
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </label>
      </p>
      <p>
        <label>
          <input
            type="checkbox"
            checked={listed}
            onChange={(e) => setListed(e.target.checked)}
          />{" "}
          Référencer la campagne dans l'annuaire public
        </label>
      </p>
      {/* the heading carries what these two fields are, so they can be
          named like every other account field in the app: a « Nom » right
          under « Nom de la campagne » only reads as a person's name once
          something says a person is being created */}
      <h3 className="groupe">Le compte de coordination</h3>
      <p className="gris">
        Il est créé avec la campagne : c'est lui qui se connectera, et son mot
        de passe ne s'affiche qu'une fois.
      </p>
      <p>
        <label>
          Nom
          <input
            type="text"
            required
            value={coordination}
            onChange={(e) => setCoordination(e.target.value)}
          />
        </label>
      </p>
      <p>
        <label>
          Adresse email
          <input
            type="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </label>
      </p>
      <button type="submit" aria-disabled={sending || undefined}>
        {sending ? "Création…" : "Créer la campagne"}
      </button>
    </form>
  );
}

function Traitees({ decided }: { decided: QueuedRequest[] }) {
  return (
    <>
      {decided.length > 0 && (
        <>
          <h2>Demandes traitées</h2>
          <div className="carte">
            <table>
              <thead>
                <tr>
                  <th scope="col">Adresse</th>
                  <th scope="col">Décision</th>
                  <th scope="col">Motif</th>
                  <th scope="col">Par</th>
                </tr>
              </thead>
              <tbody>
                {decided.map((d) => (
                  <tr key={d.id}>
                    <td>
                      <code>{d.slug}</code>
                    </td>
                    <td>{label(REQUEST_STATES, d.state)}</td>
                    <td>{d.reason}</td>
                    <td>{d.decided_by}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </>
  );
}
