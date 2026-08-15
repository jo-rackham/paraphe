# Deploying the application on a server

## In short

```bash
cp .env.exemple .env            # then fill in: secrets, database password,
                                # campaign identity
task build                      # (re)generates the embedded mayor list
docker compose up -d --build    # PostgreSQL + both images, on :8047
```

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
| `PARAPHE_ADMIN_EMAIL` / `_PASSWORD` / `_NAME` | coordination account, created or refreshed at every start — **mandatory at first launch** |
| `PARAPHE_SECRET_KEY` | session signing secret — `openssl rand -hex 64` |
| `PARAPHE_SOURCE_URL` | public repository URL: shows "source code" in the footer |
| `PARAPHE_DATABASE_URL` | PostgreSQL DSN — **mandatory**, the app refuses to start without it |
| `PARAPHE_HOST` / `PARAPHE_PORT` | listening interface and port |
| `PARAPHE_BASE_DOMAIN` | domain of the campaign subdomains — **empty = a single campaign** (see below) |
| `PARAPHE_ORG_SLUG` | subdomain of the campaign described by the variables above (default `campagne`) |
| `PARAPHE_INSTANCE_ADMIN_EMAIL` / `_PASSWORD` / `_NAME` | instance administration: approves hosting requests. Required in multi-campaign mode |
| `PARAPHE_VALKEY_URL` | shared rate-limit counters: `valkey://host:6379`, or `valkey+sentinel://h1:26379,h2:26379,h3:26379/paraphe`. Empty = counted in process memory, which is exact for ONE replica and says so at startup |
| `PARAPHE_VALKEY_PASSWORD` | Valkey password — never inside the URL, which travels in process lists and deployment files |
| `PARAPHE_TRUSTED_PROXIES` | CIDRs whose `X-Forwarded-For` is believed (your TLS proxy, the ingress). Empty = every request is attributed to its TCP peer — behind a proxy, everyone then shares one counter |

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
- A lead can only create volunteers, and only in their own team. Any attempt
  to raise the role or change team is ignored server-side.
- **Walls**: a team sees its own reservations and the free pool; cards
  reserved by another team are refused (403). Campaign counters (statuses,
  departments covered) stay visible to everyone, **with no names**.
- Deactivating an account takes effect at the next request, without waiting
  for a sign-out.

## Data and backup

- The mayor list is **in the API image** (frozen at build time); the team's
  work (accounts, assignments, statuses, notes) is in PostgreSQL.
  **That is the only irreplaceable data.** In Docker:
  `docker compose exec postgres pg_dump -U paraphe paraphe > backup-$(date +%F).sql`
  In Kubernetes, enable `postgres.cnpg.backup.*`: the chart then sets up WAL
  archiving **and** a daily `ScheduledBackup`. Both are needed — WAL archiving
  alone restores nothing.
- Updating the list (a new `task build`): `docker compose up -d --build`
  re-imports as an UPSERT. Data (email, rank, score) is refreshed, work
  columns (volunteer, status, notes) are untouched. A target removed from the
  list is deleted if nobody worked on it, and flagged "RETIRÉ" otherwise.

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
  --set secrets.secretKey="$(openssl rand -hex 32)"
```

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

**Rate limits** guard the doors: sign-in per source and per submitted
address, the public hosting form, the anonymous reads, the authenticated
writes and the CSV export each have a ceiling, generous enough that a
campaign office behind one NAT never meets them. The ceilings are constants,
not settings. With several replicas the counters must be shared or every
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
