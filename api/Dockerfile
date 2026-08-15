# The application image: one binary, the built interface, the campaign
# templates and the mayor list.
#
# ONE image and one process. It serves the pages AND the JSON, because a
# second image meant the interface a volunteer loads came from one artefact
# and the API it talks to from another — held together by the discipline of
# tagging them alike — and it meant the page-serving path every test
# exercises was not the one production ran.
#
# The application is STATELESS: accounts, statuses and notes live in
# PostgreSQL (PARAPHE_DATABASE_URL, given at deployment). No volume, and
# nothing is written to disk — the final stage has no writable filesystem to
# write to.
#
# Built from the repository root: the interface imports the core, the
# templates and GUIDE.md, all of which live outside web/.

# ── the interface ────────────────────────────────────────────────────────
FROM node:24-alpine AS web
RUN corepack enable
WORKDIR /src
# Manifests first, and nothing else: this layer is what installing depends
# on, and it must not be invalidated by a source file changing.
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY web/package.json web/
COPY outils/package.json outils/
RUN --mount=type=cache,id=pnpm,target=/pnpm/store \
    pnpm config set store-dir /pnpm/store && pnpm install --frozen-lockfile
COPY GUIDE.md ./
COPY noyau/ noyau/
COPY modeles/ modeles/
COPY web/ web/
# the interface is served at the root here, unlike the Pages publication
ENV PARAPHE_BASE_PATH=/
WORKDIR /src/web
RUN pnpm build

# The account-less browser version, second build of the same sources. Under
# /navigateur/, its /api calls hit paths the server answers with HTML — the
# exact condition that switches it to browser mode, same as on a static
# host. The instance domain bakes the ?org= pre-fill; empty leaves it inert.
ARG PARAPHE_BASE_DOMAIN=""
RUN PARAPHE_BASE_PATH=/navigateur/ PARAPHE_BASE_DOMAIN="$PARAPHE_BASE_DOMAIN" \
    pnpm build --outDir dist-navigateur

# Precompressed variants, built ONCE here rather than per request: brotli at
# quality 11 takes the bundle from 357 kB to 90, and is far too slow to run
# on the fly. The server picks a variant from Accept-Encoding and falls back
# to the original.
# pipefail: without it, a failing `find` still leaves the pipeline exiting on
# xargs' status, and the build ships uncompressed assets while looking green.
SHELL ["/bin/ash", "-o", "pipefail", "-c"]
RUN apk add --no-cache brotli=~1.2 \
 && find dist dist-navigateur -type f \( -name '*.js' -o -name '*.css' -o -name '*.html' \
      -o -name '*.svg' -o -name '*.json' -o -name '*.txt' \) \
      -size +1k -print0 \
    | tee /tmp/compressible | xargs -0 -r -n1 brotli -q 11 -k \
 && xargs -0 -r -n1 gzip -9 -k < /tmp/compressible \
 # EVERY file that was listed, not the first one an alphabetical `head`
 # happens to return. A future chunk under 1 kB sorting first would have
 # failed this build with a message pointing at compression, and a chunk
 # that failed to compress while another succeeded would have passed it.
 #
 # `xargs` and not `while read -d ''`: busybox ash does support the latter,
 # but nothing in this file should depend on which shell alpine ships, and
 # a non-zero exit from any invocation here fails the build.
 && xargs -0 -r -n1 sh -c \
      'test -f "$1.br" && test -f "$1.gz" || { echo "no compressed variant for $1" >&2; exit 1; }' _ \
      < /tmp/compressible \
 && echo "compressed $(tr -cd '\0' < /tmp/compressible | wc -c) files"

# ── the binary ───────────────────────────────────────────────────────────
# Independent of the stage above: buildkit runs them in parallel, and a
# change to a .tsx does not invalidate this one.
FROM golang:1.26-alpine AS binary
WORKDIR /src
# vendor/ is committed, so there is no module download and no network: the
# build is reproducible from the tree alone.
COPY api/go.mod api/go.sum ./
COPY api/vendor/ vendor/
COPY api/ ./
# static, and stripped: the final image has no libc to offer and no debugger
# to run
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w" \
    -o /paraphe-api .

# ── the image ────────────────────────────────────────────────────────────
# scratch, not alpine: 8 MB of base for a static binary that needs none of
# it. What IS needed is copied explicitly, which also means an attacker who
# reaches this container finds no shell, no package manager and no coreutils.
FROM scratch

# PostgreSQL over sslmode=verify-full needs a trust store.
COPY --from=binary /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# uid 10001, matching the chart's podSecurityContext. distroless:nonroot
# would bring its own 65532 and force the chart to follow; this is one file.
# Go needs no passwd entry to run, but pgx resolves ~/.pgpass through
# os.UserHomeDir, and a HOME nobody set resolves to a lookup that fails.
COPY api/passwd /etc/passwd

WORKDIR /srv/paraphe
COPY --from=binary /paraphe-api /usr/local/bin/paraphe-api
COPY config/ config/
COPY modeles/ modeles/
COPY --from=web /src/web/dist web/dist
# the self-hosted browser version, served under /navigateur/ (no marker)
COPY --from=web /src/web/dist-navigateur web/dist-navigateur

# LAST, and alone in its layer: 9 MB of CSV that changes on its own schedule.
# Above the binary, every code change would re-push it.
# Generated by `task build` — the image embeds the mayor list of the moment.
# 04 is what the application serves; 01 feeds the mass mailing.
COPY out/04_base_complete.csv out/04_base_complete.csv
COPY out/01_maires_cibles_prioritaires.csv out/01_maires_cibles_prioritaires.csv

ENV PARAPHE_HOST=0.0.0.0 \
    PARAPHE_PORT=8047 \
    PARAPHE_BROWSER_WEB_DIR=web/dist-navigateur
EXPOSE 8047

USER 10001
ENTRYPOINT ["paraphe-api"]
