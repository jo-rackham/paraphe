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
  **A re-entry guard is a REF, never state.** Two clicks in the same tick run
  two handlers built by the same render, and both read the state as it was
  before either of them: the button greys out and the request goes twice.
  This project has paid for it twice now — once on a submit, once on
  « Recevoir un lien », where it also spent two of the three links an address
  is allowed per quarter of an hour AND killed the first one by minting the
  second. `aria-disabled` is what the screen shows; `useRef` is what guards.
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
  **A table-position keyword belongs to THREE patterns, not one**: `sqlVerb`
  (this text is a statement), `tableRef` (this is where its table is named)
  and `unreadableTable` (its table is in that position and cannot be read).
  PostgreSQL's `TABLE t` shorthand was taught to the first two and not the
  third, and `TABLE `+t became a statement whose walled table nothing could
  resolve — which the canary reads as a statement touching no walled table,
  for all five of them at once. Whoever adds the next shorthand adds it to
  all three. The same asymmetry one position further along cost a second
  critical: `tableRef` has always read a COMMA as a table position, and
  `unreadableTable` only ever looked at the first, so `FROM accounts a, `+t
  named a walled table where the canary reads literals and never checks for
  markers. It walks the list reference by reference — a wildcard between
  `FROM` and the comma would swallow `ORDER BY x, %s`, which is nobody's
  table, and a false positive here sends the next author around the wall.
  A THIRD round produced the same shape again, so the rule now covers every
  place a table stands in: after a keyword, after a verb no predicate can
  bound (`"TRUNCATE "+t` and `"GRANT SELECT ON "+t` read as statements
  touching nothing at all), and anywhere in a FROM list — that last one
  written in Go, because the list ENDS at a clause keyword and RE2 cannot say
  "up to the first of these words". `destructiveVerbs` is declared ONCE and
  both rules are built from it, with
  `TestEveryDestructiveVerbHasAnUnreadableForm` walking the list and
  demanding both forms of each. **A marker is something format strings are
  full of, where a table NAME is not**, so the destructive rule runs ONLY on
  a string that reaches a call which executes it. Telling `"TRUNCATE "+t`
  from `lock %d in progress` by the words around them cannot be done — the
  first attempt listed what may follow the object, and that list is the
  common English prepositions: five plausible messages were refused as
  statements. What separates them is not their text, it is whether anything
  runs them. `readsNoWalledTable` is still EMPTY, and keeping it so is the
  measure of whether these rules judge statements or prose.
  **A statement starts after a SEMICOLON as well as at the beginning**, and
  the rule refusing a procedural body knew only the second: `SELECT 1; DO $$
  BEGIN TRUNCATE notes; END $$` walked past it, past `sqlVerb` — which
  searches anywhere and found the SELECT — and past every rule after, because
  `stripDollarQuoted` had already emptied the body and left no TRUNCATE to
  see. pgx runs it: measured, two campaigns' rows to none. Anchored `(?:^|;)`
  like the destructive rule, and `TestAProceduralBodyIsRefusedWhateverLeadsIt`
  walks every verb `sqlVerb` knows in front of one.
  **A FOURTH round found the fourth square of the same grid** — the `ONLY`
  modifier, which `tableRef` read and `unreadableTable` did not. So the grid
  itself is now the guard: `tablePositions` and `tableModifier` are declared
  once, both rules are built from them, and
  `TestEveryTablePositionIsReadByBothRules` walks keyword × modifier
  demanding that one read a NAME there and the other a MARKER. Teaching one
  rule a position without the other goes red in the round that does it.
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

## Signing in by email

A second path beside the password, ON only when `PARAPHE_SMTP_URL` is set —
`s.mailer == nil` IS the "off" state, `/api/config` says so, and the two
routes answer 503 rather than accept a request whose effect never arrives.
It ADDS a path: the password is still read down a telephone line, still
works when the relay is down, and still bootstraps the instance.

- **The token travels in the FRAGMENT** (`/connexion#jeton=…`), never in a
  query or a path. A fragment reaches no server: no ingress access log, no
  Referer, no proxy history — and it is invisible to the URL scanners
  corporate mail systems run, which FETCH every link they see and would
  otherwise spend a one-shot token before its recipient clicks. It is
  redeemed by a POST with the token in the BODY, which also keeps it out of
  every route parameter on the way back.
- **The link's origin is `PARAPHE_PUBLIC_URL`, never `r.Host`.** In
  single-campaign mode every Host resolves the bootstrap campaign, so a link
  built from the header would send — to a real volunteer, over the campaign's
  own name — a link to a server of the caller's choosing. Multi-campaign, the
  slug is prefixed to the configured apex and the setting must name the base
  domain; the three mail settings hold together or the start fails — **and
  the chart refuses them half-filled at RENDER time**, where the person who
  made the mistake is looking, rather than as a CrashLoopBackOff behind an
  ArgoCD stuck on « Progressing ». CI renders the mail block too: it is off
  by default, so no other case exercises those twenty-eight lines, and
  `helm lint` parses a branch without executing it.
- **Stored as a plain SHA-256, deliberately.** The token is 256 bits of
  `crypto/rand`: there is nothing to search, so a memory-hard hash buys
  nothing and would put a 32 MiB derivation behind `hashGate` on a PUBLIC
  route — the amplifier `hashGate` exists to bound.
- **Requesting a link: constant answer, and NOTHING that differs happens
  before it.** The same status and the same body for an address that names an
  account, a deactivated one, or nobody — the promise the decoy hash makes on
  the password path. Detaching the SEND was not enough: minting is a DELETE,
  an INSERT and a COMMIT, and while they sat before the reply an existing
  address answered 3.5x to 6.5x slower — a stopwatch handing back the roster
  the sentence withholds. Both branches now reply on the same SELECT, and the
  mint happens on the other side of it, on its own connection
  (`mintDetached`). `TestAnExistingAddressIsNoSlowerThanAnUnknownOne` measures
  the ratio and refuses above 1.5. A failure in that detached work is logged
  and NOT answered: the one assumed exception to "errors surface".
- **STARTTLS is required, not attempted.** Opportunistic TLS is TLS an
  attacker strips from the greeting, and the message carries a credential
  with a fifteen-minute life. A relay that does not offer it is refused;
  `smtps://` and the loopback are the two ways past that refusal, and the
  loopback is the same exception `net/smtp` makes for credentials.
  **The URL's user and `PARAPHE_SMTP_PASSWORD` hold together both ways**: a
  password with no user is refused, and so is a user with no password, which
  used to authenticate with an empty one — the relay then answered 535 to
  every message, and that refusal reached an operator as a line in a detached
  goroutine's log while volunteers waited on an inbox.
- **Assumed**: whoever knows an address can burn its pending link (minting
  deletes the previous row), bounded by three per quarter of an hour. The
  password path is untouched by it, and the alternative — several live tokens
  per address — trades a bounded nuisance for an unbounded table.
- **Invitations: synchronous, and the outcome is told.** The opposite rule
  for the opposite reason — the caller is authenticated and has just created
  the account, so there is no existence to protect. `invitation_sent` travels
  in the answer, the generated password stays on screen whatever happened,
  and the token is minted in the SAME transaction as the account.
- One live row per (campaign, address): minting deletes the previous one and
  sweeps expired rows in passing, so a new link invalidates the old one and
  the table cannot grow under a loop. Redeeming is `DELETE … RETURNING` —
  single-use by construction, with no consumed row whose existence would
  then have to be told apart from a token that never was.
- **PRESENTING a token spends it, atomically**, and on the request's OWN
  connection (`Scope.renew`: commit, then reopen a transaction behind it).
  Three shapes were tried and two were wrong. Committing at the END of the
  route let every failure in between roll the DELETE back — refused against
  a DEACTIVATED account, the link came back live and opened a session the day
  the account was switched on again, seven days later for an invitation.
  Taking a SECOND pool connection made the spend independent and DEADLOCKED
  the pool: the request already holds one, so eight simultaneous redemptions
  against a pool of four hung until they timed out. Renewing costs neither.
  **And that commit does NOT run under the request's context.** Cancelling it
  is the one failure the CALLER controls: hang up between the DELETE and the
  commit and PostgreSQL rolls the DELETE back, so the link just presented is
  live again — a replay obtained on demand, then kept for the day the account
  it was refused against is switched on. `context.WithoutCancel` plus a bound
  of its own; `s.commit` keeps the request's context, because an ordinary
  write that the caller abandons has promised nothing. **And a commit that
  fails anyway is answered by spending the token AGAIN**, on a connection of
  its own (`spendAlone`), the request's being handed back first so the count
  is never two. A cluster rolling, a node evicted, a stall past the bound —
  each of them aborts the transaction carrying the DELETE and hands the
  presented link back, live for its whole remaining life. Best effort, and
  the limit is stated rather than implied: **both attempts are bounded at
  five seconds, so a database that stalls longer than that — not only one
  that is gone — leaves the link live and says so in the log**. A longer
  bound would pin a pooled connection on a request that has already failed;
  no bound would pin it for ever. The failed spend is logged, never
  answered.
- **One live link per address AND PURPOSE is the DATABASE's promise**, not
  the DELETE's: a unique index on `(org_id, email, purpose)` plus
  `ON CONFLICT … DO UPDATE`. Under READ COMMITTED the DELETE cannot see a row
  a neighbour has not committed, so two requests in the same instant both
  inserted — two links in one inbox, the older already dead. The purpose is
  in the key because the two kinds do not compete: a sign-in request used to
  destroy a pending invitation the invitee had not asked about and would find
  dead days later.
- **Redacting an address out of a relay's answer matches the ORIGINAL text**
  (`events.go`, a case-insensitive regexp), never a lowercased copy of it.
  Lowercasing changes byte LENGTH — `Ⱦ` is two bytes, `ⱦ` is three — so an
  offset found in the copy addresses somewhere else in the original: it left
  half an address in the log, and past the end of the string it PANICKED, in
  a detached goroutine, which takes the process with it.
  **The LOCAL PART is redacted on its own too**, as a whole word: plenty of
  relays name the recipient without its domain, and the domain is the half
  that identifies nobody. It over-redacts when the local part is an ordinary
  word (`contact`, `info`) — the cheaper mistake — and stays a whole word
  because `connect@` would otherwise turn `connection reset` into nonsense.
  **That boundary is decided on RUNES, not by `\b`**: Go's is ASCII-only
  (`\w` is `[0-9A-Za-z_]`), so `\bhervé\b` has no boundary to find after the
  `é` and never matches. In a French campaign that is not an edge case, it
  is most of the volunteers — the accented name went into the log verbatim
  while the ASCII one beside it was scrubbed. **Assumed limit**: a relay
  answering with an ALIAS-EXPANDED recipient names an address this code was
  never given, and no pseudonymisation keyed on the address sent can catch
  it.
- **The interface takes the token out of the URL at BOOT** (`main.tsx`),
  hands it over EXACTLY ONCE (`consumeLinkToken`), and DROPS it when the
  visit lands on a screen that cannot use it. Kept until a screen that could
  use it finally mounted, it opened the first visitor's session for the
  second: an outage, a tab left on the table, `Réessayer` pressed by someone
  else. The link is still in its owner's inbox — clicking it again costs one
  click.
- `login_tokens` is a walled table: it carries `org_id` and is in
  `walledTables`. A token minted on one campaign is not a credential on its
  neighbour, and both guards were seen to go red on that mutation.
- Nothing a human typed reaches a HEADER: the subjects are constants of the
  package, addresses are refused if they carry a control character
  (`normalizeEmail` only lowercases and trims), and the body — where the
  campaign's name lives — is base64, an alphabet that cannot spell a header
  break.

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
- **A successful sign-in REFUNDS its attempt; it does not clear the
  counter.** Three shapes, and the first two are each wrong in one
  direction. CLEARING is observable: the per-address ceiling is one an
  anonymous caller fills for an address of their choosing, so burning it and
  polling it turns its reopening into "somebody just signed in as that
  address" — measured at ten attempts left against one, which is the decoy
  hash undone from the other side. NOT clearing locks an account out of its
  own password after ten legitimate sign-ins, because the ceiling counts
  successes too; the end-to-end journeys found that in under a minute.
  Refunding does both: counted on arrival like every event — which is what
  still bounds a flood whose handlers never finish — and given back once the
  attempt proved legitimate, so the bucket ends where it stood.
  **What is refunded is what THIS ROUTE counted, not the door the caller
  came through.** Redeeming a link counts nothing per account — the token
  carries no address, so the source ceiling is its whole bound — and
  refunding the request ceiling there gave back an event nobody had spent:
  burn that ceiling for an address you know, and the slot that reopens says
  its owner has just clicked their link. `openSession` takes the class as a
  POINTER for that reason, and the redeem passes nil.
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
- **A wedged store must not take the pod out of its Service.** Both logo
  routes hold their pool connection across a round trip to the object store
  — `inCampaign` takes it before the handler runs — so an unbounded burst
  against a store that answers nothing takes every connection the instance
  has, and `/health/db`, which needs one, fails with them. Measured on a
  paused Garage at the pgx default of four connections: six uploads, six
  readiness probes out of six lost, which is a pod dropped from its Service
  because a picture would not upload. `mediaAdmission` bounds it to TWO in
  flight and REFUSES past that (503) rather than queueing — the opposite of
  `signInAdmission`, and for its very reason: that gate sits BEFORE
  `inScope`, where waiting holds nothing, and this one sits after. Two and
  not one, because the race
  `TestARemovedLogoCannotDestroyTheOneThatReplacedIt` runs needs two writers
  to meet; that test now fails if either is refused, so narrowing the gate
  cannot quietly empty it. Same measurement after: six probes out of six at
  200. The deferred deletion is bounded on its own (one per instance): it
  answers nobody and holds both a connection and the row lock.
  **The bill is THREE connections, not two**: each admitted upload leaves a
  deferred deletion behind, and that deletion takes a connection of its own.
  So the margin at the pgx default of four is ONE, and a smaller pool has
  none — measured at three, two readiness probes out of eight lost. The
  start warns below four rather than refusing: a small pool is a legitimate
  choice, and pgx only goes under four when a DSN says so.
- **The XML DECLARATION is not a processing instruction to refuse.**
  `<?xml version="1.0" encoding="UTF-8"?>` is the first line Inkscape,
  Illustrator and Sketch write, so refusing every `xml.ProcInst` refused
  nearly every file a campaign would actually upload — and told them to
  re-export without a line no export dialog mentions. `xml-stylesheet` is
  refused; a target of `xml` is not. In a tool that BLOCKS, a false positive
  costs as much as a hole and is harder to see.
- **`data:image/` is a MIME token, not a prefix.** The C0 strip that makes
  `java<TAB>script:` readable also turns `data:image /html,…` into a value
  that starts with `data:image/`, and `data:image/x/html,…` starts with it
  outright. The allowance names the raster types and requires the token to
  END (`[;,]`); `svg+xml` is absent, an SVG inside an SVG being a document
  the validator never opened.
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
