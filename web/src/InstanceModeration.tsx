import { useEffect, useState } from "react";
import * as API from "./api.ts";
import {
  focusContenu,
  label,
  ORG_STATES,
  REQUEST_STATES,
  rescueFocusAfterCommit,
} from "./common.tsx";
import type { Message, ModerationQueue, QueuedRequest } from "./types.ts";

export function Moderation({ onMessage }: { onMessage: (m: Message) => void }) {
  const [queue, setQueue] = useState<ModerationQueue | null>(null);
  const [busy, setBusy] = useState<number | null>(null);
  const [reasons, setReasons] = useState<Record<number, string>>({});
  // The coordination password is returned only ONCE: it does not go back
  // to the database in the clear, and there is no way to retrieve it
  // afterwards.
  const [opened, setOpened] = useState<{
    address: string;
    coordination: string;
    password: string;
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
    if (busy !== null) return; // aria-disabled keeps the pair focusable
    setBusy(d.id);
    try {
      const rep = await API.decideRequest(d.id, decision, reasons[d.id] ?? "");
      if (rep.password && rep.address && rep.coordination) {
        setOpened({
          address: rep.address,
          coordination: rep.coordination,
          password: rep.password,
        });
      } else {
        onMessage({ tone: "ok", text: `Demande ${d.slug} refusée.` });
      }
      await load();
    } catch (e) {
      onMessage({ tone: "erreur", text: (e as Error).message });
    } finally {
      setBusy(null);
      // the decided card unmounts with the very button that decided it
      rescueFocusAfterCommit();
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
          ? `La campagne ${opened.address} vient d'être ouverte. Le mot de ` +
            "passe de coordination est affiché à l'écran."
          : ""}
      </span>
      {opened && (
        <div className="carte">
          <h2>Campagne ouverte : {opened.address}</h2>
          <p>
            Transmettez ces accès à {opened.coordination}. Le mot de passe n'est
            affiché <strong>qu'une seule fois</strong> — il n'est stocké nulle
            part en clair.
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
            {/* one pair of buttons per request: the name says which */}
            <button
              type="button"
              aria-disabled={busy === d.id || undefined}
              onClick={() => decide(d, "accepted")}
            >
              Ouvrir la campagne
              <span className="sr-only"> {d.slug}</span>
            </button>{" "}
            <button
              type="button"
              className="lien"
              aria-disabled={busy === d.id || undefined}
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

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (sending) return; // aria-disabled greys the button but keeps it live
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
          Email de la coordination
          <input
            type="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </label>
      </p>
      <p>
        <label>
          Nom de la coordination
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
          <input
            type="checkbox"
            checked={listed}
            onChange={(e) => setListed(e.target.checked)}
          />{" "}
          Référencer la campagne dans l'annuaire public
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
