#!/bin/sh
# The upstream is substituted into the configuration verbatim, so a `;` in it
# closes proxy_pass and everything after is read as more nginx directives:
# `http://api:8047; return 302 https://elsewhere; #` makes this container
# serve a redirect to whoever set it. The value comes from an operator, not
# from a client, so this is not an open door — but a chart edited by mistake
# or inherited from elsewhere should not be able to turn the interface into
# something else in silence.
#
# Runs before 20-envsubst-on-templates.sh, which is what nginx's entrypoint
# orders by file name.
set -eu

upstream="${PARAPHE_API_UPSTREAM:-}"
if [ -z "$upstream" ]; then
    echo "PARAPHE_API_UPSTREAM is empty: the interface has no API to talk to" >&2
    exit 1
fi

case "$upstream" in
    http://*|https://*) ;;
    *)
        echo "PARAPHE_API_UPSTREAM must start with http:// or https:// (got: $upstream)" >&2
        exit 1
        ;;
esac

# Host, optional port, optional trailing slash — and nothing else. Anything
# outside that alphabet could carry a directive.
rest="${upstream#http://}"
rest="${rest#https://}"
if ! echo "$rest" | grep -Eq '^[A-Za-z0-9._-]+(:[0-9]{1,5})?/?$'; then
    echo "PARAPHE_API_UPSTREAM is not a plain host[:port] URL (got: $upstream)." >&2
    echo "A ';' or a space there would be read as an nginx directive." >&2
    exit 1
fi
