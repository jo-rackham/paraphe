# paraphe — tooling for a French presidential endorsement drive

Running for the French presidency requires **500 endorsements from elected
officials**, drawn from at least 30 departments, with a cap of 50 per
department. Since 2017 every endorsement is **published by name**. Many mayors
no longer dare to sign, and serious candidacies stop before the debate can
happen.

This repository holds the whole tooling for such a drive: crossing open data
to find the officials most likely to listen, generating personalised messages,
and a web application so a team of volunteers can share the work without
stepping on each other.

**It belongs to no candidate.** The candidate's name, the texts and the
contact details are parameters: the same code serves any candidacy, from any
part of the political spectrum. It is a tool for pluralism, not a campaign for
anyone in particular — see [DEMARCHE.md](DEMARCHE.md) (in French, as is the
volunteers' guide: the campaign-facing documents are written for the people
who use them).

## What it does

1. **Identify** — crosses three open datasets (endorsements published by the
   Conseil constitutionnel in 2017 and 2022, the national register of elected
   officials, the public service directory) to produce the list of mayors
   **still in office** who have already endorsed a little-known candidacy,
   with their town hall's email, phone, opening hours and address.
2. **Write** — generates an email, a printable letter and a phone script for
   each official, from editable templates.
3. **Organise** — a web application where volunteers reserve batches, track
   statuses and share notes, in walled local teams. Stateless, backed by
   PostgreSQL: it runs as several instances, and a Helm chart deploys it on
   Kubernetes with CloudNativePG.

## Two versions

One interface (`web/`, React), two modes. What decides is whether an API
answers behind the page — at run time, not at build time.

| | **Team** (`api/` + interface) | **Browser** (interface alone) |
|---|---|---|
| For whom | a team, up to a few dozen | one person, or two or three |
| Coordination | reservations, per-team walls, shared notes | none — that is its limit |
| Data | PostgreSQL, on your server | **nothing leaves the browser** |
| Install | Docker or Kubernetes | a web page, nothing to install |
| Mayor list | embedded at build time | published next to the app |

The browser version coordinates nothing: it cannot know whether another
volunteer has already called the same mayor. That is the price of maximum
confidentiality, and the interface says so. For a campaign of several dozen
people, the server version avoids duplicates — the worst possible misstep with
an elected official.

The browser version loads the **priority list** (1,972 mayors, 139 kB) by
default and offers the **full base** (34,826 mayors, 2 MB) in one click. You
can also load your own file: the published lists are a convenience, they age,
and `task all` regenerates them.

## Getting started

```bash
devbox install && task
```

`task` alone lists every command. The main ones:

| Command | Effect |
|---|---|
| `task all` | downloads the open sources and builds every list |
| `task messages` | generates the mailing (CSV emails, HTML letters) |
| `task build` | crosses the sources again without re-downloading |
| `task db` | starts PostgreSQL locally (required by `task api`) |
| `task api` | runs the team API on http://127.0.0.1:8047 |
| `task web` | the interface in development (http://127.0.0.1:5180) |
| `task test` | every test: tools and core (TS), API (Go), interface (TS) |
| `task deploy` | builds and runs the app in Docker |

Fill in `config/campagne.yaml` (candidate identity, contacts, signature)
before generating anything — otherwise the scripts refuse to run rather than
send "Prénom NOM" to thousands of officials.

Deployment, accounts and the per-team walls are described in
[DEPLOYMENT.md](DEPLOYMENT.md). The volunteers' handbook is
[GUIDE.md](GUIDE.md), and the application serves it itself.

## How it is built

**Two languages, not three.** Go for the server, TypeScript for everything
else:

- `noyau/` — **TypeScript, no dependencies**: the message engine, CSV reading
  and writing, name normalisation. One implementation, shared by the interface
  and the tools, so an invariant cannot be broken in one copy and not the
  other.
- `outils/` — **TypeScript run by Node**, no compilation
  (`node outils/build.ts`): the crossing of the open sources and the mass
  mailing.
- `api/` — **Go**: the team application's JSON API. Stateless, several
  instances in front of one database. It renders no HTML and generates no
  message.
- `web/` — **React + Vite**: the interface, in its modes. It has its own
  image, which serves the pages and passes `/api` to the API's — two images,
  always at the same tag.
- `outils/telecharger.sh` — bash, because downloading four files deserves no
  more.

## Data sources

All open, fetched by `task download`:

- **2017 and 2022 endorsements** — Conseil constitutionnel via data.gouv.fr
  (public domain). The final nominative publication: official, office,
  commune, candidate endorsed.
- **National register of elected officials** — Ministry of the Interior, the
  mayors file, updated continuously (Etalab Open Licence 2.0).
- **Public service directory** — DILA, `api-lannuaire.service-public.fr`:
  each town hall's official contact details, keyed by INSEE code.

No scraping, and no personal data collected outside those official
publications. The contact details used are the **town hall's**, never a
private person's.

Raw data and produced lists are **not versioned**: one command regenerates
them, and a frozen file ages (the register and the directory change
continuously). CI rebuilds them at each release and serves them next to the
browser version, with a `robots.txt` that discourages harvesting — those
details are public and meant to be used, but an indexed directory would become
a target for commercial soliciting, which would harm the officials and the
drive alike.

## What is delicate, and how it is handled

Crossing civil-status files is a false-positive exercise, and here a false
positive has a real cost: writing "thank you for your endorsement" to someone
who never signed discredits the whole approach.

- An endorser's identity is **commune + surname + first name**, never the
  surname alone: family successions at the town hall are common.
- The **sex code** of the national register settles what spelling confuses
  (Christian → Christine).
- What is not certain goes to a "check by hand" file rather than being
  asserted either way.
- **The message depends on what is known**: an official with no endorsement
  history receives a text that presumes nothing about them.

The rules that decide the crossing are in `CLAUDE.md`, and the report produced
by `task build` lists every candidate with its total and its class.

## Licence

MIT — see [LICENSE](LICENSE). Take it, adapt it, fork it for your own
campaign: that is the point.
