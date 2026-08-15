# The interface image: static files and an nginx that proxies /api to the
# API image. Built from the repository root — the interface imports the core,
# the templates and GUIDE.md from there, a single source shared with the mass
# mailing.

FROM node:24-alpine AS build
RUN corepack enable
WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY web/package.json web/
COPY outils/package.json outils/
RUN pnpm install --frozen-lockfile
COPY GUIDE.md ./
COPY noyau/ noyau/
COPY modeles/ modeles/
COPY web/ web/
# the interface is served at the root here, unlike the Pages publication
ENV PARAPHE_BASE_PATH=/
RUN cd web && pnpm build

# The mode marker, stamped at BUILD time. The interface picks its mode at
# load: an API answers /api/config → team mode, nothing answers → browser
# mode, everything in IndexedDB. This marker says "there IS an API here", so
# a passing failure of /api/config cannot silently drop a volunteer into
# browser mode and have them write their team's work to local storage.
#
# The API used to inject it while serving index.html itself. It no longer
# serves anything, so the image that does carries it — and
# outils/deploiement.test.ts checks the two spellings still agree.
RUN sed -i 's|</head>|<meta name="paraphe-mode" content="team">\n</head>|' \
    web/dist/index.html \
 && grep -q 'paraphe-mode' web/dist/index.html

FROM nginxinc/nginx-unprivileged:1.29-alpine
# Only our own variables are substituted: the template is full of nginx's
# own ($host, $uri, $scheme), and envsubst would empty every one of them.
ENV NGINX_ENVSUBST_FILTER=^PARAPHE_
ENV PARAPHE_API_UPSTREAM=http://paraphe-api:8047
COPY web/nginx.conf.template /etc/nginx/templates/default.conf.template
# Runs before the template substitution, which nginx's entrypoint orders by
# file name: an upstream carrying a `;` would be read as more directives.
COPY --chmod=755 web/docker-entrypoint.d/05-check-upstream.sh \
    /docker-entrypoint.d/05-check-upstream.sh
COPY --from=build /src/web/dist /usr/share/nginx/html
EXPOSE 8080
