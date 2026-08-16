import { useCallback, useEffect, useState } from "react";
import * as API from "./api.ts";
import {
  ChampLogo,
  ChampsCampagne,
  campaignLabel,
  focusContenu,
  label,
  ROLES,
  rescueFocusAfterCommit,
  useSubmitGuard,
} from "./common.tsx";
import type { Me, Message, ServerConfig, TeamData } from "./types.ts";

/** An access just opened, whose password is shown once and never again. */
interface NewAccess {
  email: string;
  name: string;
  role: string;
  password: string;
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
  const [sending, setSending] = useState(false);
  const [logoSending, setLogoSending] = useState(false);
  const [busy, done] = useSubmitGuard();

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    if (busy()) return; // a REF: state is a render behind
    setSending(true);
    try {
      const r = await API.updateCampaign(values, Number(batchSize), listed);
      onCfg({
        ...cfg,
        campaign: r.campaign,
        batch_size: r.batch_size,
        unfilled: r.unfilled,
        organisation: cfg.organisation && {
          ...cfg.organisation,
          listed: r.listed,
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
  onDecided: (r: NewAccess | null) => void;
}) {
  // one draft per request, seeded from what was asked
  const [drafts, setDrafts] = useState<
    Record<number, { name: string; departments: string[] }>
  >({});
  const [deciding, setDeciding] = useState<number | null>(null);
  const [busy, done] = useSubmitGuard();

  const pending = data.requests.filter((r) => r.state === "pending");
  const decided = data.requests.filter((r) => r.state !== "pending");
  if (data.requests.length === 0) return null;

  const draftOf = (id: number, asked: string) =>
    drafts[id] ?? {
      name: data.requests.find((r) => r.id === id)?.name ?? "",
      departments: asked ? asked.split(";") : [],
    };

  const decide = async (
    id: number,
    decision: "accepted" | "refused",
    draft: { name: string; departments: string[] },
  ) => {
    if (busy()) return;
    setDeciding(id);
    try {
      const r = await API.decideTeamRequest(id, decision, {
        name: draft.name,
        departments: draft.departments,
      });
      // the card carrying this button is about to vanish from the queue
      rescueFocusAfterCommit();
      // an acceptance opens the lead's access, and its password is shown
      // once, in the same card every new access uses
      onDecided(
        r.password
          ? {
              email: r.lead ?? "",
              name: `${r.name ?? draft.name} — ${r.lead ?? ""}`,
              role: "lead",
              password: r.password,
            }
          : null,
      );
    } catch (e) {
      onError(e);
    } finally {
      done();
      setDeciding(null);
    }
  };

  return (
    <>
      <h2>Demandes d'équipe ({pending.length})</h2>
      {pending.map((r) => {
        const draft = draftOf(r.id, r.departments);
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
            <button
              type="button"
              aria-disabled={deciding === r.id || undefined}
              onClick={() => decide(r.id, "accepted", draft)}
            >
              Accepter — ouvrir l'équipe
              <span className="sr-only"> {r.name}</span>
            </button>{" "}
            <button
              type="button"
              className="secondaire"
              aria-disabled={deciding === r.id || undefined}
              onClick={() => decide(r.id, "refused", draft)}
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
  const [created, setCreated] = useState<NewAccess | null>(null);
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
    try {
      const r = await API.createAccount({
        email: draft.email,
        name: draft.name,
        role: coordination ? draft.role : undefined,
        team_id:
          coordination && draft.team_id ? Number(draft.team_id) : undefined,
      });
      setCreated(r);
      setDraft({ email: "", name: "", role: "volunteer", team_id: "" });
      await reload();
    } catch (err) {
      onError(err);
    }
  };

  return (
    <>
      <h1>Mon équipe</h1>

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
        {created
          ? `Un accès vient d'être créé pour ${created.name}. Le mot de ` +
            "passe provisoire est affiché à l'écran, à transmettre de vive voix."
          : ""}
      </span>
      {created && (
        <div className="carte alerte">
          <p>
            <strong>
              Accès créé pour {created.name} ({created.email}).
            </strong>
          </p>
          <p>
            Mot de passe provisoire — <strong>affiché une seule fois</strong>, à
            transmettre de vive voix :
          </p>
          <p className="grand-tel">{created.password}</p>
          <button
            type="button"
            className="lien"
            onClick={() => {
              // this button unmounts with its card: hand focus back first
              focusContenu();
              setCreated(null);
            }}
          >
            j'ai noté
          </button>
        </div>
      )}

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
          onDecided={async (access) => {
            setCreated(access);
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
