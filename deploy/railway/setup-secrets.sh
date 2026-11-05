#!/usr/bin/env bash
# Collects what one environment of payrudder.com needs (plan 23, docs/runbooks/staging-and-production.md), one item at a time: what it is, where to
# get it (with the link) and how. GitHub repository secrets are set with `gh secret set` (the value goes through stdin, never argv); the bootstrap's
# inputs go into a file outside the repository (mode 600), which the printed bootstrap command reads. Items already set are skipped, so run it again
# to fill what is missing. To change a value: edit the file, or `gh secret set NAME`.
#
#   deploy/railway/setup-secrets.sh [--check] staging|production
#
#   --check                prompts for nothing: lists every item as set (✓), missing (✗) or optional and missing (-); exits 1 when a required one is missing
#   PAYRUDDER_SECRETS_DIR  where the file goes, default ~/.config/payrudder (<environment>.env)
set -euo pipefail
umask 077

CHECK=0
if [[ ${1:-} == --check ]]; then CHECK=1; shift; fi
ENV=${1:-}
case $ENV in
  staging) SUFFIX=-staging ;;
  production) SUFFIX= ;;
  *) echo "usage: $0 [--check] staging|production" >&2; exit 2 ;;
esac
REPO=${REPO:-0furkancolak/payrudder}
CONSOLE=console$SUFFIX.payrudder.com
DIR=${PAYRUDDER_SECRETS_DIR:-$HOME/.config/payrudder}
FILE=$DIR/$ENV.env
FILE_VARS=(GEO_API_KEY NAFRU_BASE_URL NAFRU_API_KEY OPERATOR_EMAILS ERROR_TRACKING_URL PAYRUDDER_GOOGLE_CLIENT_ID PAYRUDDER_GOOGLE_CLIENT_SECRET)

# Only the file's values count, not whatever the calling shell exports.
for v in "${FILE_VARS[@]}"; do unset "$v"; done
if [[ -f $FILE ]]; then set -a; . "$FILE"; set +a; fi
GH_SECRETS=$(gh secret list --repo "$REPO" | cut -f1)
missing=0

is_set() { [[ -n ${!1:-} ]]; }

save() {
  mkdir -p "$DIR"
  { echo "# $ENV: the inputs of deploy/railway/bootstrap.sh, written by deploy/railway/setup-secrets.sh"
    for v in "${FILE_VARS[@]}"; do if is_set "$v"; then printf '%s=%q\n' "$v" "${!v}"; fi; done; } >"$FILE.tmp"
  mv "$FILE.tmp" "$FILE"
}

# show <mark> <name> <title> <how...>: the how lines only when something is asked
show() {
  local mark=$1 name=$2 title=$3; shift 3
  printf '\n%s %s: %s\n' "$mark" "$name" "$title"
  if [[ $CHECK == 0 && $mark != ✓ ]]; then local l; for l in "$@"; do printf '    %s\n' "$l"; done; fi
}

# ask <secret|plain|generate> <regex>: a value into REPLY, asked again until it matches; Enter skips (generate: makes a 32-byte hex key)
ask() {
  while :; do
    if [[ $1 == secret ]]; then read -rsp '  value (hidden; Enter skips): ' REPLY; echo
    elif [[ $1 == generate ]]; then read -rp '  value (Enter generates one): ' REPLY; if [[ -z $REPLY ]]; then REPLY=$(openssl rand -hex 32); echo "  generated"; fi
    else read -rp '  value (Enter skips): ' REPLY; fi
    if [[ -z $REPLY || $REPLY =~ $2 ]]; then return; fi
    echo "  that does not look right (expected $2), again" >&2
  done
}

# github <NAME> <regex> <title> <how...>: a repository secret
github() {
  local name=$1 re=$2 title=$3; shift 3
  if grep -qx "$name" <<<"$GH_SECRETS"; then show ✓ "$name" "$title"; return; fi
  show ✗ "$name" "$title (GitHub secret of $REPO)" "$@"
  if [[ $CHECK == 0 ]]; then
    ask secret "$re"
    if [[ -n $REPLY ]]; then printf '%s' "$REPLY" | gh secret set "$name" --repo "$REPO" >/dev/null; echo "  set on $REPO"; return; fi
  fi
  missing=$((missing + 1))
}

# input <NAME> <required|optional> <secret|plain|generate> <regex> <title> <how...>: a bootstrap input, kept in $FILE
input() {
  local name=$1 need=$2 kind=$3 re=$4 title=$5; shift 5
  if is_set "$name"; then show ✓ "$name" "$title"; return; fi
  if [[ $need == required ]]; then show ✗ "$name" "$title" "$@"; else show - "$name" "$title (optional)" "$@"; fi
  if [[ $CHECK == 0 ]]; then
    ask "$kind" "$re"
    if [[ -n $REPLY ]]; then printf -v "$name" '%s' "$REPLY"; save; return; fi
  fi
  if [[ $need == required ]]; then missing=$((missing + 1)); fi
}

echo "== $ENV: GitHub secrets of $REPO"
github CLOUDFLARE_ACCOUNT_ID '^[0-9a-f]{32}$' "the Cloudflare account the console Worker is deployed to" \
  "https://dash.cloudflare.com → Workers & Pages: 'Account ID' on the right (32 hex characters)." \
  "The zone payrudder.com must be in this account (https://dash.cloudflare.com → Add a domain)."
github CLOUDFLARE_API_TOKEN '^[A-Za-z0-9_-]{30,}$' "the token deploy-console.yml deploys the Worker with" \
  "https://dash.cloudflare.com/profile/api-tokens → Create Token → template 'Edit Cloudflare Workers'." \
  "Account resources: this account; Zone resources: payrudder.com (the Worker routes need it)."
if [[ $ENV == production ]]; then
  github RAILWAY_TOKEN_PRODUCTION '^[A-Za-z0-9-]{20,}$' "the project token deploy-production.yml deploys a release with" \
    "https://railway.com/dashboard → the project → Settings → Tokens → Create, environment 'production'." \
    "The environment must exist first: Settings → Environments → New, or \`railway environment new production\`."
fi

echo
echo "== $ENV: bootstrap inputs, in $FILE"
input GEO_API_KEY required generate '^[0-9a-f]{64}$' "this environment's nafru-geo key (address forms: countries, regions, districts)" \
  "A new random key; Enter makes one. It also has to be added to nafru-geo (the next steps say how)."
input NAFRU_BASE_URL required plain '^https://' "Nafru's API address, for the platform's own mail (sign-up verification, invoices)" \
  "From Nafru's panel (https://nafru.com), with payrudder.com verified as the sender domain: its SPF/DKIM records go into Cloudflare DNS."
input NAFRU_API_KEY required secret '.{16,}' "the Nafru API key of this environment" \
  "Nafru's panel: a new API key named payrudder-$ENV."
input OPERATOR_EMAILS required plain '^[^@ ,]+@[^@ ,]+(,[^@ ,]+@[^@ ,]+)*$' "who runs the platform (operator console), comma separated" \
  "Your own address(es); they see the operator console after verifying them."
input ERROR_TRACKING_URL optional plain '^https://[^@]+@' "the DSN errors are reported to" \
  "https://sentry.io (or https://app.glitchtip.com) → a project payrudder-$ENV → Settings → Client Keys (DSN): https://<key>@host/<id>."
input PAYRUDDER_GOOGLE_CLIENT_ID optional plain '\.apps\.googleusercontent\.com$' "'Continue with Google' on the console (the secret comes next)" \
  "https://console.cloud.google.com/apis/credentials → Create credentials → OAuth client ID → Web application, name payrudder-$ENV." \
  "Authorised redirect URI: https://$CONSOLE/gw/auth/google/callback"
if is_set PAYRUDDER_GOOGLE_CLIENT_ID; then
  input PAYRUDDER_GOOGLE_CLIENT_SECRET required secret '.{10,}' "the secret of that OAuth client" "Shown next to the client ID (Download JSON has it too)."
fi

if [[ $missing -gt 0 ]]; then echo; echo "$missing required item(s) missing: run it again"; exit 1; fi

if [[ $ENV == staging ]]; then FLAGS="--repo $REPO --profile small"; else FLAGS="--source local --profile full"; fi
cat <<EOF

== next steps for $ENV
1. nafru-geo: add GEO_API_KEY (in $FILE) to the key list in nafrucom/nafru-geo's .env.keys, then there:
     printf '%s' "<all keys, comma separated>" | bunx wrangler secret put API_KEYS
2. Railway: the environment '$ENV' exists (https://railway.com/dashboard → the project → Settings → Environments), \`railway login\`, \`railway link\`.
3. The backend (read the --dry-run first; production: from a checkout of the release's tag):
     (set -a; . $FILE; set +a; deploy/railway/bootstrap.sh --dry-run --environment $ENV $FLAGS \\
       --console-url https://$CONSOLE --api-domain api$SUFFIX.payrudder.com --files-domain files$SUFFIX.payrudder.com --allowed-environments sandbox)
4. The DNS records Railway prints → Cloudflare, DNS only (grey cloud).
5. The console's proxy secret, the same value on the Worker:
     railway variable list --environment $ENV --service gateway --kv | grep PAYRUDDER_TRUSTED_PROXY_SECRET
     (cd web/admin && npx wrangler secret put EDGE_AUTH_SECRET --env $ENV)
EOF
