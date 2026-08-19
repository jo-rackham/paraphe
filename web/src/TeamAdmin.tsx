import { useCallback, useEffect, useRef, useState } from "react";
import * as API from "./api.ts";
import {
  ChampLogo,
  ChampsCampagne,
  campaignLabel,
  focusContenu,
  gestionLabel,
  holdFocusThrough,
  label,
  ROLES,
  useSubmitGuard,
} from "./common.tsx";
import type {
  Me,
  Message,
  ServerConfig,
  TeamData,
  TeamRequest,
} from "./types.ts";

// What the live region says when an access opens. It names the ADDRESS as
// well as the person: two volunteers can share a name, and a sentence
// identical to the one already in the region is never read out again.
function creation(c: NewAccess): string {
  if (c.reset) {
    return (
      `Un nouveau mot de passe vient d'être tiré pour ${c.name} ` +
      `(${c.email}). Il est affiché à l'écran, une seule fois, à transmettre ` +
      "de vive voix. Les sessions ouvertes avec l'ancien sont fermées."
    );
  }
  return (
    `Un accès vient d'être créé pour ${c.name} (${c.email}). ` +
    (c.invitation_sent ? `Une invitation est partie à ${c.email}. ` : "") +
    // Said here too, and not only on the card: this announcement is what
    // somebody who cannot see the screen acts on, and an invitation that did
    // not leave changes what they must do next.
    (c.invitation_error ? `${c.invitation_error} ` : "") +
    "Le mot de passe provisoire est affiché à l'écran, à transmettre de " +
    "vive voix."
  );
}

// What a tick confirms: this row AND this address. Either changing is a new
// confirmation to make.
const seal = (r: TeamRequest) => `${r.id}:${r.requester_email}`;

/**
 * A one-time password on screen: an access just opened, or one drawn again
 * for somebody who lost theirs. Shown once and stored nowhere.
 */
interface NewAccess {
  /**
   * A COUNTER, not the address. Two cards can now name one person — draw a
   * password, forget to note it, draw another — and React calls duplicate
   * keys unsupported: the second card replaced the first in place, so the
   * password nobody had written down went off the only screen it existed on.
   * The same lesson the instance's moderation screen already paid for.
   */
  id: number;
  email: string;
  name: string;
  role?: string;
  password: string;
  /** drawn again for an existing account, rather than opening a new one */
  reset?: boolean;
  // The invitation the API tried to send with it. Absent when the instance
  // sends no email at all; false with a reason when the relay refused —
  // either way the one-time password below is what gets the person in.
  invitation_sent?: boolean;
  invitation_error?: string;
}

// Labels of the campaign keys. A campaign opened from a hosting request
// arrives with not a word filled in: this is where its coordination fills
// it — not in environment variables it has no hand on.
/** The banner names the missing keys: a volunteer reads a label, not a column. */

function ConfigurationCampagne({
  cfg,
  onCfg,
  onError,
  onMessage,
}: {
  cfg: ServerConfig;
  onCfg: (c: ServerConfig) => void;
  onError: (e: unknown) => void;
  onMessage: (m: Message) => void;
}) {
  const [values, setValues] = useState<Record<string, string>>(cfg.campaign);
  const [batchSize, setBatchSize] = useState(String(cfg.batch_size));
  const [listed, setListed] = useState(cfg.organisation?.listed ?? true);
  const [name, setName] = useState(cfg.organisation?.name ?? "");
  const [sending, setSending] = useState(false);
  const [logoSending, setLogoSending] = useState(false);
  const [busy, done] = useSubmitGuard();

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    if (busy()) return; // a REF: state is a render behind
    setSending(true);
    try {
      const r = await API.updateCampaign(
        values,
        Number(batchSize),
        listed,
        // Only where the field is shown. Sent from a screen that does not
        // offer it, an empty string would UNNAME the campaign — `nil` is
        // what leaves it alone, and that is the same rule as `listed`.
        cfg.organisation ? name : undefined,
      );
      onCfg({
        ...cfg,
        campaign: r.campaign,
        batch_size: r.batch_size,
        unfilled: r.unfilled,
        organisation: cfg.organisation && {
          ...cfg.organisation,
          listed: r.listed,
          name: r.name,
        },
      });
      onMessage({
        tone: "ok",
        text:
          r.unfilled.length === 0
            ? "Campagne enregistrée. Les messages sont prêts à partir."
            : "Campagne enregistrée. Restent à remplir : " +
              `${r.unfilled.map(campaignLabel).join(", ")}.`,
      });
    } catch (err) {
      onError(err);
    } finally {
      done();
      setSending(false);
    }
  };

  return (
    <form className="carte" onSubmit={save}>
      <h2 style={{ marginTop: 0 }}>La campagne</h2>
      <p className="gris">
        Ces valeurs remplissent les messages envoyés aux maires. Tant qu'il en
        manque, l'application le dit sur chaque page et le publipostage de masse
        refuse de tourner.
      </p>
      {/* The campaign's own name, and not the candidate's: it is what the
          header shows, what the instance's annuaire lists, and what an
          administrator moderated. Only where there IS an instance to be
          named on — a single-campaign deployment has no annuaire and no
          neighbour to be told apart from. */}
      {cfg.organisation && (
        <p>
          <label>
            Nom de la campagne
            <input
              value={name}
              maxLength={200}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
        </p>
      )}
      {/* h3 group titles: under this card's own h2 */}
      <ChampsCampagne
        values={values}
        groupe="h3"
        onEdit={(key, value) => setValues({ ...values, [key]: value })}
      />
      {/* Sent on its own, the moment a file is chosen, and NOT with the
          form: the campaign body already fills most of what a request may
          carry, and a volunteer who picks a logo then leaves the page
          without pressing "Enregistrer" has still chosen one. */}
      <ChampLogo
        logo={cfg.logo?.url ?? ""}
        occupe={logoSending}
        onErreur={(text) => onMessage({ tone: "erreur", text })}
        onChoisi={async (dataUri) => {
          setLogoSending(true);
          try {
            const r = await API.uploadLogo(dataUri);
            onCfg({ ...cfg, logo: r.logo });
            onMessage({ tone: "ok", text: "Logo enregistré." });
          } catch (err) {
            onError(err);
          } finally {
            setLogoSending(false);
          }
        }}
        onRetire={async () => {
          setLogoSending(true);
          try {
            await API.removeLogo();
            onCfg({ ...cfg, logo: null });
            onMessage({ tone: "ok", text: "Logo retiré." });
          } catch (err) {
            onError(err);
          } finally {
            setLogoSending(false);
          }
        }}
      />
      <p>
        <label>
          Maires attribués par « prendre un lot »
          <input
            type="number"
            min={1}
            max={100}
            value={batchSize}
            onChange={(e) => setBatchSize(e.target.value)}
          />
        </label>
      </p>
      {cfg.organisation && (
        <>
          <p>
            <label>
              <input
                type="checkbox"
                checked={listed}
                onChange={(e) => setListed(e.target.checked)}
              />{" "}
              Référencer la campagne dans l'annuaire public de l'instance
            </label>
          </p>
          <p className="gris">
            Décochée, l'adresse de la campagne n'apparaît pas sur l'accueil de
            l'instance — elle reste joignable par qui la connaît. L'annuaire ne
            l'affiche de toute façon qu'une fois la campagne nommée ci-dessus.
          </p>
        </>
      )}
      <button type="submit" aria-disabled={sending || undefined}>
        {sending ? "Enregistrement…" : "Enregistrer la campagne"}
      </button>
    </form>
  );
}

/**
 * The moderation queue: requests to open a local team, and what the
 * coordination actually opens.
 *
 * The name and the perimeter are EDITABLE here. The person who filled the
 * form knows their department, not the campaign's map, and a coordination
 * that can only accept or refuse ends up refusing a good request because the
 * name is wrong.
 */
function DemandesEquipes({
  data,
  onError,
  onDecided,
}: {
  data: TeamData;
  onError: (e: unknown) => void;
  onDecided: (
    decided: { id: number; state: "accepted" | "refused" },
    // WITHOUT the card key: the counter belongs to whoever owns the list,
    // and a child minting one would be a second source of the same number.
    r: Omit<NewAccess, "id"> | null,
    said: string,
  ) => Promise<void>;
}) {
  // one draft per request, seeded from what was asked
  const [drafts, setDrafts] = useState<
    Record<number, { name: string; departments: string[] }>
  >({});
  const [deciding, setDeciding] = useState<number | null>(null);
  // Ticked per card, and only the ACCEPT path reads it: accepting sends a
  // session link to an address a stranger typed.
  //
  // Keyed by the id AND the address, not by the id alone. What the
  // coordination confirmed is an ADDRESS; a key naming only the row would
  // carry the tick over to a different address the day one is edited, or
  // share it between two rows that came back with the same identity. The
  // whole point of the box is that the thing confirmed is the thing sent to.
  const [verified, setVerified] = useState<Record<string, boolean>>({});
  const [busy, done] = useSubmitGuard();

  if (data.requests.length === 0) return null;
  const pending = data.requests.filter((r) => r.state === "pending");
  const decided = data.requests.filter((r) => r.state !== "pending");

  const decide = async (
    id: number,
    decision: "accepted" | "refused",
    draft: { name: string; departments: string[] },
    seal: string,
  ) => {
    // BEFORE the submit guard, which TAKES it as a side effect: refusing
    // after it would leave the guard held by a click that did nothing, and
    // every later decision in the queue would be swallowed for good.
    //
    // What the greyed button shows, enforced: aria-disabled leaves a control
    // clickable on purpose, so the refusal has to live in the handler too.
    if (decision === "accepted" && !verified[seal]) return;
    if (busy()) return;
    setDeciding(id);
    // captured BEFORE the round trip: this card leaves the queue when the
    // reload lands, and by then the button under the moderator's finger is
    // gone and focus is on <body>
    const restoreFocus = holdFocusThrough();
    try {
      const r = await API.decideTeamRequest(id, decision, {
        name: draft.name,
        departments: draft.departments,
      });
      // an acceptance opens the lead's access, and its password is shown
      // once, in the same card every new access uses. A refusal opens
      // nothing, and the three fields come back together or not at all.
      const lead = r.lead ?? "";
      await onDecided(
        { id, state: decision },
        r.password
          ? {
              email: lead,
              name: `${r.name ?? draft.name} — ${lead}`,
              role: "lead",
              password: r.password,
            }
          : null,
        decision === "accepted"
          ? `L'équipe « ${r.name ?? draft.name} » est ouverte.`
          : // an acceptance shows the password card, which says it happened;
            // a refusal leaves nothing on screen but one card fewer
            `Demande refusée : « ${draft.name} ».`,
      );
    } catch (e) {
      onError(e);
    } finally {
      done();
      setDeciding(null);
      // in the `finally`, not after the await: the card leaves the pending
      // list as soon as the decision answers, so a reload that THROWS still
      // unmounts the button under the moderator's finger
      restoreFocus();
    }
  };

  return (
    <>
      <h2>Demandes d'équipe ({pending.length})</h2>
      {pending.map((r) => {
        // seeded from what was asked, until the coordination edits it
        const draft = drafts[r.id] ?? {
          name: r.name,
          departments: r.departments ? r.departments.split(";") : [],
        };
        return (
          <div className="carte" key={r.id}>
            <h3 style={{ marginTop: 0 }}>{r.name}</h3>
            <p className="gris">
              Demandée par {r.requester_name} ({r.requester_email}) le {r.ts} —
              périmètre souhaité :{" "}
              {r.departments.split(";").join(", ") || "toute la France"}
            </p>
            {r.message && <p>{r.message}</p>}
            <p>
              <label>
                Nom de l'équipe ouverte
                <input
                  type="text"
                  value={draft.name}
                  onChange={(e) =>
                    setDrafts({
                      ...drafts,
                      [r.id]: { ...draft, name: e.target.value },
                    })
                  }
                />
              </label>
            </p>
            <p>
              <label>
                Départements accordés
                <select
                  multiple
                  size={5}
                  value={draft.departments}
                  onChange={(e) =>
                    setDrafts({
                      ...drafts,
                      [r.id]: {
                        ...draft,
                        departments: [...e.target.selectedOptions].map(
                          (o) => o.value,
                        ),
                      },
                    })
                  }
                >
                  {data.departments.map((d) => (
                    <option key={d} value={d}>
                      {d}
                    </option>
                  ))}
                </select>
              </label>
            </p>
            {/* Accepting SENDS: an email signed by the campaign leaves for
                an address a stranger typed, carrying a link that opens the
                lead's session. The address is on the card, but a queue is
                read fast — this puts it back under the eye at the moment of
                the decision. A refusal sends nothing and needs none of it. */}
            <p>
              <label>
                <input
                  type="checkbox"
                  checked={verified[seal(r)] ?? false}
                  onChange={(e) =>
                    setVerified({ ...verified, [seal(r)]: e.target.checked })
                  }
                />{" "}
                {/* named, and pointed at by the button below: reached by
                    « next button » rather than by Tab, an inert control says
                    only « indisponible » and nothing ties it to what would
                    make it live again */}
                <span id={`confirmation-${r.id}`}>
                  J'ai vérifié que <strong>{r.requester_email}</strong> est bien
                  l'adresse de la personne qui demande : l'ouverture lui envoie
                  un lien qui ouvre sa session de référent.
                </span>
              </label>
            </p>
            {/* `deciding !== null`, not `=== r.id`: ONE submit guard covers
                the whole list, so while any card is in flight every other
                button would look live and have its click swallowed */}
            <button
              type="button"
              aria-disabled={
                deciding !== null || !verified[seal(r)] || undefined
              }
              aria-describedby={`confirmation-${r.id}`}
              onClick={() => decide(r.id, "accepted", draft, seal(r))}
            >
              Accepter — ouvrir l'équipe
              <span className="sr-only"> {r.name}</span>
            </button>{" "}
            <button
              type="button"
              className="secondaire"
              aria-disabled={deciding !== null || undefined}
              onClick={() => decide(r.id, "refused", draft, seal(r))}
            >
              Refuser
              <span className="sr-only"> {r.name}</span>
            </button>
          </div>
        );
      })}
      {decided.length > 0 && (
        <div className="carte">
          <table>
            <thead>
              <tr>
                <th scope="col">Équipe demandée</th>
                <th scope="col">Demandeur</th>
                <th scope="col">Décision</th>
                <th scope="col">Motif</th>
              </tr>
            </thead>
            <tbody>
              {decided.map((r) => (
                <tr key={r.id}>
                  <td>{r.name}</td>
                  <td className="gris">{r.requester_email}</td>
                  <td>{r.state === "accepted" ? "Acceptée" : "Refusée"}</td>
                  <td className="gris">{r.reason || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

export function GestionEquipe({
  onError,
  me,
  cfg,
  onCfg,
  onMessage,
}: {
  onError: (e: unknown) => void;
  me: Me;
  cfg: ServerConfig;
  onCfg: (c: ServerConfig) => void;
  onMessage: (m: Message) => void;
}) {
  const [data, setData] = useState<TeamData | null>(null);
  // A LIST, and appended to. A single slot is a slot the next password
  // overwrites, and two flows mint one here: opening an access directly, and
  // accepting a team request — each with its own re-entry guard, so neither
  // sees the other. It did not even need a race: opening a second access
  // while the first password was still on screen wiped it. An append cannot
  // lose one, and a guard read at the top of a handler cannot say the same.
  const [created, setCreated] = useState<NewAccess[]>([]);
  // The key of the next card. A REF and not state: it is read and bumped
  // inside handlers, never rendered, and a state bump would be a second
  // render for a number nobody looks at.
  const nextCard = useRef(0);
  // The announcement is an EVENT, written only when a password is APPENDED.
  // Derived from the list instead, it moved every time a card was dismissed:
  // noting the newest made the region announce the creation of the access
  // before it — a creation that did not happen, and a wrong password to
  // attribute. The count is state, not an event, so it stays OUT of here.
  const [announced, setAnnounced] = useState("");
  const [draft, setDraft] = useState({
    email: "",
    name: "",
    role: "volunteer",
    team_id: "",
  });
  const [team, setTeam] = useState<{ name: string; departments: string[] }>({
    name: "",
    departments: [],
  });
  // A REF, and it guards the route that OPENS AN ACCESS. Two clicks in the
  // same tick run two handlers built by the same render: two accounts, two
  // invitations, and `setCreated` keeps the last — so the first volunteer's
  // password, shown once and never stored, is gone from the screen that was
  // the only place it existed.
  const [openingAccess, accessOpened] = useSubmitGuard();
  // …and one for drawing a password again. Its own guard, not the one above:
  // they are different rows and different acts, and a shared flag would make
  // opening an access refuse a reset that happened to overlap it. Two clicks
  // on THIS one draw two passwords and the first is replaced by the second
  // — with the first still on screen, which is a password that opens nothing.
  const [drawing, drawn] = useSubmitGuard();

  const reload = useCallback(async () => {
    try {
      setData(await API.team());
    } catch (e) {
      onError(e);
    }
  }, [onError]);
  useEffect(() => {
    reload();
  }, [reload]);

  if (!data) return <p role="status">Chargement…</p>;
  const coordination = me.account.role === "coordination";

  const createAccount = async (e: React.FormEvent) => {
    e.preventDefault();
    if (openingAccess()) return;
    try {
      const r = await API.createAccount({
        email: draft.email,
        name: draft.name,
        role: coordination ? draft.role : undefined,
        team_id:
          coordination && draft.team_id ? Number(draft.team_id) : undefined,
      });
      nextCard.current += 1;
      const card = { ...r, id: nextCard.current };
      setCreated((shown) => [...shown, card]);
      setAnnounced(creation(card));
      setDraft({ email: "", name: "", role: "volunteer", team_id: "" });
      await reload();
    } catch (err) {
      onError(err);
    } finally {
      accessOpened();
    }
  };

  return (
    <>
      {/* Through the SAME function as the tab that opened it: a heading
          that disagrees with the tab is how a reader concludes they landed
          on the wrong screen. */}
      <h1>{gestionLabel(me)}</h1>

      {coordination && (
        <ConfigurationCampagne
          cfg={cfg}
          onCfg={onCfg}
          onError={onError}
          onMessage={onMessage}
        />
      )}

      {/* The announcement is a PERSISTENT text-only region beside the
          card, never the card itself: a live region is reliable only when
          its content changes inside an existing node, and it must hold no
          interactive control — status implies aria-atomic, so any rerender
          would re-read the whole card, password included. */}
      <span role="status" className="sr-only">
        {announced}
      </span>
      {/* how many wait: ordinary text, in the document for everyone, and
          carrying NO live role — it is a state, and a state that moved
          inside the region above made every dismissal re-read a creation */}
      {created.length > 1 && (
        <p className="gris">
          {created.length} mots de passe sont affichés et n'ont pas encore été
          notés.
        </p>
      )}
      {created.map((c) => (
        <div className="carte alerte" key={c.id}>
          <p>
            <strong>
              {c.reset ? "Nouveau mot de passe pour" : "Accès créé pour"}{" "}
              {c.name} ({c.email}).
            </strong>
          </p>
          {c.reset && (
            <p>
              Les sessions ouvertes avec l'ancien mot de passe sont fermées : si
              quelqu'un d'autre l'avait, il est dehors.
            </p>
          )}
          {c.invitation_sent && (
            <p>
              Une invitation vient de partir à cette adresse : le lien qu'elle
              contient ouvre l'accès sans mot de passe.
            </p>
          )}
          {c.invitation_error && (
            <p>
              <strong>{c.invitation_error}</strong>
            </p>
          )}
          {/* The password stays on screen whatever the invitation did: a
              relay can be down tomorrow, and reading it out is the path
              that has always worked. */}
          <p>
            Mot de passe provisoire — <strong>affiché une seule fois</strong>, à
            transmettre de vive voix :
          </p>
          <p className="grand-tel">{c.password}</p>
          <button
            type="button"
            className="lien"
            onClick={() => {
              // this button unmounts with its card: hand focus back first
              focusContenu();
              setCreated((shown) => shown.filter((x) => x !== c));
            }}
          >
            j'ai noté
            {/* several of these stand at once: without the address, a
                screen reader enumerates N buttons of one name and none of
                them says which card it closes */}
            <span className="sr-only"> {c.email}</span>
          </button>
        </div>
      ))}

      <div className="carte">
        <h2 style={{ marginTop: 0 }}>Ouvrir un accès</h2>
        <form className="enligne" onSubmit={createAccount}>
          <div>
            <label>
              Nom
              <input
                type="text"
                required
                value={draft.name}
                onChange={(e) => setDraft({ ...draft, name: e.target.value })}
              />
            </label>
          </div>
          <div>
            <label>
              Adresse email
              <input
                type="text"
                required
                value={draft.email}
                onChange={(e) => setDraft({ ...draft, email: e.target.value })}
              />
            </label>
          </div>
          {coordination && (
            <>
              <div>
                <label>
                  Rôle
                  <select
                    value={draft.role}
                    onChange={(e) =>
                      setDraft({ ...draft, role: e.target.value })
                    }
                  >
                    <option value="volunteer">Bénévole</option>
                    <option value="lead">Référent</option>
                    <option value="coordination">Coordination</option>
                  </select>
                </label>
              </div>
              <div>
                <label>
                  Équipe
                  <select
                    value={draft.team_id}
                    onChange={(e) =>
                      setDraft({ ...draft, team_id: e.target.value })
                    }
                  >
                    <option value="">— nationale —</option>
                    {data.teams.map((g) => (
                      <option key={g.id} value={g.id}>
                        {g.name}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
            </>
          )}
          <button type="submit">Créer</button>
        </form>
        {!coordination && (
          <p className="gris">
            En tant que référent, vous ouvrez des accès bénévoles dans votre
            équipe.
          </p>
        )}
      </div>

      {coordination && (
        <DemandesEquipes
          data={data}
          onError={onError}
          onDecided={async (decided, access, said) => {
            // The server has answered: the card leaves the pending list HERE,
            // not when the reload lands. A reload that fails — a blip, a 5xx —
            // would otherwise leave the one-time password beside the very
            // request it answers, and a moderator discards a password that
            // contradicts the screen. It is shown once and never again.
            setData(
              (d) =>
                d && {
                  ...d,
                  requests: d.requests.map((r) =>
                    r.id === decided.id ? { ...r, state: decided.state } : r,
                  ),
                },
            );
            // appended, never assigned: a moderator accepting a second
            // request before noting the first password must not lose it
            if (access) {
              nextCard.current += 1;
              const card = { ...access, id: nextCard.current };
              setCreated((shown) => [...shown, card]);
              setAnnounced(creation(card));
            }
            // through the SHELL's region, which pre-exists this screen: an
            // acceptance leaves a password card that says what happened, a
            // refusal leaves nothing but one card fewer
            onMessage({ tone: "ok", text: said });
            await reload();
          }}
        />
      )}

      <h2>Les accès ({data.accounts.length})</h2>
      <div className="carte">
        <table>
          <thead>
            <tr>
              <th scope="col">Nom</th>
              <th scope="col">Adresse</th>
              <th scope="col">Rôle</th>
              <th scope="col">Équipe</th>
              <th scope="col">
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {data.accounts.map((c) => (
              // muted, not faded: opacity .5 halved every contrast in the
              // row, réactiver button included — and made the state a
              // colour-only signal. The word carries it now.
              <tr key={c.email} className={c.active ? undefined : "inactif"}>
                <td>
                  {c.name}
                  {!c.active && <span className="gris"> (désactivé)</span>}
                </td>
                <td className="gris">{c.email}</td>
                <td>{label(ROLES, c.role)}</td>
                <td>{c.team ?? "—"}</td>
                <td>
                  {c.email !== me.account.email && (
                    <>
                      <button
                        type="button"
                        className="lien"
                        onClick={async () => {
                          try {
                            await API.toggleAccount(c.email);
                            await reload();
                          } catch (e) {
                            onError(e);
                          }
                        }}
                      >
                        {c.active ? "désactiver" : "réactiver"}
                        {/* nine identical buttons in a column: the name says
                            whose access this one touches */}
                        <span className="sr-only"> {c.name}</span>
                      </button>
                      {" · "}
                      {/* The door the sign-in screen has always promised —
                          « le mot de passe n'est affiché qu'une fois à sa
                          création : s'il est perdu, il faut en regénérer un »
                          — and that no button offered. The server draws it,
                          shows it once, and closes the sessions opened with
                          the old one. */}
                      <button
                        type="button"
                        className="lien"
                        onClick={async () => {
                          if (drawing()) return;
                          try {
                            const r = await API.resetPassword(c.email);
                            nextCard.current += 1;
                            const card = {
                              ...r,
                              id: nextCard.current,
                              reset: true,
                            };
                            setCreated((shown) => [...shown, card]);
                            setAnnounced(creation(card));
                          } catch (e) {
                            onError(e);
                          } finally {
                            drawn();
                          }
                        }}
                      >
                        nouveau mot de passe
                        <span className="sr-only"> {c.name}</span>
                      </button>
                      {/* a campaign carries as many coordinators as it
                          trusts: promotion is coordination's, and stepping
                          someone down needs another coordinator to remain —
                          the server refuses the last one (409) */}
                      {coordination && (
                        <>
                          {" · "}
                          <button
                            type="button"
                            className="lien"
                            onClick={async () => {
                              try {
                                await API.changeRole(
                                  c.email,
                                  c.role === "coordination"
                                    ? "volunteer"
                                    : "coordination",
                                );
                                await reload();
                              } catch (e) {
                                onError(e);
                              }
                            }}
                          >
                            {c.role === "coordination"
                              ? "rendre bénévole"
                              : "promouvoir coordination"}
                            <span className="sr-only"> {c.name}</span>
                          </button>
                        </>
                      )}
                    </>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {coordination && (
        <>
          <h2>Les équipes locales</h2>
          <div className="carte">
            <form
              className="enligne"
              onSubmit={async (e) => {
                e.preventDefault();
                try {
                  await API.createTeam(team.name, team.departments);
                  setTeam({ name: "", departments: [] });
                  await reload();
                } catch (err) {
                  onError(err);
                }
              }}
            >
              <div>
                <label>
                  Nom de l'équipe
                  <input
                    type="text"
                    required
                    value={team.name}
                    onChange={(e) => setTeam({ ...team, name: e.target.value })}
                  />
                </label>
              </div>
              <div>
                <label>
                  Départements (plusieurs possibles)
                  <select
                    multiple
                    size={5}
                    value={team.departments}
                    onChange={(e) =>
                      setTeam({
                        ...team,
                        departments: [...e.target.selectedOptions].map(
                          (o) => o.value,
                        ),
                      })
                    }
                  >
                    {data.departments.map((d) => (
                      <option key={d} value={d}>
                        {d}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
              <button type="submit">Créer l'équipe</button>
            </form>
            <p className="gris">
              Une équipe ne pioche que dans ses départements et ne voit que son
              propre travail. Sans départements, elle travaille partout.
            </p>
            <table>
              <thead>
                <tr>
                  <th scope="col">Équipe</th>
                  <th scope="col">Départements</th>
                  <th scope="col">Membres</th>
                  <th scope="col">Fiches</th>
                </tr>
              </thead>
              <tbody>
                {data.teams.map((g) => (
                  <tr key={g.id}>
                    <td>{g.name}</td>
                    <td className="gris">
                      {g.departments || "toute la France"}
                    </td>
                    <td>{g.members}</td>
                    <td>{g.reserved}</td>
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
