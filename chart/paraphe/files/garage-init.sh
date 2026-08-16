#!/bin/sh
# Bootstraps the Garage cluster that holds the campaign logos: introduce the
# nodes to each other, assign a layout, create the bucket, import the
# application's key, publish the bucket on the web endpoint.
#
# ONE file, run by the compose stack (one node) and by the chart's Job
# (three). The chart reads it from here into a ConfigMap and the compose
# mounts this same path — a second copy would be a second thing to keep in
# step, and the differences between the two shapes are three variables.
#
# Why a script rather than declarative YAML: a Garage cluster's LAYOUT is
# imperative. There is no configuration form of "this node stores data" —
# the nodes must be listed, given a zone and a capacity, and the resulting
# layout version applied. Nothing else here is imperative; this one thing is.
#
# Why the admin API rather than the `garage` CLI: the official image is
# FROM scratch and carries no shell, so no container built on it can run a
# multi-step bootstrap. The admin API is the supported machine interface.
#
# Idempotent, and re-run at every start: an already-assigned node is left
# alone, an existing bucket is not recreated, an imported key is imported
# again to the same value.
set -eu

: "${GARAGE_ADMIN_TOKEN:?the admin token, GARAGE_ADMIN_TOKEN of the nodes}"
: "${MEDIA_BUCKET:?the bucket holding the logos}"
: "${MEDIA_ACCESS_KEY:?the S3 access key the application uses}"
: "${MEDIA_SECRET_KEY:?the S3 secret key the application uses}"
# Capacity per node. Logos weigh 64 KiB at most, so this is not a sizing
# decision — it is what Garage requires to place data at all.
: "${GARAGE_CAPACITY:=10000000000}"
: "${GARAGE_ZONE:=paraphe}"
# Digits only, checked here. A YAML file that writes this unquoted is read
# as a FLOAT and rendered "1e+10"; jq passes that through as scientific
# notation, and Garage refuses the whole layout with a message about an
# untagged enum that names neither the field nor the value. The cluster
# then never lays itself out, and nothing says why.
case $GARAGE_CAPACITY in
  '' | *[!0-9]*)
    echo "GARAGE_CAPACITY = '$GARAGE_CAPACITY': expected a number of BYTES," \
      "digits only. Quote it in values.yaml — unquoted, YAML reads a float" \
      "and Garage is handed 1e+10." >&2
    exit 1
    ;;
esac
# GARAGE_PEERS: the admin URL of EACH node, space separated. One entry is
# the compose stack; three are the chart — a one-item peer list is still a
# peer list, so both shapes set the same variable and this file carries no
# per-shape fallback. A single address behind a round-robin Service will
# not do: the nodes have to be addressed individually to be introduced.
: "${GARAGE_PEERS:?the admin URL of each node, space separated}"
# Everything after the introductions is asked of the first node: they share
# one view of the cluster from that point on.
first=$(echo "$GARAGE_PEERS" | cut -d' ' -f1)
expected=$(echo "$GARAGE_PEERS" | wc -w)

# Prints the response body, and on a refusal prints the STATUS AND THE BODY
# before giving up: `curl -f` alone swallows the explanation, and a bare
# "error: 400" from a bootstrap tells whoever is deploying nothing at all.
api() {
  method=$1
  path=$2
  shift 2
  status=$(curl -sS -o /tmp/api-body -w '%{http_code}' -X "$method" \
    -H "Authorization: Bearer $GARAGE_ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    "$first$path" "$@")
  if [ "$status" -lt 200 ] || [ "$status" -ge 300 ]; then
    echo "$method $path answered $status: $(cat /tmp/api-body)" >&2
    return 1
  fi
  cat /tmp/api-body
}

peer() {
  curl -fsS -H "Authorization: Bearer $GARAGE_ADMIN_TOKEN" "$1$2"
}

# --- the introductions -----------------------------------------------------
# Each node knows only itself until told otherwise. Garage's own discovery
# would do this through a CustomResourceDefinition and cluster-scoped
# rights; asking the nodes directly needs neither, and this Job already
# speaks the admin API.
echo "waiting for $expected node(s) to answer…"
attempt=0
while : ; do
  answering=""
  for url in $GARAGE_PEERS; do
    id=$(peer "$url" /v2/GetClusterStatus 2>/dev/null \
      | jq -r '[.nodes[] | select(.addr != null) | "\(.id)@\(.addr)"] | .[]' \
      2>/dev/null || true)
    [ -n "$id" ] && answering="$answering $id"
  done
  # deduplicated: once connected, every node reports every other one
  answering=$(echo "$answering" | tr ' ' '\n' | grep . | sort -u | tr '\n' ' ')
  [ "$(echo "$answering" | wc -w)" -ge "$expected" ] && break
  attempt=$((attempt + 1))
  if [ "$attempt" -gt 60 ]; then
    echo "only $(echo "$answering" | wc -w) of $expected nodes answered after" \
      "two minutes. The cluster must NOT be laid out on a subset: the" \
      "replication factor would never reach its quorum, and every write" \
      "would be refused." >&2
    exit 1
  fi
  sleep 2
done

if [ "$expected" -gt 1 ]; then
  echo "introducing the nodes to each other"
  # shellcheck disable=SC2086 # a word list is exactly what jq wants here
  api POST /v2/ConnectClusterNodes \
    -d "$(printf '%s\n' $answering | jq -Rsc 'split("\n") | map(select(. != ""))')" \
    >/dev/null
  # and CONNECTED, not merely known: a layout assigned to nodes that cannot
  # reach one another is a cluster that answers nothing.
  attempt=0
  until [ "$(api GET /v2/GetClusterStatus | jq '[.nodes[] | select(.isUp)] | length')" \
    -ge "$expected" ]; do
    attempt=$((attempt + 1))
    if [ "$attempt" -gt 30 ]; then
      echo "the nodes were introduced but are not all up after a minute" >&2
      api GET /v2/GetClusterStatus | jq -c '.nodes[] | {id, addr, isUp}' >&2
      exit 1
    fi
    sleep 2
  done
fi

# --- the layout ------------------------------------------------------------
# A node needs a role when it has NONE — and also when the one it has
# disagrees with what this deployment now asks for. Only the first case was
# handled, so raising `capacityBytes` or moving `zone` in values.yaml
# changed the Job's environment, ran green, and left the cluster exactly as
# it was: `helm upgrade` returned 0, every probe stayed 200, and nothing
# anywhere said the new value had been ignored.
status=$(api GET /v2/GetClusterStatus)
unassigned=$(echo "$status" | jq -r --arg zone "$GARAGE_ZONE" \
  --argjson capacity "$GARAGE_CAPACITY" \
  '[.nodes[]
    | select(.isUp)
    | select(.role == null
             or .role.zone != $zone
             or .role.capacity != $capacity)
    | .id] | join(" ")')

if [ -n "$unassigned" ]; then
  # Anything already staged by hand is about to be replaced by what this
  # deployment says. Said out loud: an operator mid-way through a manual
  # `garage layout assign` would otherwise see their work vanish into an
  # upgrade that reported nothing.
  staged=$(echo "$status" | jq -r '.layout.stagedRoleChanges? // [] | length')
  if [ "${staged:-0}" -gt 0 ]; then
    echo "warning: $staged staged role change(s) were pending and are being" \
      "replaced by this deployment's zone and capacity" >&2
  fi
  echo "assigning a layout to: $unassigned"
  roles=$(for id in $unassigned; do
    jq -nc --arg id "$id" --arg zone "$GARAGE_ZONE" \
      --argjson capacity "$GARAGE_CAPACITY" \
      '{id: $id, zone: $zone, capacity: $capacity, tags: []}'
  done | jq -sc '{roles: .}')
  api POST /v2/UpdateClusterLayout -d "$roles" >/dev/null
  # The version to apply is the staged one, read back rather than guessed:
  # a cluster that has been laid out before is not at version 1.
  next=$(api GET /v2/GetClusterLayout | jq '.version + 1')
  api POST /v2/ApplyClusterLayout -d "{\"version\": $next}" >/dev/null
  echo "layout applied, version $next"
fi

# Applied is not yet OPERATIONAL: the nodes have to take up their share of
# the partitions before anything can be written. Creating the bucket in that
# window fails, and the Job only succeeded on its second attempt — a fresh
# install that works the third time is a fresh install nobody trusts.
echo "waiting for the cluster to become operational…"
attempt=0
until [ "$(curl -s -o /dev/null -w '%{http_code}' "$first/health")" = "200" ]; do
  attempt=$((attempt + 1))
  if [ "$attempt" -gt 60 ]; then
    echo "the layout is applied but the cluster is still not operational" \
      "after two minutes:" >&2
    api GET /v2/GetClusterHealth >&2
    exit 1
  fi
  sleep 2
done

# --- the bucket ------------------------------------------------------------
# An existing bucket answers 409, which is the normal case on every restart
# after the first — and the ONLY refusal tolerated here. Swallowing the rest
# is how a rejected bucket name surfaced as "neither created nor found",
# three steps later, with the reason discarded.
if ! bucket=$(api POST /v2/CreateBucket \
  -d "$(jq -nc --arg a "$MEDIA_BUCKET" '{globalAlias: $a}')" 2>/tmp/create-err); then
  grep -q "BucketAlreadyExists\|already exists" /tmp/create-err || {
    cat /tmp/create-err >&2
    exit 1
  }
fi
bucket=$(api GET "/v2/GetBucketInfo?globalAlias=$MEDIA_BUCKET" | jq -r '.id')
if [ -z "$bucket" ] || [ "$bucket" = "null" ]; then
  echo "bucket $MEDIA_BUCKET was neither created nor found" >&2
  exit 1
fi
echo "bucket $MEDIA_BUCKET is $bucket"

# --- the application's key -------------------------------------------------
# IMPORTED, not created: a created key is random, and the application would
# have to be told afterwards what it turned out to be. Imported, the same
# configuration file drives both sides.
if ! api POST /v2/ImportKey -d "$(jq -nc \
  --arg id "$MEDIA_ACCESS_KEY" --arg secret "$MEDIA_SECRET_KEY" \
  '{accessKeyId: $id, secretAccessKey: $secret, name: "paraphe"}')" \
  2>/tmp/key-err >/dev/null; then
  grep -q 'KeyAlreadyExists' /tmp/key-err || { cat /tmp/key-err >&2; exit 1; }
  # The ID exists. Garage CANNOT change the secret of an existing key — and
  # it refuses to let a deleted ID be recreated, so there is no way round it
  # either. Left as `|| true`, rotating `mediaSecretKey` alone was a perfect
  # silence: the Job went green, `helm upgrade` returned 0, every pod stayed
  # ready, and the application answered 403 on every single upload.
  current=$(api GET "/v2/GetKeyInfo?id=$MEDIA_ACCESS_KEY&showSecretKey=true" \
    | jq -r '.secretAccessKey // ""')
  if [ "$current" != "$MEDIA_SECRET_KEY" ]; then
    echo "the key $MEDIA_ACCESS_KEY already exists in the store with a" \
      "DIFFERENT secret, and Garage cannot change the secret of an existing" \
      "key (nor reuse an ID once deleted). Rotating mediaSecretKey alone" \
      "does nothing: the application would be handed a secret the store" \
      "refuses. Rotate mediaAccessKey as well." >&2
    exit 1
  fi
fi
api POST /v2/AllowBucketKey -d "$(jq -nc \
  --arg b "$bucket" --arg k "$MEDIA_ACCESS_KEY" \
  '{bucketId: $b, accessKeyId: $k,
    permissions: {read: true, write: true, owner: false}}')" >/dev/null
echo "key $MEDIA_ACCESS_KEY may read and write $MEDIA_BUCKET"

# Every FORMER GENERATION OF THIS KEY loses its access here. Rotating the ID
# is what an operator does after a leak, and granting the new one revokes
# nothing by itself — the leaked credential kept full read and write for
# ever. Denied rather than deleted: a revoked key answers 403, and Garage
# never lets an ID come back, so deleting it spends something unrecoverable.
#
# Filtered on the NAME this script imports under, not on "everything that is
# not the current key". The broader form revoked whatever else the operator
# had deliberately granted — a backup pipeline, a mirror, a migration tool —
# on every `helm upgrade`, silently, and Garage would not let those IDs
# return either. Keys nobody named "paraphe" are none of this script's
# business.
# Read FIRST, filter second. `set -e` does not cross a pipe, so
# `api GET … | jq` exited 0 whenever jq did — a 5xx or a restarted peer
# skipped the revocation in silence, which after a leak is the one moment
# it must not. As its own command, a failure stops the script. (`pipefail`
# would say the same thing and is not POSIX; this file is #!/bin/sh.)
bucket_info=$(api GET "/v2/GetBucketInfo?globalAlias=$MEDIA_BUCKET")
stale_keys=$(echo "$bucket_info" | jq -r --arg keep "$MEDIA_ACCESS_KEY" \
  '.keys[]? | select(.name == "paraphe" and .accessKeyId != $keep)
   | select(.permissions.read or .permissions.write or .permissions.owner)
   | .accessKeyId')
for stale in $stale_keys; do
  api POST /v2/DenyBucketKey -d "$(jq -nc --arg b "$bucket" --arg k "$stale" \
    '{bucketId: $b, accessKeyId: $k,
      permissions: {read: true, write: true, owner: true}}')" >/dev/null
  echo "revoked a former paraphe key on $MEDIA_BUCKET: $stale"
done

# --- publishing ------------------------------------------------------------
# The whole point of this arbitration: the browser fetches the logos from
# the store's own origin, so the bucket has to be readable without a
# signature. Only logos live here, and each of them is already shown on a
# public sign-in page.
api POST "/v2/UpdateBucket?id=$bucket" -d '{"websiteAccess": {"enabled": true,
  "indexDocument": "index.html", "errorDocument": "index.html"}}' >/dev/null
echo "bucket $MEDIA_BUCKET is served on the web endpoint"
