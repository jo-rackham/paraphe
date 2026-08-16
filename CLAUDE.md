# paraphe — reaching the mayors who endorse small candidates

Find the mayors who endorsed little-known candidates since 2017, who are
**still in office** after the March 2026 municipal elections, with their town
hall's contact details — so a 2027 campaign can approach them first.

`devbox install && task` lists every command. `task all` rebuilds everything
(download + build), `task messages` produces the mass mailing in
`out/messages/`, `task api` serves the team API on http://127.0.0.1:8047,
`task web` serves the interface on :5180 with `/api` proxied to it.

## Layout

- `config/campagne.yaml` — candidate, signatory, contacts. **To be filled in.**
- `modeles/*.txt` — the message templates (email, letter, phone), editable
  without touching code. Placeholders are checked: an unknown one raises.
  `modeles/*.md` are the per-channel strategy notes. Single source, imported
  by both the interface and the mass mailing.
- `noyau/` — TypeScript, **no dependencies**. The message engine (gendered
  salutation, French dates, the volunteer's personal touch), CSV reading and
  writing, name normalisation, and a port of `difflib.SequenceMatcher`.
  Shared by `web/` and `outils/`.
- `outils/` — TypeScript run by Node, no compilation: `build.ts` crosses the
  sources, `messages-masse.ts` does the mailing, `faux-jeu.ts` builds the
  synthetic dataset for CI, `telecharger.sh` downloads.
- `api/` — **Go JSON API** (pgx, `PARAPHE_DATABASE_URL`; `task db` starts a
  local database). It renders no HTML and generates no message. Stateless:
  several instances in front of one database.
- `web/` — **React + TypeScript**, three modes. `App.tsx` asks `/api/config`
  at load: an API answering with a campaign → team mode; answering
  "instance" → the public apex and its moderation (`Instance.tsx`); nothing
  answering → browser mode, everything in IndexedDB. The mode is chosen at
  RUN time, not at build time — a build with the wrong flag would only show
  in production. `common.tsx` holds the mayor card (hence message
  generation) and the guide, shared by every mode.
- `GUIDE.md` — the method, for the team. Imported verbatim by the interface
  (`?raw` + marked), so it renders in every mode, and it is the landing page
  after sign-in.
- `DEPLOYMENT.md`, `api/Dockerfile`, `docker-compose.yml`, `chart/`.

## One image, one process

`api/Dockerfile`, **amd64**, `FROM scratch`: 22 MB holding the binary, the
built interface, the templates and the mayor list. One container in the pod,
one service in compose, one port.

It was two images — one nginx serving the pages and proxying `/api` to the
other — held together by the discipline of tagging them alike. Merging them
buys three things, and the first is why:

- **Whoever serves a response is who sets its headers.** Splitting them once
  left every PAGE without a Content-Security-Policy while the API kept its
  own, and it took a measurement on the built image to notice.
  `securityHeaders` now wraps the whole router, pages included, and there is
  no second implementation to hold in step.
- **The page-serving path the tests exercise is the one production runs.**
  With nginx in front, the 35 end-to-end journeys drove Go's
  `serveInterface` and production drove an nginx template.
- An interface of one version talking to an API of another stops being a
  failure to avoid and becomes a situation that does not exist.

- **The mode marker** (`<meta name="paraphe-mode" content="team">`) is
  injected at STARTUP, in memory, by `markInterface`. Without it, a passing
  failure of `/api/config` drops a volunteer into browser mode and their
  team's work goes to IndexedDB.
- **`PARAPHE_WEB_DIR` unreadable FAILS the start.** One image serves both, so
  an interface that cannot be read is a broken image, not a deployment shape
  — answering 404 on every page while `/api` works is what a readiness probe
  calls healthy and a volunteer calls a blank screen. Set EXPLICITLY empty,
  it means "JSON only", which is what a developer has before the first
  `task web-build`.
- **The assets are precompressed at build time**, brotli and gzip beside each
  original; the server negotiates on `Accept-Encoding`. 357 kB of bundle
  leaves as 90. nginx shipped `#gzip on;` commented out and served all 357.
- **Nothing is written to disk.** The final stage has no shell, no package
  manager and no writable filesystem. The application's own pods mount no
  volume; the only ones the chart gives a volume to are the object store's
  (see below), and the application never touches them except over S3.

## Sources (all open)

Public domain for the Conseil constitutionnel endorsements, Etalab open
licence for the RNE.

- **Endorsements 2017 and 2022**: Conseil constitutionnel via data.gouv.fr —
  the final nominative publication (official, office, commune, candidate).
- **RNE, mayors file**: Ministry of the Interior via data.gouv.fr, updated
  continuously (state after the 2026 municipal elections).
- **Public service directory** (DILA, api-lannuaire.service-public.fr):
  email, address, phone and website of each town hall, keyed by INSEE code.
  This is THE contact source — no need to scrape municipal websites.

## Locked decisions

- **All code and technical documentation are in English**: identifiers,
  comments, SQL, JSON keys, routes, CSV headers, status/rank/role values,
  `PARAPHE_*` variables, `CLAUDE.md`, `README.md`, `DEPLOYMENT.md`,
  `APPROACH.md`.
  **French is for what the end user sees**: interface strings, error messages
  shown in the app, the `{placeholders}` of the templates, the keys of
  `campagne.yaml`, the comments of `chart/values.yaml` and `.env.exemple`
  (read by whoever runs a campaign), the generated file names
  (`01_maires_cibles_prioritaires.csv`…), `rapport.md`, `GUIDE.md` and
  `modeles/*.md`.
  `PARAPHE_BASE_PATH` is Vite's base path, not to be confused with
  `PARAPHE_BASE_DOMAIN` — hence the suffix.
- **Candidate classification** lives in `outils/build.ts` (TIER_A / TIER_B),
  transparent and adjustable; the report lists every candidate with its total
  and its class. A = real candidates (≥5 endorsements) who did not qualify,
  excluding mainstream figures. B = outsiders who qualified narrowly
  (Arthaud, Poutou, Lassalle, Cheminade, Asselineau 2017) plus Taubira 2022.
  Endorsements "calling on" non-candidates (Pesquet, Hollande 2022…) and
  party figures (Juppé, Yade…) do not count.
- **Score**: A=2, B=1, +1 for endorsing a small candidate in BOTH 2017 and
  2022. P1 = at least one A. A B signatory of both years (score 3) ranks above
  a lone A (score 2): repetition predicts better than intensity.
- **The target is a person, not a commune.** If the mayor changed, the commune
  leaves the list (file 02 keeps the record).
- **Output is CSV `;` UTF-8-BOM** (Excel and LibreOffice friendly). File 01
  carries the three channels (email, phone + opening hours, postal address)
  and two mail-merge columns (`candidat_recent` as prose, `annee_recente`).
- **Full base `out/04_base_complete.csv` (34,826 mayors), three ranks**:
  `has_endorsed` (~1,960, the app's default filter), `commune_has_endorsed`
  (3,049 — the commune has a precedent, the mayor changed), `no_signal`
  (29,805). Necessary: ~1,960 targets at 10-15 % conversion do not produce
  500 signatures.
  **The rank chooses the template** (`modeles/*_decouverte.txt`): "you
  endorsed X" is said at rank `has_endorsed` and nowhere else, and the
  endorser placeholders exist only for an endorser. `task messages` mails
  file 01 ONLY — 34,800 bulk emails would be spam.
- **Accessibility is guarded by `e2e/07-accessibility.spec.ts`**: axe scans
  (WCAG A + AA) every screen of the three modes, light AND dark, plus the
  keyboard path. Axe does NOT see everything — live-region timing and
  non-text contrast (gauge fill, field borders) were checked by hand;
  `--piste` and `--champ-trait` exist for the latter two.
  The interface conventions: **a live region pre-exists, only its TEXT
  changes, and it holds no interactive control** — `Alerte`,
  `CompteurResultats` (one node, debounced) and the sr-only announcer
  spans follow it; a card that appears with a button in it never carries
  the role. **Pre-exists means from the SHELL, loading state included**:
  `setReady(true)` and the first message land in one React batch, so a
  region living only in the ready tree mounts together with its text —
  that regression happened once. `web/src/live-regions.test.tsx` pins it.
  Assumed exceptions: the transient « Chargement… » paragraphs mount with
  their text (a missed one costs nothing), and card borders (`--trait`)
  stay decorative.
  **A control never vanishes or goes `disabled` under the user's focus**:
  a self-unmounting button (« fermer », « j'ai noté », accepting an offer)
  hands focus to the content first (`focusContenu`), and a busy submit
  uses `aria-disabled` plus a re-entry guard in its handler — `disabled`
  on the focused button drops keyboard focus to `<body>` in every
  browser. Leaving the outage shell focuses the next view's h1
  (`useViewFocus` remembers across shells). `web/src/focus.test.tsx` pins
  all three.
  **A control that dies at the completion of an awaited RELOAD is a fourth
  case, and `rescueFocusAfterCommit` does not cover it**: a moderated card
  leaves its queue when the refetch lands, not when the decision answers.
  Called before the await, that helper's two checks (0 ms then 60 ms) watch
  a button still in the page; called after, it finds focus already on
  `<body>` and cannot tell "nobody was holding anything" from "the holder
  just died". `holdFocusThrough` captures the element before the round trip
  and WATCHES for its removal — a timer is a bet on when React commits, and
  it loses. Both moderation screens use it.
  Tab strips go through `NavOnglets` (`aria-current="page"`), view changes
  through `useViewFocus` (focus to the h1, per-view `document.title`),
  decorative pictograms through `Emoji` (aria-hidden). `--focus` is the
  ring colour (olive in light: yellow reads 1.6:1 there). Muting a row is
  done with `tr.inactif` plus a word, never opacity — opacity halves every
  contrast under it.
- Tag `parrainage_theme_democratique` (142 targets): endorsed a candidacy
  about how democracy works (Marchandise/LaPrimaire, Egger/RIC,
  Koenig/subsidiarity, Jardin, Nikonoff, Faudot, Troadec). The tag describes
  **a public act on record**, never a conviction — do not rename it to
  "leaning": an elected official's sincerity is not ours to presume.

## Data rules that decide the crossing

- **An endorser's identity is commune + surname + FIRST NAME, everywhere**:
  the endorsement aggregation key, the RNE match, and the INSEE dedup. Drop
  the first name and Mélina and Gilles Gardien merge, so a sitting mayor
  inherits someone else's endorsement; the surname alone over-matches
  successions. A duplicate INSEE in the output is an assert.
- **The RNE's sex code (column 8) is the decisive discriminator.** It settles
  Christian vs Christine (0.89 ratio, invisible to spelling) and in exchange
  allows a low tolerance on typos (Henry/Henri). It also gives the output
  civility — the Conseil constitutionnel's contradicts the RNE on two rows.
- **Compare WHOLE first names.** Reduced to their first token, Marie-Cécile
  and Marie-Ève are the same person. Truncation is accepted (Jean-Louis ⊇
  Louis), contradiction is not.
- **Overseas**: Martinique, Guyane, Polynésie, Nouvelle-Calédonie and SPM
  have an EMPTY department label in the RNE — their name is in the "special
  status collectivity" column. Without that fallback, 139 communes are
  unreachable.
- **Never assert what is not established.** A fuzzy commune match (0.87
  confuses Goncourt and Voncourt) followed by a different name does not prove
  a succession. The cutoff is 0.93, and doubt goes to file 03 "to check",
  never to file 02 "successor in office".
- **The INSEE code of the signed commune settles a rename from a merger**:
  identical means renamed (Vertus and Blancs-Coteaux are both 51612),
  different means two communes. A commune missing from the RNE is NOT a
  merged commune — 912 have a town hall and no RNE row. Delegated town halls
  are excluded from the index: they keep the historical code, and counting
  them would refuse the very mergers the fallback exists for.
- Civil status and output department come from the RNE. The endorsement files
  vary (compound names, "Corse du Sud" vs "Corse-du-Sud"), and the 2022 file
  has double spaces in some candidate names — everything goes through
  `collapse()`.
- RNE surnames are sometimes compound (birth name + used name) → match on
  "one of the tokens". Communes merged or renamed since 2017 → fuzzy fallback
  (difflib ≥0.87), then a search for the person across the whole department.
- Directory: `pivot like "mairie"` also catches annexes and delegated town
  halls → filter `type_service_local ∈ {mairie, mairie_com}` and dedup by
  INSEE (shortest name = the main record). Its `telephone`, `adresse` and
  `site` fields are JSON embedded in the CSV.
- **`difflib.SequenceMatcher` has no equivalent outside Python.** The 0.93
  commune threshold is tuned against ITS `ratio()`: another distance changes
  which mayors are targeted, silently. The port is `noyau/texte.ts`, frozen
  by `noyau/texte.test.ts`. Its "autojunk" heuristic (b ≥ 200 elements) is
  not reproduced — the port raises rather than diverge in silence.
- **`noyau/csv.ts` ends its lines with CRLF**, which is what Excel and
  LibreOffice expect from a `;` file.
- **The sources are read BY POSITION** (r[4] = commune, r[8] = sex code). A
  column inserted upstream would shift every identity and produce 34,826
  lines of nonsense with exit 0. Headers are checked at the index actually
  read, and a yield floor guards the total. The two endorsement files do not
  spell their last column the same way ("Candidat" in 2022, "Candidat-e
  parrainé-e" in 2017).

## Multi-campaign

One instance can host several campaigns, one per subdomain.

- `PARAPHE_BASE_DOMAIN` empty = **single campaign**: every host serves the
  bootstrap campaign. Set = each campaign on `<slug>.<domain>`, the apex
  serving the public home page and moderation. The mode is read from the
  `Host` header of each request, not chosen at build time.
- **The mayor list stays COMMON and read-only** — public data, identical for
  everyone. Only the work columns move out of `mayors` into
  `assignments(org_id, insee_code, team_id, volunteer, status, updated_at)`.
- **Isolation is ONE wall: every query touching a per-campaign table names
  the campaign**, bound as `$1` by the single constructor `scoped(r)`.
  PostgreSQL row-level security was a second wall and was removed on
  15/08/2026 — do not reintroduce it without reopening that decision.
  Two tests carry the whole guarantee:
  1. `TestEveryQueryOnAWalledTableNamesTheCampaign` reads the package as an
     AST and demands the `org_id` predicate **per table alias**. One crossing
     is exempt, `import.go:removeStale`, because the mayor list is common and
     deleting a row there must account for EVERY campaign. It also refuses
     `TRUNCATE`, `LOCK`, `DROP`, `ALTER`, `COPY` and `DO $$…$$` blocks: no
     predicate can bound them.
  2. `TestNoCampaignSeesAnother` runs two campaigns on one instance,
     exercises every route, and checks that no row, no count and no string of
     the neighbour comes back.
  **That canary is a regular-expression heuristic, not a SQL parser** (a
  measured decision — `pg_query_go` brings cgo into a project that has none,
  `postgresql-parser` brings 180 transitive modules against 4 direct ones).
  Seven adversarial rounds walked past it 46 times. It is the only wall:
  keeping it sharp is not optional.
  `walledTables` does not verify itself: `TestEveryPerCampaignTableIsWalled`
  asks the database which tables carry an `org_id` column and requires the
  list to match.
  In `assignmentJoin` the predicate sits in the JOIN CONDITION, never in the
  `WHERE`: in a `WHERE`, the outer join becomes inner and every mayor nobody
  has taken disappears.
- **Two sentinel scopes**, impossible in the database (identities start at 1):
  `0` = instance (the apex, which sees no work row), `-1` = maintenance
  (import and migrations, crossing campaigns). No HTTP request can reach
  `-1`: resolution only returns identifiers read from the database, or `0`.
- Role `administration` validates hosting requests, lives in the instance
  scope and reads no campaign data. It is obtained by bootstrap only —
  `validRole` knows the three campaign roles and nothing else, so a
  coordination cannot mint one.
- Campaign configuration lives **in the database, per organisation**, edited
  by coordination ("Mon équipe"). The campaign `PARAPHE_*` only bootstrap the
  first one.
- A public request form plus moderation: a request creates NOTHING until an
  instance administrator approves it. Without that, the first abuse is
  squatting a candidate's name, with no recourse for the campaign squatted.

## Accounts and teams

Access is by individual account (email + hashed password), three roles:
coordination (sees everything, creates teams and leads), lead (opens
volunteer access for THEIR team), volunteer. A team is a name plus
departments; it draws only within its perimeter, sees only its own
reservations, and a card reserved elsewhere is refused (403). Campaign
counters stay visible to all, without names. `PARAPHE_ADMIN_EMAIL` /
`_PASSWORD` bootstrap coordination: with no coordination account the app
refuses to open rather than let anyone in.

**A team can also be ASKED FOR**, from a public form on the campaign's own
sign-in screen — the instance's hosting request, one level down, and for the
same reason: whoever wants to gather a team around them has no account yet.
Like that one, it creates NOTHING. `team_requests` is a walled table, and it
is the campaign's coordination that decides.

- **The coordination edits the name and the perimeter as it accepts.** The
  person filling the form knows their department, not the campaign's map; a
  coordination that could only accept or refuse would refuse a good request
  because the name is wrong.
- **Accepting opens the team and its lead account in one transaction.** If
  the address already has an account here it is refused (409) rather than
  promoted behind its owner's back — and the team is not opened either, since
  a team whose lead could not be created is a team nobody leads.
- **The requested departments are checked against the mayor list.** A
  perimeter of labels no mayor bears is a team that draws zero cards for
  ever, and nothing downstream ever says why. `/api/config` carries the
  department list for that form alone — public data, common to every
  campaign.
- **The ceiling on pending requests is applied BY THE INSERT**, never by a
  count read before it. Read separately, two clients both see 199 and both
  write; the queue then showed its newest 200 and dropped the OLDEST — the
  legitimate early requests — off the only screen that can accept them. The
  pending read therefore carries no `LIMIT` of its own either: the insert
  bounds what exists, the read shows all of it.
- **One refusal for a name already spoken for**, whether a team bears it or
  a request is pending on it. Two sentences answered a question nobody
  asked: an anonymous visitor learned which of the two a name hit, and a
  campaign's team names appear in no public route. The hosting form may
  distinguish them — a slug is public by construction, every subdomain
  answers.
- **A public form's names go through `legible`**: no control character, no
  format character. A right-to-left override reverses what a moderator
  reads without touching a byte of what is stored, so the row they believe
  they are accepting is not the one they accept. Free text is exempt — a
  message is allowed its line breaks.

## Security and operations

- `PARAPHE_INSTANCE_ADMIN_*` bootstrap the instance administration.
  The session cookie is always `Secure`, `HttpOnly` and `SameSite=Lax`, none
  of them configurable: a setting is one an operator can get wrong once, in
  the direction where nothing appears to break. `PARAPHE_SECRET_KEY`, when
  supplied, is **refused below 32 bytes** — it signs every session, and one
  captured cookie turns a short key into an offline search; unset, a random
  secret is drawn at first start and kept in the database. **The chart, on
  the other hand, requires it**: rendered client-side (`helm template`,
  ArgoCD by default) it cannot read the Secret already in place, so drawing
  one would mint a new one at every sync and sign everyone out. The session
  is a **JWT
  (HS512) in that cookie**: symmetric, so post-quantum by construction, and
  the margin is bounded by the KEY — 64 bytes, hence ~256 bits after Grover.
  The verifier never READS `alg`, it compares the header byte for byte, which
  is what rules out `alg:none`, HS/RS confusion and `kid`/`jku` injection. The repository's example
  values are refused at startup — they are public.
- **Rate limits, declared once in `api/limiter.go`**: sign-in per source
  AND per submitted address (counted whether the account exists or not — a
  429 must reveal nothing the decoy hash withholds), public hosting form,
  anonymous reads, authenticated writes, export. Ceilings are constants,
  not settings. Counters live in **Valkey** (`PARAPHE_VALKEY_URL`, direct
  or `valkey+sentinel://…/paraphe`) when set — the chart ships a 3-node
  Sentinel group, diskless by design — and in process memory otherwise,
  which is exact for one replica; a Valkey outage degrades to per-instance
  counting, said once, recovered alone. Two canaries walk the route tree:
  every route names its ceiling (`limiter_routes_test.go`) and every route
  parameter has an executed foreign-identifier refusal (`idor_test.go`).
- **Privacy first, no address in the clear**: limiter keys are HMAC of the
  subject (IPv4 addr / IPv6 **/64** aggregate, or org+email), security
  events log day-scoped pseudonyms only, and `X-Forwarded-For` is believed
  solely behind `PARAPHE_TRUSTED_PROXIES`. No fail2ban feed on purpose —
  the ban is the in-app ceiling.
- **Real values never go in a versioned file**: `.env` (compose) and
  `config/campagne.local.yaml` are ignored; `docker-compose.yml` and
  `config/campagne.yaml` are public templates.
- One PostgreSQL role, the one CloudNativePG generates, owner of the tables:
  the application runs its own DDL at startup. Its privileges have no bearing
  on isolation — the wall is in the queries.
- Startup import is an UPSERT: data (email, score, tags) is refreshed, work
  columns (volunteer, status, updated_at) are never touched. A target removed
  from the CSV is deleted if untouched, flagged "RETIRÉ" if already worked on.
  Schema migrated by ALTER TABLE.
- **The team's work lives in PostgreSQL and is the only IRREPLACEABLE data**
  — `task backup`. It is no longer the only data: a campaign logo lives in
  the object store (`task backup-media`), and losing one costs the thirty
  seconds it takes to upload it again. PostgreSQL keeps the POINTER (the key and
  its type, the key ending in a digest of the content), so a bucket
  restored from an older copy is detectable rather than silent.

## The campaign logo, and the origin that serves it

Optional, uploaded from "Mon équipe", shown beside the paraphe mark and on
the sign-in page. PNG, JPEG, WebP or SVG, 64 KiB at most.

- **The hexagon and the word stay.** `style.css` opens on the reason — this
  identity is deliberately not the State's and the site must never look
  official — and on a shared instance a campaign taking over the whole mark
  is the squat moderation exists to prevent. The logo JOINS the mark.
- **The browser fetches it from the object store's own origin**, not
  through the application. That is the one remote origin
  `securityHeaders` allows, assembled at startup from
  `PARAPHE_MEDIA_PUBLIC_URL` — so the policy is no longer a constant, and
  `contentSecurityPolicy` reads the setting into `img-src` and nowhere else.
- **A separate host is what contains an SVG.** The session cookie is
  host-only (no `Domain=`), so a script that ran on the media origin finds
  neither cookie nor DOM. Serving from the app would have needed a
  per-response `sandbox` policy; the bucket cannot set one, and the origin
  replaces it. The upload validator is the third line, behind that and
  behind `<img>`'s secure static mode: the BYTES decide the format, and an
  SVG with a script, an `on*` attribute, a DOCTYPE or an external reference
  is refused.
- **PostgreSQL holds the pointer, the store holds the bytes**
  (`orgs.logo_key`, `orgs.logo_type` — no digest COLUMN: the key ends in
  one, and the same fact written twice is a fact that can diverge). The key
  carries a digest of the content,
  so the URL is immutable and cached for ever; replacing a logo writes a new
  key and deletes the old one, best-effort and never blocking the answer.
  The object goes in BEFORE the pointer moves: the other order publishes a
  URL that 404s.
- **Five settings, all or nothing.** Half of them refuses the start, and so
  does a store that is configured but unreachable — the same posture as an
  unreadable `PARAPHE_WEB_DIR`. None of them is the normal state of a
  developer's instance and of most tests: the routes then answer 501 saying
  so, and the header shows the hexagon.
- **The S3 client is hand-written** (`api/media.go`), SigV4 over two calls.
  The usual client brings fourteen transitive modules into a project with
  six direct ones, almost all for multipart paths a 64 KiB object never
  takes. `TestALogoWrittenToTheStoreIsPubliclyReadableThenGone` runs it
  against a real Garage: a wrong signature is a 403, loudly.
- **Browser mode never holds a URL**, only a data URI in IndexedDB (a new
  key in the existing `settings` store — no VERSION bump). A logo adopted
  through `?org=` is downloaded ONCE, at the moment the volunteer accepts.
  Holding the URL would put a call to the instance in every page load, and
  "aucune donnée ne quitte ce navigateur" has to survive being checked in
  the network tab.
- **A Garage layout is imperative**: there is no declarative form of "this
  node stores data". `chart/paraphe/files/garage-init.sh` — one file, run by
  the compose stack and by the chart's Job — introduces the nodes, lays them
  out, creates the bucket, imports the key and publishes it. Three things it
  learned the hard way: `/health` answers 503 until the layout exists (so it
  is the READINESS probe and never the liveness one), the Job must not wear
  the StatefulSet's labels (their anti-affinity then excludes it from every
  machine), and `capacityBytes` must be QUOTED in values.yaml (unquoted,
  YAML reads a float and Garage is handed `1e+10`).

## Non-obvious constraints

- **`FOR UPDATE SKIP LOCKED` does not protect a row that does not exist.**
  Work lives in `assignments`, so a free card has NO row to lock: the
  assignment IS the insert, with `ON CONFLICT (org_id, insee_code) DO UPDATE
  … WHERE volunteer IS NULL`. PostgreSQL lets exactly one through.
- **An empty assignment round does not mean the pool is empty.** Every
  volunteer aims at the best-scored cards, so the loser of a race sees its
  whole snapshot taken. Hence the bounded loop (8 rounds) and an explicit
  question — are there cards left? — before saying the pool is exhausted.
- **A Kubernetes probe addresses the pod's IP**, so with a `Host` matching no
  subdomain. `/api/config` answers it 404 in multi-campaign mode and no pod
  ever becomes ready — hence `/health/db`, which queries the database without
  resolving a campaign.
- **An unquoted `:` in a `run:` breaks the WHOLE YAML file**, and GitHub
  rejects the entire workflow. `outils/deploiement.test.ts` sweeps the
  workflows and the Taskfile for it.
- **IndexedDB only reshapes a database when the VERSION goes up.** Stores
  renamed at a constant version leave every already-open database on the old
  schema, and every read raises — the emergency export included.
- **Third-party GitHub Actions are referenced by commit, never by tag.**
  A tag is movable by whoever owns the repository, onto code that reads the
  workflow's secrets. `outils/deploiement.test.ts` refuses a floating one.

## How to work on the guards

The isolation guards have been through seven adversarial rounds. What those
rounds found, stated as rules rather than as a story:

- **A "could not read this" token is not proof.** When the canary fails to
  resolve an expression it marks it; accepting that marker as a bounding
  predicate is how tautologies get declared compliant. Either the code
  becomes readable, or the site is declared.
- **Never assert a write by what did NOT change.** Assert the return code
  first: a handler answering 400, 409 or 415 writes nothing, and "the
  neighbour is untouched" then holds for a reason unrelated to any wall. And
  ask whether the marker would be REACHABLE without the wall.
- **The canary and PostgreSQL must read the same text the same way.**
  `NOT (…)`, nested `/* /* */ */` comments, `E'\'escaped'` strings and
  `DO $$…$$` bodies are where they disagree.
- **A guard that verifies itself guards nothing** — one list deciding both
  what is protected and what is checked loses both in a single edit.
- **A guard that panics examines nothing after it**, and does not even fail
  on what it already found.
- **Mutate the code and require the test to go red ON ITS OWN ASSERTION.**
  Red elsewhere, or red on a bare `t.Fatal(err)`, proves nothing. Check with
  `grep -c` that the mutation applied: an edit that did not take leaves the
  test green and the conclusion wrong.
