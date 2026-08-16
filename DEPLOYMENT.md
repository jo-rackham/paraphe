# Deploying the application on a server

## In short

```bash
cp .env.exemple .env            # then fill in: secrets, database password,
                                # campaign identity
task build                      # (re)generates the embedded mayor list
docker compose up -d --build    # PostgreSQL, Valkey and the app, on :8047
```

Three containers: PostgreSQL, Valkey (the shared rate-limit counters) and
**one** application image, which serves the pages and the JSON alike.

The application is **stateless**: accounts, assignments, statuses and notes
all live in PostgreSQL. That is what lets it run as several instances — and
it is also what must be backed up.

Then an HTTPS reverse proxy in front. With Caddy (`Caddyfile`):

```
parrainages.mydomain.fr {
    reverse_proxy 127.0.0.1:8047
}
```

(Without a domain: a `cloudflared` tunnel or Tailscale Funnel pointed at port
8047. Without Docker: `task web-build` then `task api` behind the proxy, with
the same environment variables — in that mode the API serves the interface
itself and there is nothing else to install.)

**One image.** It serves the pages and the JSON, from one process, on one
port. There is no interface version and API version to keep in step, because
there is one artefact. Nothing is written to disk: the image is built `FROM
scratch` and the chart mounts no volume.

## Runtime configuration (environment variables)

Every value in `config/campagne.yaml` can be overridden by a `PARAPHE_*`
variable — the recommended route on a server (full list and examples in
`.env.exemple`):

| Variable | Role |
|---|---|
| `PARAPHE_CANDIDATE` | candidate name (appears in every message) |
| `PARAPHE_CANDIDATE_DESCRIPTION` | one factual line (email, phone script) |
| `PARAPHE_CANDIDATE_DESCRIPTION_LONG` | 2-3 first-person sentences (letter) |
| `PARAPHE_SIGNATORY` / `_ROLE` | default signature (mass mailing) |
| `PARAPHE_CONTACT_PHONE` / `_EMAIL` / `PARAPHE_SITE` | campaign contact details |
| `PARAPHE_SENDING_CITY` | the place in the letter's dateline |
| `PARAPHE_BATCH_SIZE` | mayors handed out per "take a batch" (default 10) |
| `PARAPHE_ADMIN_EMAIL` / `_PASSWORD` / `_NAME` | coordination account, created at first launch and its password refreshed at every start — **mandatory at first launch**. A deactivated account is NOT switched back on: the password an incident revoked stays revoked |
| `PARAPHE_SECRET_KEY` | session signing secret — `openssl rand -hex 64`. Refused below 32 bytes: it signs every session, and a short one falls to an offline search on a single captured cookie |
| `PARAPHE_LOG_LEVEL` | `debug`, `info` (default), `warn` or `error`. Panics, 500s and refused waves are logged at `warn` or above, so they survive any level an operator picks |
| `PARAPHE_SOURCE_URL` | public repository URL: shows "source code" in the footer |
| `PARAPHE_BROWSER_VERSION_URL` | URL of the account-less browser version offered on the instance home page — defaults to the self-hosted `/navigateur/` when the image serves one; set to point elsewhere |
| `PARAPHE_BROWSER_WEB_DIR` | build of the browser version served under `/navigateur/` (the image sets it; empty serves none) |
| `PARAPHE_WEB_DIR` | the interface the binary serves (the image sets it). Unreadable, **the start fails** — one image serves the pages and the JSON, so a 404 on every page while `/api` answers is what a probe calls healthy and a volunteer calls a blank screen. Set explicitly empty, it means "JSON only" |
| `PARAPHE_DATABASE_URL` | PostgreSQL DSN — **mandatory**, the app refuses to start without it |
| `PARAPHE_HOST` / `PARAPHE_PORT` | listening interface and port |
| `PARAPHE_BASE_DOMAIN` | domain of the campaign subdomains — **empty = a single campaign** (see below) |
| `PARAPHE_ORG_SLUG` | subdomain of the campaign described by the variables above (default `campagne`) |
| `PARAPHE_INSTANCE_ADMIN_EMAIL` / `_PASSWORD` / `_NAME` | instance administration: approves hosting requests. Required in multi-campaign mode |
| `PARAPHE_VALKEY_URL` | shared rate-limit counters: `valkey://host:6379`, or `valkey+sentinel://h1:26379,h2:26379,h3:26379/paraphe`. Empty = counted in process memory, which is exact for ONE replica and says so at startup |
| `PARAPHE_VALKEY_PASSWORD` | Valkey password — never inside the URL, which travels in process lists and deployment files |
| `PARAPHE_TRUSTED_PROXIES` | CIDRs whose `X-Forwarded-For` is believed (your TLS proxy, the ingress). Empty = every request is attributed to its TCP peer — behind a proxy, everyone then shares one counter. `docker-compose.yml` and the chart both set the private ranges; **set it yourself only when running the binary directly** behind a proxy |
| `PARAPHE_SMTP_URL` | relay carrying the sign-in links: `smtp://user@host:587` or `smtps://user@host:465`. Empty = this instance sends nothing, and signing in by link is off everywhere. **STARTTLS is required, not attempted**: a relay that does not offer it is refused rather than served in the clear, because these messages carry a credential. The loopback is the exception, for a relay running beside the process |
| `PARAPHE_SMTP_PASSWORD` | relay password — never inside the URL, which travels in process lists and deployment files |
| `PARAPHE_MAIL_FROM` | sender of those messages, `Campagne <contact@exemple.fr>`. Required as soon as `PARAPHE_SMTP_URL` is set |
| `PARAPHE_PUBLIC_URL` | the origin those links point at (`https://paraphe.org`). Required with `PARAPHE_SMTP_URL`, and **never derived from the `Host` header** — anyone can set that one, and the link would leave over your campaign's name pointing at their server. Multi-campaign: give the apex, each campaign's subdomain is prefixed to it |

A variable left unset falls back to the embedded `campagne.yaml` (or to
`config/campagne.local.yaml`, git-ignored, if you prefer a file).

Two behaviours, deliberately different:

- a **secret** left at the repository's example value (`PARAPHE_SECRET_KEY`,
  `PARAPHE_ADMIN_PASSWORD`) → **refusal to start**: those values are public,
  and accepting them would hand over an open instance;
- a **campaign value** left unfilled → the app starts and shows a warning
  banner on every page, but the mass mailing refuses to run. You can explore
  the tool before configuring it.

The session cookie is always `Secure`, `HttpOnly` and `SameSite=Lax`, and
none of the three is configurable: an operator cannot forget one. Browsers
treat `http://localhost` and `http://127.0.0.1` as secure contexts and accept
the cookie there, so a local trial works; a deployment served over plain HTTP
on a real host does not, which is the point.

## Accounts, local teams and walls

Access is by **individual account** (email + password), not a shared code — so
that who contacted whom is known, and one access can be cut without disturbing
the others.

- At first start, `PARAPHE_ADMIN_EMAIL` + `PARAPHE_ADMIN_PASSWORD` create the
  **coordination** account. Without them nobody can get in, and the app says
  so instead of opening.
- Coordination creates the **teams** (a name, some departments) and their
  **leads**. Each lead then opens access for their volunteers: the app
  generates a temporary password, shown **once**.
- **Signing in by email** (only when `PARAPHE_SMTP_URL` is set): the sign-in
  screen offers a link, and opening an account sends an invitation carrying
  one. It is what a volunteer who forgot their password has — there is no
  other recovery. The token lives 15 minutes (7 days for an invitation), is
  single-use, is stored only as a SHA-256, and travels in the URL's
  **fragment**: never sent to a server, hence in no access log, and invisible
  to the URL scanners corporate mail systems run — which otherwise spend a
  one-shot link before its recipient clicks it. The generated password is
  still shown either way: a relay can be down, and reading it out is the path
  that always worked.
- A lead can only create volunteers, and only in their own team. Any attempt
  to raise the role or change team is ignored server-side.
- **Walls**: a team sees its own reservations and the free pool; cards
  reserved by another team are refused (403). Campaign counters (statuses,
  departments covered) stay visible to everyone, **with no names**.
- Deactivating an account takes effect at the next request, without waiting
  for a sign-out — **and it survives a restart**, including for the account
  `PARAPHE_ADMIN_EMAIL` seeds. Startup then refuses rather than serve a
  campaign whose only coordination is switched off.

## Data and backup

- The mayor list is **in the API image** (frozen at build time); the team's
  work (accounts, assignments, statuses, notes) is in PostgreSQL.
  **That is the only IRREPLACEABLE data** — and, since campaigns may upload
  a logo, no longer the only data. The logos live in the object store
  (`task backup-media`), and losing one costs a campaign the thirty seconds
  it takes to upload it again: worth copying, not worth an incident. What
  PostgreSQL keeps is the POINTER — which object should exist and its type,
  the key ending in a digest of the content — so a bucket restored from an
  older copy is detectable rather than silent. Putting the copy back is
  `task restore-media DOSSIER=media-2026-08-15`, which ADDS and never
  deletes: the keys are unique, so a copy from yesterday cannot overwrite a
  logo uploaded since. **Restore with that task and not with a plain
  `rclone copy`**: an object is not only its bytes, and the copy carries
  neither the `Content-Disposition: attachment` an SVG is stored with — the
  header that stops a hostile one from being a page on the media origin —
  nor its `Cache-Control`. The task puts both back, which is why it makes
  two passes. In Docker:
  `docker compose exec postgres pg_dump -U paraphe paraphe > backup-$(date +%F).sql`
  In Kubernetes, enable `postgres.cnpg.backup.*`: the chart then sets up WAL
  archiving **and** a daily `ScheduledBackup`. Both are needed — WAL archiving
  alone restores nothing.
- **Restoring**, in Docker: `task restore -- backup-2026-08-15.sql`. It stops
  the API first, recreates the database and replays the dump. Stopping the API
  is not optional: it runs its own DDL and re-imports the list at startup, and
  doing that over a half-written database is how a restore ends up worse than
  the incident. In Kubernetes, restore through CloudNativePG (`Cluster`
  `bootstrap.recovery`), never by piping a dump into a running cluster.
- **A backup nobody has restored is a hypothesis.** `task backup` now writes a
  `.partiel` and renames it only once the dump is non-empty — the shell's `>`
  used to create the file before `pg_dump` ran, so a failure left a 0-byte
  backup carrying today's date, which rotation keeps and only a restore
  reveals.
- Updating the list (a new `task build`): `docker compose up -d --build`
  re-imports as an UPSERT. Data (email, rank, score) is refreshed, work
  columns (volunteer, status, notes) are untouched. A target removed from the
  list is deleted if nobody worked on it, and flagged "RETIRÉ" otherwise.

## The campaign logo (optional)

A campaign may upload a logo — PNG, JPEG, WebP or SVG, 64 KiB at most — from
"Mon équipe". It shows beside the paraphe mark in the header and on the
campaign's sign-in page. Configure nothing and campaigns simply have none:
the header keeps the hexagon and the screen says so.

- **The browser fetches it from the object store, not from the
  application.** That needs an origin of its own —
  `PARAPHE_MEDIA_PUBLIC_URL`, typically `media.<your domain>` — and it is
  the ONE remote origin the Content-Security-Policy allows. A wrong value
  shows as an image that never appears, in the browser console and nowhere
  else.
- **A separate host, not a path under the application's.** The session
  cookie is host-only, so a hostile SVG served from the media origin finds
  neither the cookie nor the application's DOM. Under `/media/` of the same
  host it would be at home. The uploads are validated too — the bytes decide
  the format, and an SVG carrying a script, an event handler, a DOCTYPE or
  an external reference is refused — but the origin is what makes that
  validation the third line of defence rather than the only one.
- **Five variables, all or nothing** (`PARAPHE_MEDIA_ENDPOINT`, `_BUCKET`,
  `_ACCESS_KEY`, `_SECRET_KEY`, `_PUBLIC_URL`). Half of them refuses to
  start: a campaign that can upload a logo nobody can fetch is worse than no
  logo. Configured and unreachable also refuses to start — the alternative
  is a coordination discovering at upload time that the deployment was never
  finished, behind a probe that has been green for a week.
- **In Docker**, the compose file runs one Garage node and bootstraps it.
  **In Kubernetes**, the chart runs three (`media.enabled=true`), one per
  machine, replication 3 — the loss of a node costs nothing. A Job lays the
  cluster out at every install and upgrade, because a Garage layout is
  imperative and has no declarative form.
- **On a cloud**, run no storage at all: `garage.enabled=false` and
  `media.endpoint` pointing at the provider's object store (OVH, Scaleway,
  R2). The application only ever speaks S3.
- **The key name `paraphe` belongs to the bootstrap**, in a Garage the chart
  manages. Rotating `secrets.mediaAccessKey` leaves the previous key able to
  write, so the Job revokes the ones it recognises — and it recognises them
  by that name. Give your own keys (a backup job, a migration script) any
  other name and they are left alone; call one `paraphe` and the next
  `helm upgrade` revokes it, with no way back since Garage never reissues an
  identifier.

## Several campaigns on one instance

An instance can host **several campaigns**, each on its own subdomain. Set
`PARAPHE_BASE_DOMAIN`:

```
PARAPHE_BASE_DOMAIN=paraphe.fr
```

From then on:

- `paraphe.fr` serves the **instance home page**: the public hosting request
  form, and the administration sign-in;
- `<campaign>.paraphe.fr` serves **one campaign** — its teams, its work, its
  configuration, invisible to the others;
- a request creates **nothing**: an instance administrator approves it, and
  approval is what opens the campaign and returns, once, its coordination
  password. Without that moderation, the first abuse is squatting a
  candidate's name, with no recourse for the campaign squatted;
- each campaign's coordination fills in its own configuration in the app ("Mon
  équipe"). The campaign `PARAPHE_*` only bootstrap the FIRST one.

**Leaving `PARAPHE_BASE_DOMAIN` empty stays the default**: every host then
serves the single configured campaign, with no particular DNS.

### What separates two campaigns

One thing: **every query on a per-campaign table names the campaign**. The
`org_id` predicate is bound as `$1` by a single constructor, and two tests
carry it — one reads the source and demands the predicate table by table, the
other runs two campaigns on one instance and checks that nothing of the
neighbour comes back.

**The PostgreSQL role's privileges have no bearing on this.** CloudNativePG's
`<cluster>-app` role fits, and so does the official PostgreSQL image's.

### Kubernetes + HAProxy Ingress

The chart renders the Ingress with **two hosts** as soon as
`instance.baseDomain` is set: the apex, and the wildcard `*.<domain>`. The
wildcard is what allows opening a campaign without touching the cluster —
otherwise every approval would mean editing the Ingress, hence giving the
application write access to the Kubernetes API.

```bash
helm upgrade --install paraphe chart/paraphe \
  --set ingress.enabled=true --set ingress.className=haproxy \
  --set ingress.tls.enabled=true \
  --set ingress.host=paraphe.fr \
  --set instance.baseDomain=paraphe.fr \
  --set instance.admin.email=admin@paraphe.fr \
  --set secrets.instanceAdminPassword="$(openssl rand -hex 24)" \
  --set secrets.adminPassword="$(openssl rand -hex 24)" \
  --set secrets.secretKey="$(openssl rand -hex 32)" \
  --set secrets.valkeyPassword="$(openssl rand -hex 24)" \
  --set postgres.cnpg.backup.enabled=true \
  --set postgres.cnpg.backup.s3CredentialsSecret=paraphe-s3
```

**The chart refuses to render** without `secrets.secretKey`,
without `secrets.valkeyPassword` (while `valkey.enabled`), and with
CloudNativePG but no backup. The first two because it cannot draw them
itself: rendered client-side — `helm template`, and ArgoCD by default — it
cannot read the Secret already in place, so a random fallback would mint a
NEW one at every sync, signing every volunteer out and leaving the
application and the Valkey pods refusing each other's password. The third
because a database with no base backup AND no WAL archiving loses the only
irreplaceable thing here; if durability is covered elsewhere (Velero,
storage snapshots), say so with
`--set postgres.cnpg.backup.acknowledgedDisabled=true`.

Passing them through a `Secret` you manage yourself — ExternalSecret,
SealedSecret, SOPS — is `--set secrets.existingSecret=<name>`, and the keys
it must carry are listed in `values.yaml`.

On the infrastructure side:

- **DNS**: `paraphe.fr` AND `*.paraphe.fr` to the cluster's public IP (the
  HAProxy Ingress Service's). Without the wildcard record, every campaign
  opened would be unreachable.
- **A WILDCARD certificate**: Let's Encrypt only signs a `*` over **DNS-01** —
  HTTP-01 cannot. That means a cert-manager `ClusterIssuer` with the DNS
  provider's API token, and the matching annotation on the Ingress
  (`ingress.annotations`). One certificate per campaign, issued on the fly,
  would require creating an Ingress resource at every approval: the same
  problem as above.
- The Ingress wildcard host covers **one level only** (`a.paraphe.fr`, not
  `a.b.paraphe.fr`). The application applies the same rule, so the two never
  diverge.
- Probes query `/health/db`, which resolves no campaign: kubelet addresses the
  pod's IP, whose host name matches no subdomain. A probe on `/api/config`
  would leave every pod forever "not ready".

### Who sees what

- A campaign sees **only** its own work: its teams, its accounts, its notes,
  its reservations. The mayor list is **common and read-only** — public data,
  identical for everyone, and duplicating it per campaign would copy 34,826
  rows for nothing.
- The instance administration reads **no** campaign data: its scope is
  separate. It sees the requests and the list of campaigns, not the
  volunteers' notes.
- A session is valid for **one** campaign: the cookie carries its identifier
  and is refused elsewhere.

## Security: what this is up against

Individual accounts, hashed passwords, per-team walls: right for an activist
campaign, not for an open public service. The proxy's HTTPS is indispensable —
passwords travel at sign-in.

The mayor data is **public** (Conseil constitutionnel, national register,
public service directory). What is sensitive is **the team's notes**: who said
what, who is hesitating, who refused. Hence HTTPS, named accounts, encrypted
backups if they leave the server, and deleting the notes when the campaign
ends.

**Rate limits** guard the doors, generously enough that a campaign office
behind one NAT never meets them. Each refusal logs its class, so a `429` a
volunteer reports is read straight off `rate_limited class=…`:

| class | ceiling | keyed on |
|---|---|---|
| `signin_ip` | 60 / 10 min | source |
| `signin_account` | 10 / 15 min | submitted address |
| `hosting_ip` | 3 / hour | source |
| `anon_ip` | 120 / min | source |
| `write_account` | 120 / min | account |
| `export_account` | 6 / 10 min | account |

The ceilings are constants, not settings: one an operator can raise under
pressure is one raised on the night it was protecting something. With
several replicas the counters must be shared or every
ceiling silently multiplies: that is what Valkey is for — one node in the
compose file, a three-node Sentinel group in the chart, automatic failover.
Its content is disposable TTL'd counters, so it has **no volume anywhere**:
losing it re-opens the windows, nothing more, and the application counts per
instance (and says so) whenever Valkey is unreachable.

**No client address in the clear, anywhere.** The limiter's keys are keyed
hashes of the source (IPv4 address or IPv6 /64) and of the submitted email;
the security events (`signin_failed`, `rate_limited`, `account_toggled`…)
carry day-scoped pseudonyms — correlatable across replicas within a day,
non-reversible, unlinkable across days. The logs never become a second
nominative store, and there is deliberately no fail2ban feed: the ban IS the
in-application ceiling. A 429 is identical for an address that has an
account and one that does not, so the limiter answers nothing the decoy
hash refuses to.

## Personal data

This repository tools a processing of personal data: surname, first name,
civility, commune and town hall contact details of 34,826 identified people,
plus the notes the team takes about them. **The team that deploys is the data
controller** — not the author of the repository.

- **Legal basis**: legitimate interest (GDPR art. 6.1.f), political
  communication towards elected officials in the exercise of their office,
  from official publications.
- **Informing the data subjects** (art. 14): the templates state where the
  data comes from at first contact, and the guide requires answering
  precisely if an official asks.
- **Right to object** (art. 21): that is the "do not contact again" status. It
  is final, and the team must never work around it.
- **Retention**: the notes are only useful during the campaign.
  `task purge-notes` erases them and resets the assignments. Run it after the
  Conseil constitutionnel publishes the endorsements.
- **What is sensitive** is not the contact details (public) but the notes: who
  is hesitating, who refused, what a mayor said in confidence. Hence the
  per-team walls, HTTPS and encrypted backups.

## Pre-filling the browser version (optional)

Volunteers working without an account, in their browser, otherwise retype the
campaign's nine fields — and a typo goes out to mayors under the campaign's
name. Built with `PARAPHE_BASE_DOMAIN`, the interface accepts `?org=<slug>`
and offers to take the campaign published by `<slug>.<domain>`:

```
PARAPHE_BASE_DOMAIN=paraphe.fr PARAPHE_BASE_PATH=/paraphe/ pnpm --dir web build
```

What that exposes: `GET /api/campaign/public` returns the slug, the name and
the nine campaign keys — nothing else, and with `Access-Control-Allow-Origin:
*` since those are exactly the values that already go out in every message to
a mayor. No session, no cookie.

**The parameter names a CAMPAIGN, never a host**: the domain is fixed at build
time. A forged link therefore cannot slip a third party's contact details
under a real candidate's name — that would require having a campaign approved
on your instance. The volunteer sees the values before accepting them anyway,
and a campaign still at its template values pre-fills nothing (409).

Leave `PARAPHE_BASE_DOMAIN` empty at build time and `?org=` does nothing at
all.
