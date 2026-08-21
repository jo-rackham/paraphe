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

## The way out, offered by the way in

The same image carries a SECOND build of the interface, served under
`/navigateur/` on every host: the account-less version, whose index carries
no mode marker and whose `/api` paths answer HTML — the two conditions that
make it decide "no API here", on the very origin that serves one. Each
campaign's sign-in screen links to it, and so does the footer of every screen
of that campaign. A volunteer who wants no account, or a shared computer, or
a train, has a door, and it is on the page they already landed on.

- **A CAMPAIGN'S OWN BROWSER VERSION FILLS ITSELF IN**, with no click and no
  confirmation. It asks the origin that served it — `ownCampaign()`, a
  root-relative `GET /api/campaign/public` — and takes the answer as its
  DEFAULT. The confirmation screen was written for a LINK: `?org=` may name a
  campaign of another instance, so its values are shown before they are
  applied, and that is what stops a forged one putting somebody else's
  contact details under a real candidate's name. None of it applies to the
  campaign that SERVED THE PAGE — whoever could lie about it wrote the page
  saying so. Confirming cost two clicks, and to the volunteer who did not
  make them it read as a tool that had failed to fill anything in; it was
  reported twice as « les champs ne sont pas substitués ».
  **ROOT-RELATIVE is the whole trick.** Every other call this build makes
  goes through Vite's base, `/navigateur/api/…`, which the server answers
  with HTML deliberately — that is what keeps it in browser mode on an origin
  that has an API. The real API is at the ROOT of that same origin, and
  asking it is also what tells a campaign's own version from a static
  publication: on GitHub Pages that path answers HTML too, and `readOffer`
  refuses it.
  It never overwrites what a volunteer typed (`untouchedCampaign`), and a
  `?org=` naming a DIFFERENT campaign keeps its offer — a link is a link
  wherever it is opened.
  **WHO SIGNS DOES NOT TRAVEL** (`PERSONAL_CAMPAIGN_KEYS`). Seven of the nine
  describe the candidate and how to reach the campaign, and they exist to be
  handed over; `signataire` and `signataire_qualite` name a PERSON, and the
  person who filled the campaign form is the coordination. Adopted with the
  rest — which is what BOTH doors did — every message a volunteer produced in
  the account-less version went out over the coordinator's name and role, to
  mayors, with nothing on screen saying so. Team mode hid it completely:
  there each account supplies its own, so the fallback to the campaign's
  never fires. They are left at the template value on purpose, and the banner
  then asks for them IN ITS OWN WORDS — the unconfigured-campaign sentence
  would send a volunteer looking for a candidate already on their screen and
  never mention the signature at the bottom. `campaignUnfilled` is that
  distinction, and it is what decides which sentence shows.
  **« Aucune requête avant un clic » was never true**, and the test that said
  so now states the rule it was reaching for: zero requests to the host a
  LINK names, and the origin that already served the HTML, the bundle and
  139 kB of mayors may be asked. Held literally, that promise forbade the one
  request which names nothing.
- **The link still CARRIES the campaign** (`?org=<slug>`, built by
  `browserVersionFor`) — for the version published ELSEWHERE, which has no
  API origin to ask, and for a link sent from one campaign to another. The
  parameter is added only where it resolves — a single-campaign instance has
  no subdomain space, and it gets the plain link rather than a pre-fill that
  would land on an empty configuration.
- **ONE shape reader for the two doors** (`readOffer`): what an answer must
  look like before nine of its values go out in every message to a mayor is
  decided once. It THROWS, and the doors differ only in what they do with the
  refusal — the link door owes its reader a sentence, the origin door treats
  it as "there is no campaign here", which is its normal state on an apex, a
  static host or a captive portal.
  **What counts as CONFIGURED is not its question to answer its own way.**
  Written as "every key non-empty", it disagreed with the API that had just
  answered 200: three of the nine keys may be EMPTY
  (`campaign-optional.json` — a campaign may give a telephone number to
  nobody, run without a website, and not name the town its letters leave
  from), and a campaign that had exercised that right was refused by the
  build as "not a complete campaign". `unfilledKeys` is the referee both
  languages read, and it is the one this reads now; the emptiness test that
  remains is that every key is a STRING, which is this end's own question.
  **And the disagreement was SILENT**, which is what made it cost a
  release: `ownCampaign` swallowed every failure alike, so the screen showed
  « Prénom NOM » under a « Campagne non configurée » banner — indis-
  tinguishable from an engine that had failed to substitute, and reported as
  exactly that. Absence stays silent (a 404, a 409, HTML from a static host);
  an ANSWER this build could not take is said out loud, in the slot the
  broken-link message already owns.
- **The provenance sentence has a slot of its own**, like `offerError`. Sent
  through the general message, the list download wiped it a second later
  (« 34 826 maires chargés ») and the campaign's texts appeared with nothing
  saying where they came from — the same clobbering, one region over.
- Rejected: injecting the campaign into the page at startup, server-side.
  `s.browserPage` is ONE page for every host; rendering it per host would put
  a scope resolution, hence a pool connection, on a static path — and a
  snapshot taken at startup is stale the moment a coordination edits its
  texts.
- **The instance the parameter resolves against is injected AT STARTUP**
  (`markBrowserVersion`), in memory, like the mode marker beside it. It is
  not baked: one image serves every operator's instance, and one carrying
  `paraphe.org` would send everybody else's volunteers to fetch campaigns
  from ours. The static publication, which has no server to inject anything,
  still bakes it. **Checked first, normalised second**: `normaliseHost` is
  written for a Host header where a port is legal, so run first it turns
  `https://paraphe.test` into the perfectly valid one-label domain `https`,
  which is the confusion `validBaseDomain` exists to refuse.
- **A link may name a campaign; it may never name a host.** Unchanged, and
  the marker does not touch it: the domain comes from the DOCUMENT, the
  parameter still carries a DNS label. What the marker DOES decide is the
  scheme and the port — an instance that named itself served this page, so
  it is reachable as this page was reached. Hardcoded `https` and no port,
  the offer failed with « Failed to fetch » on every instance not listening
  on 443, and only the end-to-end suite saw it: production hid it.
- **The media origin is in `connect-src`, beside `img-src`.** That build
  downloads the logo and inlines it as a data URI, because it promises
  nothing leaves the browser. Left out of the policy, the campaign was
  adopted without its mark and the failure showed in a console alone.
- **`//` IS A HOST, AND NEITHER GUARD COULD SEE IT.** Three adversarial
  agents found the same one independently. `PARAPHE_BROWSER_VERSION_URL`
  is checked at startup — an http(s) URL, or a path on this instance — and
  `//ailleurs.test/x` begins with a slash, so a "is it absolute" test read
  it as a path. It is not: the browser resolves it against the page's
  scheme. The interface's own `httpUrl` could not catch it either, because
  it tests the RESOLVED protocol and `new URL("//ailleurs.test", origin)`
  resolves to `https://ailleurs.test` — the check passes and the raw string
  goes into the href, where the browser reads the same thing. One operator
  typo, and every campaign's sign-in screen and every footer carried a link
  off the campaign under the campaign's own name. Refused in BOTH places
  now: at startup where the operator is looking, and in `httpUrl`, which is
  where every operator-set URL becomes an href (`source_url` had the same
  hole). Naming another host OUT LOUD stays allowed — a browser version
  published elsewhere is a supported deployment.
  **The slug is checked where the host is BUILT**, not only in
  `requestedSlug` where it happened to be checked: `fetchCampaign`
  interpolates that label into a hostname, and one new call site would have
  withdrawn the promise silently.
- **THE WAY BACK, and it is a THIRD marker** (`paraphe-served-by`), stamped
  whatever the mode. Every screen of the account version has carried a door
  out to the browser version since it was built; the browser version had no
  door back, so a volunteer who took the first one had left for good, with
  the account version's address nowhere on their screen. The marker says the
  one thing no domain does — an INSTANCE is serving this build — so the
  account version is at the ROOT of this very origin, and a static
  publication, carrying no marker, offers nothing.
  **Not derived from the instance marker beside it**: a single-campaign
  instance names no domain, so a door read out of the domain would have
  opened only on multi-campaign instances — and the one every developer runs
  is the other kind. `outils/deploiement.test.ts` reads the stamped set out
  of `pages.go` and demands a reader for each, so a fourth marker cannot be
  stamped without one.
- The e2e suite builds it with `PARAPHE_BASE_DOMAIN` DELIBERATELY EMPTY, as
  the image is: a green journey there is a statement about the injection and
  nothing else.

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
- **WHOEVER SENDS IT IS WHOEVER SIGNS IT.** Every template that leaves is
  written by the VOLUNTEER, on the candidate's behalf, and signed
  `{signataire}, {signataire_qualite}`. The letter was not: it spoke at the
  candidate's own « je » — « Je m'appelle X », « mes idées », « mon équipe »,
  « ma considération » — and signed with the candidate's name, while the
  person who prints it, stamps it and posts it is a volunteer. Five hundred
  letters putting words in a candidate's mouth and a signature they never
  gave, generated by people who cannot answer for them. The email and the
  phone script always had it right, which is how it was noticed.
  **The candidate is QUOTED, not impersonated.**
  `candidat_description_longue` is first-person BY DESIGN and exists for the
  letter alone, so it stays exactly as campaigns already wrote it — announced
  by « Sa démarche, dans ses mots : ». Rewriting it into the third person
  would have been a silent migration of everyone's saved text.
  `noyau/messages.test.ts` pins it structurally, at both ranks: the signature
  block names the signatory and not the candidate. Prose is not matched —
  the candidate's name belongs in the BODY.
  A letter the candidate really does sign is a fine thing and is written by
  hand, outside the tool; `modeles/courrier.md` says so.
- **The address bar names the screen** (`web/src/route.tsx`), and it is
  written BY HAND on the History API. `web/` has three dependencies; a
  routing library brings twenty transitive packages for nested routes,
  loaders and data APIs that five views and one card do not use — the same
  reasoning that hand-wrote the S3 client and ported `difflib`. The store is
  read through `useSyncExternalStore`, because `popstate` fires outside React
  and a useState/useEffect pair tears under concurrent rendering; its
  snapshot is the pathname STRING, since a freshly built array is a fresh
  identity every call and React loops on that.
  **THE PATH, NEVER THE FRAGMENT.** `#` belongs to the sign-in link.
  Navigating drops whatever is in it, which is the second lock on the same
  door: a token cannot reach a new history entry, a bookmark, or a URL
  pasted into a support thread. Nothing was owed to the server — it has
  always answered `index.html` for every extension-less path.
  **One address per screen**: each mode's home is the BASE, not a segment
  beside it, because two URLs rendering one view make a « précédent » that
  appears to do nothing. An unknown view falls back to that home rather than
  to a blank tree, and the known list is per MODE — the same path is a real
  view in one and nonsense in another, which is what three modes on one
  bundle means.
  A card lives UNDER its list (`/maires/<insee>`), so « précédent » from a
  card lands on the list. It is loaded by ONE effect keyed on the INSEE, so
  a click, a shared link and a history move take the same path — a deep link
  that renders an empty card is a link nobody sends twice. The guard is a
  ref holding the INSEE, not a boolean: a status write replaces the card in
  place and must not read as a new one to fetch.
  **A FRAGMENT OR A QUERY IS NOT NOTHING TO DO.** `navigate` began with
  « same path, nothing to do » and returned early — but the write is what
  strips them, so the second lock was OFF for the commonest move there is:
  tapping the tab you are already on, signing out at home. A `#jeton=…` left
  by a `takeLinkToken` whose `replaceState` was refused, or a `?org=…`,
  stayed standing. Same path and a CLEAN url is the only true no-op; same
  path and a dirty one is scrubbed in place, adding no entry and waking no
  listener, because the view did not change.
  **A card is CLEARED before the next one is fetched.** Card to card — what
  « précédent » and « suivant » do between two of them, and what a shared
  link clicked from a card does — left the previous card on screen while the
  next was in flight: A's commune under B's address, and every control,
  « Enregistrer » included, wired to A's INSEE. A status against the wrong
  mayor, in a base the whole campaign reads. The list→card→list path, which
  is what the first tests exercised, never shows it.
  **A redirect REPLACES**: a spent link, a session that died, a card that was
  refused. « Précédent » must not walk back onto any of them.
  `href` encodes and `segmentsOf` decodes — the round trip is asserted,
  because a pipeline that writes one way and reads another matches no view
  and falls back to the home with nothing said.
  `src/testing/setup.ts` resets the location before each test — jsdom keeps
  ONE per file, so a test that navigates would otherwise start the next one
  on its screen. `e2e/09-navigation.spec.ts` presses the real button, with a
  note typed into a card: unit tests can dispatch a `popstate`, they cannot
  press « précédent ».
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
  **A grid walked at some positions and not at others certifies an agreement
  that does not hold** — which is how the SEVENTH round of this class arrived.
  The schema axis was added at the keyword positions, and the comma, which
  had its own loop, kept walking modifiers alone: `FROM accounts a,
  public.`+t named a walled table `tableRef` read and `unreadableTable` did
  not, and PostgreSQL cross-joined every campaign's rows into the answer
  (measured, two rows against ten). An axis added anywhere is added at EVERY
  position, and the test walks each position over all of them.
  **The EIGHTH round was not a table position at all**, and it is the one to
  remember: `localScope` learned every local ASSIGNMENT and no local
  DECLARATION. `const columns = "id, email FROM accounts "` followed by
  `"SELECT "+columns+"WHERE …"` resolved to a text with no FROM in it, so the
  statement named no walled table, no rule applied, and it passed in silence
  — while the same query written inline is refused. Worse in the shadowing
  direction: a local `const` hiding a package binding made the canary judge a
  DIFFERENT statement than the one that runs. A `const` holding half a
  statement is this package's own idiom. Whoever teaches the reader a new way
  a name gets its value teaches **all THREE passes** — the one that forgets,
  the one that learns back, and the one that ENUMERATES THE PATHS. The round
  that taught the first two left the third counting assignments only, so a
  branch shadowing with `const sql = "…org_id = $1…"` produced no variant, no
  branch-not-taken was ever read, and the sequential pass — which visits in
  source order and knows no block scope — overwrote the outer text with the
  branch's: the canary judged the statement the driver runs by a decoy it
  never runs, and an unbounded outer passed behind a bounded one. Written
  `sql = "…"`, that shape was caught throughout.
  **And that third pass asks whether a PATH EXISTS on which no binding branch
  runs — never what shape the branching has.** Read as the branching's own
  semantics it was false for a `switch` with a `default`, so a wall written in
  one case vouched for the default; a `select` never set it; `for` and `range`
  were not in the list at all, though a loop over nothing runs its body no
  time; and an `if` INITIALISER always runs but binds only inside its
  statement, so `if sql := "…org_id=$1…"; cond {}` left the outer statement
  standing while the reader had taken the inner one. Four shapes, one
  question asked wrongly. `TestEveryPathThatSkipsTheWallIsRead` walks them.
  **And three more that are not paths at all but READINGS the one scope per
  function could not hold**: two sibling branchings are enumerated one at a
  time, each reading applying the other in full, so the path where NEITHER
  runs — the bare statement — was the one nobody made; a closure binds where
  it RUNS, so a `defer` appending a wall walled the query in front of it and
  a closure nobody invokes walled one that never changes; and a call reads
  the text as of ITS position, so a query before a later `sql +=` was judged
  on the text that comes after it. The last one is generated only where a
  call actually SITS between two bindings — without that, `sql := base; sql
  += wall; query(sql)`, which is how half this package builds a query, would
  be refused on a reading no caller executes.
  **Assumed, and the safe direction**: a nested declaration shadowing an
  outer SQL name is read on BOTH paths, so a dead unbounded decoy beside a
  bounded statement is refused. The canary cannot tell inside a block from
  outside it; refusing loudly beats passing in silence, and the remedy is to
  rename. `TestAStatementBuiltFromALocalDeclarationIsRead` walks const, var
  and the shadow; `TestABranchThatDeclaresIsAPathOfItsOwn` walks the branch.
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
  coordination cannot mint one. It is not an account on any campaign either:
  `accounts` is keyed `(org_id, email)` and every read names the campaign, so
  the instance administrator's credentials open nothing on a subdomain. That
  is the design, not a gap — the host cannot read what a campaign organises.
- **The host GRANTS an access; it never READS behind it.** A campaign whose
  coordination cannot get in any more — address out of service, password
  nobody wrote down, no relay to send a link with — had no way back:
  `PARAPHE_ADMIN_*` only bootstrap the FIRST campaign, and one born from an
  approved request showed its one-time password once. The remedy left was an
  INSERT typed against production, which is the one kind of access nobody can
  audit. `POST /api/admin/campaigns/{slug}/coordination` opens a NEW
  coordination account in the named campaign and reads nothing else — no
  card, no note, no counter, and `TestGrantingAnAccessReadsNoneOfTheCampaigns
  Work` seeds all three and greps the answer for them.
  **It never takes over an account somebody already holds**: an address that
  already has one there is refused 409, never promoted, never repassworded
  and never REACTIVATED — deactivation is the lever a campaign keeps against
  its own host, and the address of a phished account is the one an
  administrator would naturally reuse. Seeding refuses the same thing for the
  same reason. A suspended campaign is refused too: every one of its routes
  answers 503, so a credential minted there opens nothing and says it worked.
  **The act is visible to the campaign it was done to**: `created_by` carries
  the administrator's address, which "Mon équipe" shows. A door opened behind
  somebody's back is what this route must not become.
  It is the THIRD flow minting a one-time password on that screen, so the
  rule those flows share — appended to a LIST, never assigned — is written
  once in `showPassword`. Its card key is a COUNTER: two cards can now name
  one address, and React calls duplicate keys unsupported. That last one is
  asserted through React's own complaint on purpose — keyed by the address
  the cards still rendered and still dismissed correctly, measured both ways,
  so any assertion about passwords on screen would have been green under the
  bug.
- Campaign configuration lives **in the database, per organisation**, edited
  by coordination ("Mon équipe"). The campaign `PARAPHE_*` only bootstrap the
  first one.
- **A CAMPAIGN IS NOT ITS CANDIDATE: it presents one.** `orgs.name` used to be
  copied from `candidat` at every save, so a campaign approved as « Alliance
  écologiste » renamed itself to its candidate the first time its coordination
  filled the form — in its own header, and in the apex's PUBLIC directory,
  where the name an administrator moderated is the only thing anyone
  recognises it by. It is a field of its own now, edited on the same screen,
  and `nil` leaves it alone: a client that says nothing about the name must
  not blank it, the same rule as the `listed` toggle beside it. Empty is a
  supported state — the directory has always skipped an unnamed campaign.
  `ensureOrg` still SEEDS it from the candidate, because a campaign
  bootstrapped from a file has nothing else to be called at birth, and it
  never reimposes it: reapplied at every start, an operator's
  `PARAPHE_CANDIDATE` would undo the edit silently, which is the failure the
  campaign keys and the batch size beside them already describe.
- **Not filling a field must not block a campaign; leaving the TEMPLATE in it
  must.** Three of the nine keys are the campaign's own contact details
  (`contact_tel`, `site`, `ville_envoi`), and a small team has the right to
  give a telephone number to nobody, to run without a website, and not to
  name the town its letters leave from. Empty, they no longer raise the
  "campaign not configured" banner or stop the mass mailing. Still carrying
  `06 00 00 00 00` they do, whether optional or not: that number reaches five
  hundred mayors verbatim, which is the exact failure the gate exists for and
  has nothing to do with declining to give one's own.
  The list is `noyau/campaign-optional.json`, the referee both languages
  answer to — the same dispositif as `campaign-env.json`, because
  `noyau/messages.ts` and `api/config.go` each hold a copy and a copy drifts:
  the banner and the mailing's refusal read one, `/api/config`'s `unfilled`
  reads the other.
  **And a separator with nothing on one side is a separator nobody wrote.**
  The four templates sign off with those details joined by « — », so a
  missing one left « — contact@… » at the foot of every letter. `render`
  rebuilds each line from its own parts and drops the empty ones — a line
  where nothing came out empty is returned byte for byte, and a line of prose
  using « — » has no empty part to drop. It is the orphan-paragraph rule
  beside it, one punctuation mark smaller.
- A public request form plus moderation: a request creates NOTHING until an
  instance administrator approves it. Without that, the first abuse is
  squatting a candidate's name, with no recourse for the campaign squatted.
  **Its pending ceiling is applied BY THE INSERT and its queue read carries
  no LIMIT** — the same two halves as the team form one level down, and the
  reason they are written twice is that the lesson was learned there and not
  here. Read separately, two clients both saw 999 and both wrote; the read
  then showed its newest thousand and dropped the OLDEST, the legitimate
  early requests, off the only screen that can accept them, with no decision
  ever bringing them back. `TestEveryReadOfAnAppendOnlyTableIsBounded` knows
  the exemption by name: the pending set is bounded by the insert, whatever
  is not pending keeps a LIMIT of its own.
- **A queue nobody is told about is a queue nobody reads.** Both public forms
  — a campaign asked of the instance, a team asked of a campaign — mail every
  ACTIVE access that can decide, and they share one reader
  (`noticeRecipients`) and one sender (`sendNotice`): written twice, it is the
  second copy that stops saying `active`, or answers the visitor a relay's
  failure. The instance form had no notice at all, so a request sat there
  until an administrator happened to open the screen, while the answer it gave
  promised that administration would reply.
  The three rules are the same as everywhere a public form talks to a relay.
  The SUBJECT is a constant — the campaign, team and requester names are
  visitor-chosen text, and so is the free-text message, which is in NEITHER
  body: 5000 runes of it belong to the screen that decides. The send is
  DETACHED and the pool connection handed back first — the caller is
  anonymous, and neither the relay's slowness nor its existence is theirs to
  observe (`TestAHeldRelayDoesNotHoldTheVisitor`). And a relay that is absent,
  refusing or hung changes nothing about the request: it is committed before
  any of this, and it is in the queue whoever reads it.

## Accounts and teams

Access is by individual account (email + hashed password), three roles:
coordination (sees everything, creates teams and leads), lead (opens
volunteer access for THEIR team), volunteer. A team is a name plus
departments. Campaign counters stay visible to all, without names.
`PARAPHE_ADMIN_EMAIL` / `_PASSWORD` bootstrap coordination: with no
coordination account the app refuses to open rather than let anyone in.

**The management screen is NAMED FOR THE ROLE that opens it** — « Ma
campagne » for a coordination, which has no team and holds the campaign, its
texts, its accesses and every team; « Mon équipe » for a référent, who leads
one. `gestionLabel` is that decision, in one place, because the name is
written in four (the tab, the document title, the heading, the banner that
points at it). The PATH does not move: one screen with two addresses is a
« précédent » that appears to do nothing, and a link between a coordinator
and a référent must open the same screen for both.

**A PASSWORD IS ITS OWNER'S TO CHANGE, AND CHANGING IT SIGNS THE OTHER
SESSIONS OUT.** The second half is why the first is worth having: the
commonest reason to change a password is believing it has leaked, and
without it the session of whoever took it outlives the change — a remedy
discovered by paying for it.

- **The CURRENT password is required.** A session cookie is a bearer token
  with twelve hours on it; without that proof, whoever picked one up off a
  shared computer would turn a borrowed afternoon into ownership of the
  account, with its owner locked out.
- **A wrong current password answers 403, never 401.** This interface reads
  a 401 from an authenticated route as « your session is gone » — it fires
  `SESSION_LOST` and returns the volunteer to the sign-in form — so a typo
  would have thrown them out of a live session, work and all.
- **The mechanism is `accounts.password_changed_at` against the token's own
  `iat`**, compared in `signedIn`. It costs nothing: that guard already
  re-reads the account on every request, which is what makes deactivation
  immediate, so this is one more column in a SELECT that was happening
  anyway. The column is NOT in `accountColumns` — the two other readers of
  that list hand their rows to the browser as maps, and a column added there
  travels to every manager's screen without anyone re-reading the query.
- **Truncated to the SECOND, from the SAME clock the tokens are minted by.**
  A token carries Unix seconds, so a change stored with microseconds would
  refuse the cookie the change itself just minted; and PostgreSQL's clock is
  a different one, where a second of skew would sign a volunteer out of the
  session they had just re-secured. The cost is a one-second grace: a session
  opened inside that second survives, which is nothing an attacker can aim
  at. The row is written BEFORE the new cookie, so the cookie is never older
  than the change it carries out.
- **There is no push channel**, so a revoked session falls at its NEXT
  REQUEST. A tab left untouched keeps showing a screen it can no longer write
  from — and says so the moment it tries, in the server's own words rather
  than the generic expiry, because that session did not run out, it was
  ended.
- **A floor of 12 runes, and nothing else** — no character classes, no forced
  digit: those push people to « Motdepasse1! », which is weaker than the four
  words `ReadablePassword` draws. Runes and not bytes, or a French passphrase
  is refused for being short in a unit nobody typed in. The number is written
  twice (the server applies it, the form announces it) and a canary holds
  them together.
- **A manager DRAWS one for somebody who lost theirs**
  (`POST /api/team/account/{email}/password`), which the sign-in screen had
  promised all along — « s'il est perdu, il faut en regénérer un » — with no
  route behind it: opening the access again answers 409, and an instance with
  no relay had no other door. It goes through the filter
  `routeToggleAccount` already draws, and for the same reason: a lead reaches
  the volunteers of THEIR team and nobody else, or a lead mints a password
  for a coordinator and takes the campaign. Never for oneself — that would
  show a drawn password instead of taking a chosen one, and end the session
  doing it.
  It is the FOURTH flow minting a one-time password on that screen, and the
  card key is now a COUNTER there too: one person can be drawn twice — draw,
  fail to note it, draw again — and keyed by the address the second card
  replaced the first, taking a password that exists nowhere else off the only
  screen it was on. Asserted through React's own complaint, because keyed by
  the address both cards still render and still dismiss correctly: every
  assertion about passwords on screen was green under that mutation.

**NO CARD IS ANY TEAM'S TO HOLD, AND THE CARD CROSSES WHERE THE PERSON DOES
NOT.** The two halves of one decision, and the second is what makes the first
safe.

Every team of a campaign reads every card of it. Nothing is hidden and
nothing is refused: no 403 on opening a card another team is working, no
card missing from a list or an export, no refusal to record a status. A team
wall between colleagues of one campaign cost more than it bought — two teams
called the same mayor with no way to find out, a volunteer could not look up
a commune somebody had mentioned to them, and a status the caller had
actually collected was refused because of the department it was in. **A
limitation nobody asked the tool for is a limitation the tool does not
have.**

What crosses is the CARD. What does not is the INDIVIDUAL: a card another
team is working comes back with `team_name` on it and with `volunteer` and
`volunteer_name` NULL — « travaillée par l'équipe Nord », informative, and
attributable to no person. The same line the campaign's counters have always
drawn: visible to all, nominative to nobody. Coordination sees the names.
That rule is `hidePerson`, and every card query comes through `s.cards`,
which applies it before returning a single row. Applied by HAND at three
sites it was kept by nothing: a fourth card query — a leaderboard, a
« recently statused » tab — would have shipped every other team's volunteer
addresses with the whole suite green, which is the same silence the team wall
used to fail in. `TestEveryCardQueryMasksThePerson` reads the package and
demands that a function naming `mayorSelection` come through `s.cards` or
call `hidePerson` itself. **Assumed limit, stated rather than implied**: it
walks the IDENTIFIER, so it does not see a query spelling the person's
columns out by hand — `routeExport` is exactly that, and it is pinned by a
test of its own. Matching the string `t.volunteer` instead would cry on
`mayorAvailable` and on counters that name nobody, and a canary that cries on
prose is one the next author routes around.
`s.cards` is in `queryCalls` and in the invisible-SQL exemptions, like every
other pass-through helper: it forwards a statement, so it is judged at its
callers.
**That canary's FIRST shape was one line from useless, twice over**, and an
adversarial round walked past both halves in a minute. It matched the
IDENTIFIER `mayorSelection`, so `const cardColumns = mayorSelection` renamed
the marker away; and it accepted any identifier named `hidePerson` or
`cards`, so a local `cards, err := s.rows(…)` — the most natural name there
is for a slice of card rows — read as the guard being satisfied. **A marker
must be what the DRIVER runs, and a requirement must be a CALL**: the walk
now resolves the SQL through the isolation canary's own reader, which follows
a const alias, and demands an `*ast.CallExpr`. That also retired the assumed
limit: `routeExport` spells the columns out by hand and is now walked like
the rest, so it calls `hidePerson` — through ONE probe map reused across the
whole stream, because the rule stated a second time inline is the copy that
drifts.
**`personColumn` matches the person's columns where they are SELECTED, never
where they are tested.** The join says `= t.volunteer AND`, `mayorAvailable`
says `t.volunteer IS NULL`, and the dashboard's own counter says
`COALESCE(c.name, t.volunteer) AS who` — bounded to the reader's team and
naming nobody else. A canary that cried on those is one the next author would
route around.
**In Go, not as a CASE in the SELECT.** Written in SQL it made the statement
a string the isolation canary could no longer read — and an unreadable
statement is one nothing can say the campaign is named in, which is how a
wall gets certified by a reader that gave up. It also put a parameter in a
SELECT list whose `COUNT(*)` sibling does not mention it, which PostgreSQL
refuses outright.

**The perimeter bounds ONE act: where a team is handed its work.**
`/api/batch` draws inside the departments a team was given. Reading and
recording are bounded by nothing — `/api/mayors`, `/api/facets`, a card
opened by INSEE code and the export carry no department predicate and no
team predicate. Nothing held that: adding `m.department = ANY(mine)` to the
list, for symmetry with the batch — which is exactly how it would be argued
— is one line nothing else refuses. `team_perimeter_test.go` pins both
halves in one file, so widening one cannot quietly widen the other.

**The wall that did not move is the CAMPAIGN's**, and it is absolute: the
`org_id` predicate every query carries, and the canary that reads them.
Between teams there is now information; between campaigns there is nothing.

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
- **A public form's names go through `legible` and `visible`**, which ask two
  different questions. `legible` refuses what REORDERS or BREAKS what a
  moderator reads — control characters, the bidi embeddings and overrides,
  U+2028/U+2029, a BOM — so the row they believe they are accepting is the
  one they accept. It does NOT refuse `Cf` wholesale: the zero-width
  non-joiner is orthographic in Persian, the joiner builds Devanagari
  conjuncts and every composed emoji, and the directional marks hold a Latin
  fragment inside an Arabic name. `visible` then requires at least one
  graphic non-space rune, because `TrimSpace` does not trim zero-width runes
  and a name of nothing but those reached the queue as a blank row. Both are
  POSITIVE or bounded on purpose — the list of runes that render blank has no
  end, and U+3164 and U+2800 still pass. Free text is exempt from both: a
  message is allowed its line breaks. An address goes through `storableEmail`
  as well, the same reader the team form uses.
- **The public answer carries no identity.** `team_requests.id` is one
  sequence for the whole table, hence for every campaign: returned to an
  anonymous visitor, the gap between two of them counts what the neighbours
  received, which is a number of the neighbour's.
- **A decided card leaves the queue when the DECISION answers, not when the
  reload lands.** The one-time password is shown once and stored nowhere in
  the clear; a reload that fails left it beside the very request it answers,
  and a moderator reading that contradiction either presses again (409, so
  they conclude it never worked) or discards the credential. Both moderation
  screens apply the decision to local state before awaiting the refetch.
- **A one-time password is held in a LIST and appended to, never assigned.**
  Both moderation screens have two flows that mint one — deciding a request,
  and creating outright — each behind its own re-entry guard, so neither sees
  the other. It took no race at all: deciding a second request before noting
  the first password replaced it, which is how a queue gets worked through. A
  guard read at the top of a handler cannot fix that, because the write comes
  after the await; an append cannot lose one whatever the interleaving.

## The texts a campaign sends, and who may rewrite them

The six `modeles/*.txt` are the image's. A campaign rewrites them for itself
(coordination, `POST /api/campaign/templates`), and each of its teams rewrites
them again on top (its référent, `POST /api/team/templates`). Two sparse
overlays in `orgs.templates` and `teams.templates`, resolved **team →
campaign → image** by `mergeTemplates`.

- **WHAT A LAYER DOES NOT MENTION IT INHERITS**, and it keeps inheriting: a
  team that customised the email alone follows the campaign's letter as the
  campaign CHANGES it. Copying the six in at creation would have frozen every
  campaign on the templates of the day it was approved — the failure
  `phone_outreach` describes one field over. The screen carries that rule or
  it is a trap: the inherited text is the textarea's PLACEHOLDER and never its
  value, because a box pre-filled with the campaign's letter becomes a frozen
  copy the moment anyone presses « Enregistrer », identical on screen and no
  longer following anything. `templates.test.tsx` pins it, and the mutation
  that fills the box goes red on that assertion.
- **An EMPTY value removes the override; it never stores a template of
  nothing.** That is the shape a textarea sends when somebody selects all and
  deletes, and it is exactly what « revenir au texte fourni » means — stored
  literally it is a campaign whose letter renders as one blank page, five
  hundred times. Both ends read it that way (`storableTemplates`,
  `mergeTemplates`), so a row written by an older client resolves the same.
- **REFUSED AT SAVE, never at send.** The engine is TypeScript and the API
  renders no message, but an invalid template is not a bad request — it is
  every message the campaign sends, discovered by the mass mailing on a Sunday
  evening with 1 960 letters not printed, or by a volunteer whose card shows an
  error where the message should be. Neither of them wrote it and neither can
  fix it. `api/templates.go` reproduces the engine's refusals in front of the
  person who typed the text: unknown placeholder, an email with no `OBJET:`
  line, a name outside the six, a bidi override, the size bound.
- **The vocabulary is `noyau/placeholders.json`**, the referee both languages
  answer to — the same dispositif as `campaign-optional.json`, and the copy in
  Go is a copy. The chain is `fields()` → the JSON → the Go maps, with a canary
  at each link; the direction that matters is a name in Go the engine does not
  produce, which accepts a template the engine will later refuse. What the
  editor SHOWS comes from `placeholderNames()`, derived from `fields()` rather
  than listed a fourth time.
- **THE TWO RANK VOCABULARIES ARE DISJOINT, and that is what enforces the
  project's cardinal invariant once campaigns edit the files.** Choosing the
  template by rank stops being enough: pasting « En {annee_recente}, vous avez
  présenté {candidat_recent}. » into the discovery template printed « En ,
  vous avez présenté . » to 32 866 mayors, in silence. Because the sets share
  no name, that paste is refused BY NAME, in both directions, and the refusal
  says which audience the file addresses — told only that the placeholder is
  unknown, whoever pasted it looks for a typo in a word spelt correctly.
  **The rank itself stays the engine's decision**: personalising must not let
  anyone choose which file goes to whom.
- **`maxTemplateRunes` is ARITHMETIC, not taste.** A save carries all six, a
  rune is at most four bytes, and 6 × 5 000 × 4 = 120 000 — under the 128 KiB
  a body may weigh. Any larger and a legitimate save is refused by
  `maxBodySize` instead, which answers about kilobytes to somebody who was
  writing a letter. It is also why these have a route of their own rather than
  a tenth campaign field: that body already weighs 94 616 bytes at its
  ceilings, the same reason the logo is not a field either.
- **`orgs.templates` is NOT selected by `ReadOrg`**, deliberately, and the
  column carries the note. That query runs on every request that resolves a
  subdomain — it is why the logo is a pointer and not an image — and six
  templates are tens of kilobytes read to answer a readiness probe. They are
  read in `/api/me`, once per page load, and written where they are edited.
- **`/api/me` carries the two layers apart, not one resolved set**, because
  the screen that edits them has to tell them apart: « revenir au texte de la
  campagne » is a button nobody can aim at a merged text. It is not in
  `/api/config` — that body is public and has no account, and a team's
  overlay is its team's.
  **THREE DOORS ANSWER THAT SHAPE** — `/api/me`, signing in, redeeming a link
  — and it was spelt out at two of them. The templates went into one: a
  volunteer who signed in and went straight to a card rendered from the
  IMAGE's texts while their campaign's own sat unused, until they happened to
  reload. Every unit test stayed green, because each door is asserted on its
  own; the end-to-end journey is what found it, and
  `TestSigningInSaysTheSameThingAsMe` compares the three by KEY.
  **And the body is built by the CALLER, before it commits.** `openSession`
  receives it rather than reading it: the sign-in route commits when it
  upgrades an older password hash, so a read inside `openSession` answered
  « tx is closed » — on that one path, which is the one nobody exercises by
  hand. The comment above `teamDepartments` had said so since it was written.
- **`leadOnly` is narrower than `managers` on purpose.** `managers` admits
  coordination, which belongs to no team: it would write into a row that does
  not exist and be told « enregistré » by an UPDATE that matched nothing. Its
  own texts are the campaign's, one route over — the split « Ma campagne » and
  « Mon équipe » already make on screen. A coordination cannot edit a given
  team's overlay, and that is the same line `routeToggleAccount` draws.
- **The mass mailing still reads `modeles/` off disk.** `outils/` is the
  world with no server, configured by `config/campagne.yaml`; the app is the
  world with a database. That split already exists for the telephone opt-in
  (`app.appel_telephonique` against `orgs.phone_outreach`) and is the same
  here. A campaign that customises its templates in the app and then runs
  `task messages` gets the image's.
- **TWO CHANNELS ARE NAMED « EMAIL », AND BOTH SCREENS MUST SAY WHICH.** The
  rank chooses the file, 29 807 mayors of 34 826 are `no_signal`, and the
  editor is unmounted by every tab click. Reset to the first of its list, it
  showed a volunteer who had customised the DISCOVERY email an empty box
  under the other one; they read the default placeholder as their work lost,
  retyped their text into that wrong file, and the card kept rendering the
  first version — three symptoms, reported as data loss, with nothing lost:
  both texts were stored, each on its file. Reproduced end to end before it
  was closed from both sides: the editor now OPENS on the first customised
  template, and each card panel names the template it renders in the
  editor's own words — label, audience, and the selector's « (personnalisé) »
  marker. The words are `CHANNELS` in `web/src/messages.ts`, ONE list read
  by both screens, because two lists is how they stopped naming the same
  template.
- **THE ACCOUNT-LESS VERSION ADOPTS THEM TOO**, through both doors, because a
  campaign that had rewritten its letter otherwise spoke with two voices — one
  to the volunteers with an account, one to the volunteers without, and
  nothing on either screen saying which. Only the CAMPAIGN's layer travels: a
  team's overlay is its team's, and that mode has no team.
  **ONE overlay there, not two.** In team mode the campaign's layer is LIVE —
  a coordination corrects its letter and every team that did not rewrite it
  gets the correction — so the two are kept apart and the inherited one is
  only ever a placeholder. In browser mode nothing is live by promise:
  adopting COPIES the texts, exactly as it copies the nine fields, and after
  that they are this browser's. Showing them as the value is the honest
  reading; pretending they are inherited would promise an update that can
  never arrive.
  **Bounded on the way in** (`offeredTemplates`), and that bound is the one
  thing there that is not cosmetic: this mode stores what it adopts and
  promises to hold only what its owner put there, so a campaign answering with
  a megabyte per file would fill a volunteer's disk on one click. The number
  is `MAX_TEMPLATE_RUNES`, the server's own, held in step by
  `outils/deploiement.test.ts` like the password floor. A key outside the six
  or a value that is not a string is DROPPED rather than refused — a stray key
  changes nothing, and throwing would refuse a whole campaign, nine fields and
  a logo, over it. The same reader judges what comes off the wire and what
  comes out of IndexedDB, since a restored backup or an older version can have
  written anything there.
  **The confirmation screen says so in a sentence**, not by showing six
  templates of two thousand characters: that is a screen nobody reads, and the
  volunteer can read and change every one of them on « Ma campagne » the
  moment they accept.
  **And the refusal is the ENGINE's, asked directly** (`invalidTemplate`),
  because there is no server here to reproduce its rules — run against a mayor
  who does not exist, at BOTH ranks, since a template is chosen by rank and
  checking one leaves the other to be found by a mayor.
  The save button is named « Enregistrer les modèles »: that screen now
  carries two, and two controls of one name are two a screen reader
  enumerates identically.

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
  **Presence was not the whole promise: a value the application refuses at
  STARTUP is refused at RENDER too.** The chart checked that the settings
  were there and never what they said, so a session key of five bytes, a
  `mail.publicUrl` carrying a path and a query, and a `media.publicUrl`
  carrying the semicolon that closes a Content-Security-Policy source all
  rendered — and produced exactly the CrashLoopBackOff this doctrine exists
  to prevent. CI drives each refusal separately, and its own renders used a
  one-letter key until the guard said so.
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
  **And a bucket the refund EMPTIES is dropped, because the window is as
  observable as the count.** Giving the count back and keeping the bucket
  left its reset standing, and `count` only arms a window when it finds
  none: the next caller on that key inherited the owner's, so the delay the
  429 handed back was short by exactly the time since the owner signed in —
  in seconds, in a `Retry-After` header, to an anonymous caller who chose
  the address. Measured one-for-one, four minutes of gap read as four
  minutes of difference. The shared store never had it, its `count`
  re-arming whenever `INCR` answers 1, which a key sitting at zero does:
  **the two stores answer the same callers, so any difference between them
  is readable from outside**, and both now hold the same test.
  **A refund that FAILS leaves that same oracle standing** — the attempt
  counted and its window armed — and the next round found it one step past
  the fix. It takes the shared store answering the count of a request and
  failing its refund, a failover landing between two calls; the local store
  has nothing to give back, because the count never went there. Assumed:
  retrying would hold a successful sign-in on a store already known down,
  and the residue is bounded by the class. The warning says what it leaves.
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
  (`orgs.logo_key`, `orgs.logo_type` — no digest COLUMN: the key carries
  one, and the same fact written twice is a fact that can diverge). A key is
  `logos/<slug>/<digest>-<8 random bytes>.<ext>`: the DIGEST makes a bucket
  restored from an older copy detectable and the URL cacheable for ever, the
  RANDOM half makes the key unrepeatable — see below, it is what keeps a
  database connection out of every call to the store. Replacing a logo
  writes a new key and deletes the old one, best-effort and never blocking
  the answer. The object goes in BEFORE the pointer moves: the other order
  publishes a URL that 404s.
- **Five settings, all or nothing.** Half of them refuses the start, and so
  does a store that is configured but unreachable — the same posture as an
  unreadable `PARAPHE_WEB_DIR`. None of them is the normal state of a
  developer's instance and of most tests: the routes then answer 501 saying
  so, and the header shows the hexagon.
- **NO DATABASE CONNECTION IS HELD ACROSS A CALL TO THE STORE**, and the
  eight random bytes at the end of every key are what buys that. A key
  derived from the content ALONE can come back — upload an image, replace
  it, upload the identical file again — so the deletion of an old object
  could destroy the one the pointer had just started naming. Closing that
  window meant holding a row lock, hence a pool connection, across a round
  trip to another machine; and a store that stopped answering then took
  every connection the instance had, `/health/db` included. Measured on a
  paused Garage at the pgx default of four: six uploads, **six readiness
  probes out of six lost** — a pod dropped from its Service because a
  picture would not upload.
  Unique keys end it at the root. Nothing can be written back, so a
  deletion is unconditional: `forgetLogo` reads nothing, locks nothing and
  takes no connection, and the upload route hands its connection BACK
  (`s.release`) before the store call and asks for one again after
  (`s.reacquire`). The lock still exists and still matters — read and
  replace the pointer under one lock, so each writer supersedes exactly one
  key — but it spans two local statements. Same measurement after: **six
  probes out of six at 200, and all six uploads answered** in five seconds
  instead of two being served and four refused. The cost is that the same
  file uploaded twice writes two objects rather than sharing one, which
  nothing depends on.
  `reacquire` is safe where `renew`'s rejected shape was not: it asks for a
  connection with none in hand. Asking while holding one is the deadlock
  that shape measured, so it refuses outright unless `release` has run.
  An intermediate answer — an admission gate bounding logo mutations to two
  in flight — was measured, worked, and was removed with this: a guard
  whose reason has gone is a refusal without one.
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
- **Writing a status TELLS, it does not TAKE**, and taking a batch does not
  take either. `/api/batch` hands a volunteer cards to work through; it
  grants nothing exclusive, and a card it handed to somebody else is still
  open to everyone — visible, readable, writable. What keeps two volunteers
  off the same person is the state being SEEN: whoever opens the card next
  reads « refusé », or « travaillée par l'équipe Nord », and decides.
  **Assumed limit**, and it is the whole trade: two people looking at the
  same card in the same moment can both call. A lock made the second lose a
  race and be told so, which cost more than the duplicate call it prevented
  — it is the coordination between volunteers that this application informs,
  not one it enforces.
  The one refusal left on a write is `seen`: a status is written against the
  state its writer READ, and a card somebody has moved since is a 409 rather
  than a silent overwrite. That refuses nobody a mayor; it refuses writing
  over an answer nobody saw.
  **The write lock outlived the wall by one round, and the screen is what
  caught it.** Removing the team wall from the READS left
  `assignments.volunteer IS NULL OR assignments.volunteer=$n` standing in the
  status write, so the card opened, said « travaillée par l'équipe Nord », and
  answered 409 to the save — a promise discovered by paying for it, which is
  worse than the wall it replaced. An adversarial pass found it by reading the
  new SENTENCES against the old SQL, not by reading either alone: when a
  product rule changes, the text that states it and the predicate that
  enforces it are two places, and only one of them was edited.
- **A status names the TEAM that wrote it, and nobody in it.** The other half
  of the same decision: once a status claims nothing, `assignments.team_id` —
  which names who RESERVED — is null on a card carrying a status every team
  of the campaign reads, and the status was attributable to no one. One team
  watched « signé » become « refusé » and could ask whom. `updated_by_team`
  answers, grants nothing, and stops at the team: a status crosses the teams
  of a campaign because that is what keeps two of them off the same mayor, a
  name does not — the campaign's counters have always been visible to all
  **without names**, and an address here would be that rule's one exception.
  The card says « Dernier statut enregistré par l'équipe Nord »; the notes
  behind it stay with the team that wrote them.
  **Three answers, not two**: null is a card statused before the column
  existed, `0` is `NationalTeam` — a real scope, held by every account
  carrying no team, with no row in `teams` hence no name. It reaches the
  browser as TEXT, like every other column of a card, and that is what keeps
  the national scope from reading as « nobody wrote this » to anything
  testing for truthiness. `equipeAyantEcrit` compares against
  `String(me.team_id ?? 0)`: the account says null where the card says `"0"`,
  and comparing them unnormalised makes the national scope foreign to itself.
  **The same trap, one column over, cost a second round.** The team WORKING a
  card was sent as `w.name` alone, and the national scope has no row in
  `teams` — so a batch the coordination had taken came back with a null name
  and read as « personne n'est encore dessus » on every local team's screen,
  which is how the volunteer who then worked it became the second caller. Two
  columns, like the writer: `taken_by` as TEXT decides (null nobody, `"0"`
  national, a number a team), `team_name` labels, and `equipeQuiTravaille`
  normalises. Whoever adds a third attribution adds both columns.
- **A NOTE IS CORRECTED BY ITS AUTHOR AND REMOVED BY ITS AUTHOR OR THE
  COORDINATION**, in all three modes. It used to be definitive: a typo taken
  during a call, a note recorded on the wrong commune, a word with no business
  in a register the whole campaign reads — the only remedy was an UPDATE typed
  against production, the one kind of access nobody can audit.
  **Correcting touches the TEXT alone.** `status`, `ts`, `volunteer` and
  `team_id` stay: a spelling is not what happened, and the status this line
  recorded is what the campaign reads to decide whether to call this person.
  Saying something else about the contact is a NEW status, which is the
  control directly under the history.
  **A REFUSAL SAYS WHICH OF THE TWO IT IS.** One sentence for both told
  somebody correcting THEIR OWN note, which a colleague had just removed from
  the other tab, that « seule la personne qui a écrit une note peut en
  corriger le texte » — an authorization refusal, about a note they wrote,
  which reads as a session gone wrong. The existence question is asked
  THROUGH THE READER'S OWN EYES, with the filter the card applies: a note
  they cannot see and a note that is gone both answer 404, which is the true
  sentence for them, and a note they can READ answers 403 and says it is not
  theirs. That is not the account routes' rule loosened — there, existence is
  the secret; here the line is in front of them with its author's name beside
  it. The removal's own refusal stays ONE sentence — and the whole of it is
  true of both, which is what the reason demanded and what it did not say.
  « une note se retire par la personne qui l'a écrite » is a sentence about
  RIGHTS, and it was read by somebody removing their own note that a colleague
  had taken away a moment earlier: an author told they may not be the author
  doubts their session, which is the exact failure the split next door exists
  to end.
  **Rewriting is the AUTHOR's, and the coordination is not an exception to
  it.** It removes words a campaign must not carry — a note whose author's
  access has since been closed would otherwise stay for ever — and it never
  puts different ones under somebody else's name, which is « whoever sends it
  is whoever signs it » one register down. A lead gets nothing extra: the same
  narrow line `routeToggleAccount` draws.
  **The trace is of THAT act, not of every removal a coordination makes.**
  `note_deleted` fires when the words were somebody else's, and the author
  comes back WITH the row (`DELETE … RETURNING volunteer`) rather than out of a
  SELECT beforehand, which would describe a row the DELETE may not remove.
  Fired on its own notes too — and most of what a coordination removes is its
  own, a typo it took during a call like everybody else — the line stopped
  marking anything: the one event worth finding sat in a stream of identical
  ones, with no author on the record to tell them apart by.
  **REMOVING ROLLS THE CARD BACK TO WHAT THE HISTORY THEN SAYS.** The history
  is the register and `assignments` is its head: the newest remaining note
  decides the status, its date and its `updated_by_team`, and no note left is
  a card nobody has contacted. Without it, emptying a history leaves « signé »
  that nobody ever wrote, on a mayor every volunteer then skips. The head is
  read WITHOUT the team filter the card applies — it may belong to another
  team, and filtered, a volunteer's deletion would roll the card back past a
  colleague's work they cannot see. Unconditional, hence a no-op when a line
  from the middle goes.
  **AND READING THE HEAD AND REWRITING THE CARD IS ONE CRITICAL SECTION**, so
  `restoreHead` takes the card's row `FOR UPDATE` BEFORE it reads. It is the
  same shape as the ceiling this project applies BY THE INSERT and never by a
  count read before it, and the same lesson one register over. Unlocked, a
  status recorded in between is answered 200 and then overwritten by a head
  read before it existed: measured through the real server, five rounds in
  fifteen left the card announcing « email envoyé » with the newest note
  reading « refus ». Two removals racing each other do it with no status write
  at all. `routeStatus` takes that same row lock in its `ON CONFLICT DO
  UPDATE`, so the two serialise whichever arrives first — locked here, its
  upsert waits and re-reads its own `seen` clause, which is a 409 rather than
  a lie. It is the ONLY read-then-write on `assignments`: the other three
  writers decide inside the statement that writes.
  **And the SELECT follows the card, by DERIVATION.** `Fiche` used to hold
  its status in state, seeded at mount, and nothing moved it from outside;
  a select still showing the withdrawn status wrote it straight back on the
  next « Enregistrer », with a `seen` the parent had refreshed, so the server
  accepted it — the roll-back undone by a screen that had not been told.
  What is held now is the volunteer's PICK together with the card status it
  was made UNDER, and the status shown is derived from the two: while the
  card stands still the pick stands, and the moment the card carries
  something else the card wins. Catching the state up from the prop was
  tried twice and was wrong in both directions, each measured end to end: a
  catch-up during RENDER advances its ref on a render React may discard, so
  it landed one render late — on the render the select change caused — and
  the outcome the volunteer chose was recorded as the previous one, with an
  empty note; a catch-up in an EFFECT is worse, since passive effects run
  after paint, so the screen showed the pick, the volunteer typed on, and
  the clobber arrived later. Derivation has no moment to land at.
  **A recorded pick is DROPPED where a note is removed, and nowhere else** —
  dropped before the round trip, so no frame shows the withdrawn status.
  It used to also remember the status it was made UNDER, so that a spent pick
  would not come back to life the day the card RETURNED to that status. Only
  one thing returns a card to a status it has left, and that thing already
  drops the pick: the comparison bought nothing and it cost a wrong status
  against a named mayor. A status chosen WHILE the removal was out was
  remembered under the pre-roll-back value, so the roll-back dropped it in
  silence, the select showed the rolled-back status, and the next
  « Enregistrer » — which a hurried volunteer presses without re-reading —
  filed THAT. A pick made after the drop is the volunteer's current
  intention and stands. NOT on a correction, which moves the card nowhere: written into the
  act the two share it fired on both, and a volunteer who chose an outcome and
  then fixed a typo in an older line lost the choice to the fix — silently,
  the select simply reverting, so the next « Enregistrer » filed the status
  the card already carried.
  **AND GIVEN BACK IF THE ROUND TRIP REFUSES: a refusal is not a roll-back.**
  Dropped on the way out and never restored, a removal the server turned down
  — a 404 because somebody removed the line first, a 409, a connection that
  dropped, and in browser mode the very refusal that keeps two tabs from
  overwriting each other — took the choice with it on a card that had not
  moved at all. Everything else in this interface clears a field AFTER the
  await and only on success, which is why this is the only site that owes the
  restoring; and it restores through the SETTER, so a choice made while the
  request was in flight wins over the one being handed back.
  **And the pick is keyed on the PERSON as well as on the status.** Team mode
  clears its card before fetching the next, so the card unmounts between two
  mayors; BROWSER mode derives the card synchronously from the list it already
  holds, so one card's address to another — a bookmark, a link between two
  volunteers, « précédent » between two cards — swaps the mayor with
  everything still mounted. Both being « à contacter », as most of the list
  is, a pick made on one stood on the next and « Enregistrer » filed it
  against the wrong person. It is the trap the rewritten email already paid
  for, one field over: keying on the render alone missed the identity.
  **EVERYTHING THAT BELONGS TO A CARD LEAVES WITH IT**, and the `shown` block
  is where the list of what that is lives — whoever adds a piece of per-card
  state adds it there. Keying the PICK on the person and stopping was the
  same defect asked one question short: the note EDITOR stayed open over the
  next mayor's history with the first one's text still in the box, so
  « Enregistrer la note » rewrote the line at the same POSITION on the card
  now on screen, with words written about somebody else; a « Supprimer cette
  note ? » left standing removed that line outright. Measured, both. What the
  screen SAID goes too — « Enregistré. » over a mayor nobody has written to,
  or a red alert about a refusal on somebody else, is read as this card's.
  On WHO alone, not on the render: an editor must not close because the
  campaign changed its logo. An unsaved correction is abandoned by leaving
  the card, which is what « Annuler » does.
  **AND WHAT AN ACT WRITES WHEN IT LANDS IS WRITTEN ONLY IF NOTHING HAS
  SUPERSEDED IT**, because clearing per-card state at the swap does not
  reach a request that lands AFTER it — the terminal writes happen past the
  await. The worst was `setNote("")`: the volunteer opens the next card,
  starts typing what that mayor just said, the previous request lands, and
  the field empties under their hands. Its confirmation, its refusal and its
  editor-closing went the same way, onto a card they were not about.
  **Not the mayor's IDENTITY**, which was the first answer and is one round
  short: leave a card with a request in flight, come back, start writing
  again — the identity matches, the gate opens, and the field is emptied a
  second time. A COUNTER answers the real question; anything that follows an
  act supersedes it, and leaving the card is one thing that does.
  **It gates the cleanup too**, and that is not a detail: releasing the
  re-entry guards at the swap is right — two mayors are two requests — but
  ungated, the guard the FIRST act releases when it lands is by then the
  SECOND card's, and the next click doubles its save.
  **ONE COUNTER PER KIND OF ACT.** Recording an outcome and revising a line
  have two buttons, two guards and two busy labels, and each cleans up after
  itself. Counted together, correcting a line while a save was in flight
  superseded the save's own cleanup: the guard stayed armed with nothing to
  release it and « Enregistrement… » stood on a card nobody had left, so the
  save was bricked until they went elsewhere. Symmetrically a save left an
  editor open over the correction it had just written, and it made « a
  refusal is not a roll-back » unreachable.
  **AN ACT CLOSES ITS OWN EDITOR, NOT WHICHEVER ONE IS OPEN.** On a rural
  connection a correction takes a second or two, and the volunteer moves to
  another line and starts writing what the mayor is saying; closed
  unconditionally, the landing act took THAT editor away and every character
  with it — in silence, because the sentence about an abandoned correction
  only fires when one editor replaces another, not when one is taken away.
  Opening an editor supersedes nothing, so the counter has nothing to say
  about it: what is compared is the editor itself.
  **AND « ANNULER » DOES NOT OFFER TO CANCEL WHAT IS ALREADY OUT.** Pressed
  while the request was in flight it closed the question — which reads as
  « nothing was removed » — and the note went anyway, with « Note supprimée. »
  underneath. It cannot be taken back, so the control refuses and says so
  rather than promise what it cannot do. Both cancels, the editor's and the
  confirmation's.
  **AND TYPING SUPERSEDES NOTHING**, which is why a counter cannot be the
  whole answer: on a weak connection the button reads « Enregistrement… » for
  a second or two and a volunteer still on the telephone goes on writing, on
  the same card, with no other act in between. What a save clears is WHAT IT
  SENT, compared through the setter.
  Four rounds of adversarial review walked past four successive answers here
  — identity, then one counter, then the ungated cleanup, then the counter
  itself — and each walk-past was reproduced before the next answer was
  written.
- **A CALL NOTE IS WRITTEN IN LINES**, and the history swallowed them:
  « rappeler avant 11 h » over « secrétariat: Mme X » came back as one run of
  words. The editor kept them all along, which is what made it invisible.
  `.note-texte` is `pre-wrap`, and the rule is pinned by reading the
  stylesheet — jsdom loads none, so a computed style would answer the empty
  string whatever the CSS says.
- **ONE EDITOR, ONE DRAFT — AND ABANDONING IT IS SAID.** Kept per line and
  keyed like the rows, it inherited the rows' own instability: browser mode
  names a note by its POSITION, a removal shifts every position newer than it,
  and a draft typed for one line came back under whichever line inherited its
  number. The volunteer opened what they took for their own recent draft,
  pressed « Enregistrer la note », and the wrong note was overwritten with
  words about another contact — measured. A map keyed by anything a note
  carries has the same shape of problem one collision further out; one draft
  has none, because the editor it belongs to is on screen. What it costs is
  the rewrite in progress when another line is opened, and that is not
  swallowed: the screen says so, which is the rule the rewritten email already
  follows one panel up.
  **Assumed**: the draft dies with the card, like the editor it is in. The
  email and the call note survive a tab click through `cardDrafts` because
  they are VISIBLE and pre-filled; a closed editor claims nothing.
- **AN ACT IS AIMED AT A LINE, NOT AT A POSITION**, and that is `noteKey`'s
  limit rather than its meaning. It is the REACT key — unique within the list
  by construction, which two notes of one minute and one outcome are not, and
  React calls duplicate keys unsupported. Held as the AIM it re-bound the
  moment the history changed length, in both directions and with no race in
  either: on a REFUSAL, `reviseNote` re-read the record, the standing
  « Supprimer cette note ? » slid one line along, and « Confirmer » removed a
  note nobody had pointed at; on a SUCCESS, a removal landing while an editor
  was open on another line — which is what « an act closes its OWN editor »
  leaves standing — moved that editor onto a third line and
  « Enregistrer la note » wrote the volunteer's words into it. Measured, both.
  So the aim is the LINE, by value (`sameLine`), and an act renders under
  whichever row IS that line; a line that has gone takes its act with it,
  because there is then nothing to confirm and nothing to correct. What is
  compared is what a CORRECTION does not touch — an id where the server gives
  one, otherwise the moment and the outcome. **The text is left out on
  purpose**: a correction another window has just landed would otherwise take
  this one's editor away with the words still in it, which is the one thing
  the refusal below must not do.
  **AND `sameLine` IS NOT AN IDENTITY**, which is what the round after found:
  leaving the text out means two lines answer to it alike — an afternoon of
  « à rappeler » produces those — and a line that never existed before can
  INHERIT it, a colleague having removed the aimed one and recorded their own
  contact in the same minute. Matched row by row it opened an editor on every
  match, sharing one draft; matched against a line that had merely inherited
  the minute, « Enregistrer la note » replaced somebody else's words with a
  correction written for another call. So it is resolved ONCE against the
  whole list (`aimedAt`), preferring the line the act was TAKEN on — text and
  all — and falling back to a same-minute, same-outcome line only when no such
  line is left; that fallback is the concurrent-correction case and nothing
  else.
  **AND WHAT GOES OUT IS WHAT THE VOLUNTEER READ.** The act carries the line
  it was AIMED at, not the row it happens to render over: browser mode sends
  it as its `seen` — where a note is named by position PLUS content — and team
  mode takes the id off it. An inherited line is then REFUSED by the store
  instead of being written into, which is the difference between a fallback
  that keeps an editor open and one that hands it a stranger.
  Two notes of the same minute, the same outcome AND the same text remain
  indistinguishable, which is the limit browser mode already states.
  **And a line that has gone taking its act with it is SAID**, because the
  words typed into that act go with it — a colleague removes the very line an
  editor is open on and the box simply disappears. « La correction en cours a
  été abandonnée » does not fire there: that sentence is for opening ANOTHER
  editor. The new one is DERIVED, never written to state: a render-phase
  `setSaved` would survive the render that swaps the card, which React
  discards, and land on the next mayor with its output nowhere.
  **Said where something is LISTENING, and in the SHELL.** Written as a
  paragraph of its own inside the history card it did neither: a region that
  appears together with its text announces nothing, so the one person the
  sentence exists for heard silence; and a card holding ONE note is the
  ordinary shape of a mayor contacted once, so removing that note took the
  card, the sentence and the editor away together. It is written from an
  EFFECT — the one place a write is safe, since an effect runs only after a
  render that COMMITTED — into the region that already carries what an act
  did, and the act itself is cleared with it: an aim with no line is not a
  state to keep.
  **ONE SENTENCE PER EVENT.** A second region beside it was tried and it says
  the wrong thing: the store's refusal is written for a line that is STILL
  THERE — « une autre fenêtre l'a modifiée. Annulez pour voir son texte. » —
  so beside the true sentence it named an editor that had just gone, and a
  screen reader read the two one after the other. Clearing that slot from the
  effect does not reach it either: the refusal lands AFTER, because it is the
  act's own report. What reaches it is knowing that its line went
  (`lostItsLine`), and the refusal is then not said at all. **The pick is
  still handed back** — a refusal is not a roll-back, whatever became of the
  line.
  **And the focus is CAUGHT**, because the control the volunteer was holding
  died WITH the row and no click ran: `holdFocusThrough` arms on an act, and
  there is no act here.
  **Or WORSE than died**, and that half was found a round later: `noteKey` is
  positional in browser mode, so React REUSES the row's DOM when a line
  vanishes — the focused « Enregistrer la note » became « Modifier » under the
  very same element, focus never fell anywhere, and the next Enter opened an
  editor on somebody else's line. So the history block counts as well as
  `<body>`: any focus still inside it is on a control the removal has just
  renamed. Outside it, the volunteer moved on themselves, and it is not ours
  to take.
- **A REFUSAL IN BROWSER MODE REFRESHES WHAT THE SCREEN HOLDS.** That mode
  reads its store ONCE, at load, so « rouvrez la fiche » sent the volunteer to
  the list and back to exactly what they had, and the second attempt was
  refused the same way; only a full reload worked, and nothing said so. A
  refusal means one thing — another window wrote — so `reviseNote` re-reads
  that one record and hands it to the screen before re-raising. The sentence
  then names a gesture that DOES something: the volunteer's own editor is open
  over that line, holding text a refusal must not throw away, so « Annulez
  pour voir son texte » is what is left to say.
  **Assumed**: correcting a line to nothing is allowed and stores an empty
  note. It is not the same act as removing the line — removing rolls the card
  back, and somebody who recorded the right outcome and wrote the wrong words
  wants the status kept. Refusing would push them into the control that
  undoes more than they asked.
  **A save is finished when the note field clears, not when the history
  shows the line**: the line is drawn from state written INSIDE the awaited
  call, while the handler goes on to clear the field and say « Enregistré. ».
  Both end-to-end journeys wait on the field, and before they did they typed
  into it in between — and recorded an empty note under the previous status.
  **`mine` is a boolean the server computes, never the address it comes
  from**: it is what puts « Modifier » on a line, and what refuses is the
  predicate in the route. The two rights are separate on screen because the
  routes behind them are two.
  **A row's buttons are named by their POSITION and their date.** Every line
  carries the same two, so their visible names repeat down the list and a
  screen reader enumerates them identically; the date alone does not separate
  them either, because two outcomes recorded in the same minute — an email
  sent and the call that followed — share a `ts`. « la note 1 » is the one
  just recorded, the history being newest first.
  **The editor closes one commit AFTER the card comes back**, and until it
  does its row carries no « Supprimer » — so a click aimed at that row lands
  on the row BELOW and removes a note nobody meant to touch. Both journeys
  wait for the editor to be gone.
  Browser mode names a note by its POSITION plus the content the screen was
  showing — the `seen` of the team version, on this side of the wire. It gains
  no identifier: a note already written would have none, and the two paths
  would have to be told apart for ever.
  **THAT CHECK AND THE WRITE IT GUARDS ARE ONE IndexedDB TRANSACTION**
  (`reviseTracking`), or the check is worth nothing. Read in a readonly
  transaction and written in a second one — which is how `saveTracking` had
  always done it — two callers see the same array, both write, both are told
  it worked, and one of the two pieces of work is gone. Two tabs on one card
  is all it takes, and this store is the only thing browser mode owns: no
  server holds a second copy. IndexedDB queues readwrite transactions over a
  store, so inside one the read sees every write committed before it. A
  refusal returns null rather than aborting: an abort fires `onabort`, not
  `onerror`, and a promise waiting on the other two never settles.
  **`timestamp()` HAS MINUTE GRANULARITY, so a test that does not FREEZE TIME
  cannot tell one date from another.** A whole file runs inside one minute,
  and every assertion comparing a note's `ts` with a fresh `timestamp()`
  compares a string with itself: `editNote` made to rewrite the card's date
  and `deleteNote` made to date the card NOW both left all nineteen tests
  green. `db.test.ts` fakes Date alone — fake-indexeddb runs on real timers —
  and gives each note its own minute.
  **AND `shortTimestamp()` HAS THE SAME GRANULARITY, so the lesson was learned
  on one side of the wire and not the other.** A Go test that writes a note,
  corrects it and asserts inside one minute carries the same string in `ts`, in
  the window it opened and in the one it closed — so `edited_at=ts`, which
  announces that a correction happened when the CONTACT did, satisfied the
  bound with all 429 tests green. There is no injectable clock behind
  `shortTimestamp`, so the note is BACK-DATED before the window opens: the
  fixture is what makes the mark's value checkable, and « modifiée le » is a
  date the whole campaign reads.
- **THE ANSWER THAT LANDS LAST IS NOT THE ONE THAT KNOWS MOST.** Every write
  on a card answers WITH the card, re-read inside its own transaction, so each
  answer is a true snapshot of a DIFFERENT moment. One screen now holds three
  of them — a status, a correction, a removal — and whichever landed last
  wrote `chosen`. Measured on a rural connection: a correction delayed two
  seconds, « A signé » recorded while it was out, and the correction's answer,
  taken before that status existed, put the screen back to a history without
  it. The server had the signature; the volunteer was looking at a card saying
  it had never been written, and their next move is to record it again or to
  ring the mayor back. An answer is shown only if nothing has been ASKED since
  (`showsCard`), and a card being FETCHED counts — a write answering after the
  volunteer moved to another mayor would otherwise put the previous one back
  on screen, which is « A's commune under B's address » one level up.
  **AND A REFUSED ANSWER IS NOT DROPPED, IT IS ASKED AGAIN.** Ordering by
  ask-time alone INVERTS this defect rather than closing it, and the round
  after measured the inversion: delay the REQUEST instead of the response and
  the server commits the correction AFTER the status, so its answer carries
  both and is the fresher one — dropped for having been asked first. The
  screen then said « Note modifiée. » over the text as it was before, and
  « Note supprimée. » under a line still on screen with its buttons, while the
  whole campaign read the other thing. Arrival order and ask order are both
  wrong, because neither is COMMIT order and no client can see that one.
  A READ can: a query started later runs on a snapshot at least as new as one
  started earlier, so the last-ASKED read is the freshest, full stop. A
  refused answer therefore asks the server once more, and only there — two
  writes that did not overlap pay nothing. **Assumed**: the second question
  is not asked when a CARD LOAD is refused, because what refused it is the
  volunteer moving on, and what they moved to is already on its way.
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
