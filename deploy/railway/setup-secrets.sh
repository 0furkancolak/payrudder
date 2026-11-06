#!/usr/bin/env bash
# Collects what one environment of payrudder.com needs (plan 23, docs/runbooks/staging-and-production.md), one item at a time: what it is, where to
# get it (with the link) and how. GitHub repository secrets are set with `gh secret set` (the value goes through stdin, never argv); the bootstrap's
# inputs go into a file outside the repository (mode 600), which the printed bootstrap command reads. Items already set are skipped, so run it again
# to fill what is missing. To change a value: edit the file, or `gh secret set NAME`. Its messages are in Turkish (the owner's language).
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
  *) echo "kullanım: $0 [--check] staging|production" >&2; exit 2 ;;
esac
REPO=${REPO:-0furkancolak/payrudder}
CONSOLE=console$SUFFIX.payrudder.com
DIR=${PAYRUDDER_SECRETS_DIR:-$HOME/.config/payrudder}
FILE=$DIR/$ENV.env
FILE_VARS=(GEO_API_KEY NAFRU_BASE_URL NAFRU_API_KEY S3_ENDPOINT S3_ACCESS_KEY S3_SECRET_KEY OPERATOR_EMAILS ERROR_TRACKING_URL PAYRUDDER_GOOGLE_CLIENT_ID PAYRUDDER_GOOGLE_CLIENT_SECRET)

# Only the file's values count, not whatever the calling shell exports.
for v in "${FILE_VARS[@]}"; do unset "$v"; done
if [[ -f $FILE ]]; then set -a; . "$FILE"; set +a; fi
GH_SECRETS=$(gh secret list --repo "$REPO" | cut -f1)
missing=0

is_set() { [[ -n ${!1:-} ]]; }

save() {
  mkdir -p "$DIR"
  { echo "# $ENV: deploy/railway/bootstrap.sh girdileri, deploy/railway/setup-secrets.sh yazdı"
    for v in "${FILE_VARS[@]}"; do if is_set "$v"; then printf '%s=%q\n' "$v" "${!v}"; fi; done; } >"$FILE.tmp"
  mv "$FILE.tmp" "$FILE"
}

# show <mark> <name> <title> <how...>: the how lines only when something is asked
show() {
  local mark=$1 name=$2 title=$3; shift 3
  printf '\n%s %s: %s\n' "$mark" "$name" "$title"
  if [[ $CHECK == 0 && $mark != ✓ ]]; then local l; for l in "$@"; do printf '    %s\n' "$l"; done; fi
}

# ask <secret|plain|generate> <regex>: a value into REPLY, asked again until it matches; Enter skips (generate: a nafru-geo key, ngeo_payrudder_<env>_<48 hex>)
ask() {
  while :; do
    if [[ $1 == secret ]]; then read -rsp '  değer (gizli; Enter atlar): ' REPLY; echo
    elif [[ $1 == generate ]]; then read -rp '  değer (Enter yenisini üretir): ' REPLY; if [[ -z $REPLY ]]; then REPLY=ngeo_payrudder_${ENV}_$(openssl rand -hex 24); echo "  üretildi"; fi
    else read -rp '  değer (Enter atlar): ' REPLY; fi
    if [[ -z $REPLY || $REPLY =~ $2 ]]; then return; fi
    echo "  bu doğru görünmüyor (beklenen: $2), tekrar" >&2
  done
}

# github <NAME> <regex> <title> <how...>: a repository secret
github() {
  local name=$1 re=$2 title=$3; shift 3
  if grep -qx "$name" <<<"$GH_SECRETS"; then show ✓ "$name" "$title"; return; fi
  show ✗ "$name" "$title ($REPO deposunun GitHub secret'ı)" "$@"
  if [[ $CHECK == 0 ]]; then
    ask secret "$re"
    if [[ -n $REPLY ]]; then printf '%s' "$REPLY" | gh secret set "$name" --repo "$REPO" >/dev/null; echo "  $REPO üzerine kaydedildi"; return; fi
  fi
  missing=$((missing + 1))
}

# input <NAME> <required|optional> <secret|plain|generate> <regex> <title> <how...>: a bootstrap input, kept in $FILE
input() {
  local name=$1 need=$2 kind=$3 re=$4 title=$5; shift 5
  if is_set "$name"; then show ✓ "$name" "$title"; return; fi
  if [[ $need == required ]]; then show ✗ "$name" "$title" "$@"; else show - "$name" "$title (isteğe bağlı)" "$@"; fi
  if [[ $CHECK == 0 ]]; then
    ask "$kind" "$re"
    if [[ -n $REPLY ]]; then printf -v "$name" '%s' "$REPLY"; save; return; fi
  fi
  if [[ $need == required ]]; then missing=$((missing + 1)); fi
}

echo "== $ENV: $REPO GitHub secret'ları"
github CLOUDFLARE_ACCOUNT_ID '^[0-9a-f]{32}$' "console Worker'ının deploy edildiği Cloudflare hesabı" \
  "https://dash.cloudflare.com → Workers & Pages: sağda 'Account ID' (32 hex karakter). Ya da: npx wrangler whoami" \
  "payrudder.com zone'u bu hesapta olmalı (https://dash.cloudflare.com → Add a domain)."
github CLOUDFLARE_API_TOKEN '^[A-Za-z0-9_-]{30,}$' "deploy-console.yml'ın Worker'ı deploy ettiği token" \
  "https://dash.cloudflare.com/profile/api-tokens → Create Token → 'Edit Cloudflare Workers' şablonu." \
  "Account resources: bu hesap; Zone resources: payrudder.com (Worker route'ları için gerekli)."
# Railway project tokens look like rlwy_proj_... (older ones are a UUID).
if [[ $ENV == production ]]; then WHEN="bir release yayınlandığında deploy-production.yml"; else WHEN="main'e her push'ta deploy-staging.yml"; fi
github "RAILWAY_TOKEN_$(tr a-z A-Z <<<"$ENV")" '^[A-Za-z0-9_-]{20,}$' "$WHEN'ın servisleri deploy ettiği proje token'ı" \
  "https://railway.com/dashboard → payrudder projesi → Settings → Tokens → Create, ortam '$ENV'." \
  "Önce ortam olmalı: Settings → Environments → New, ya da \`railway environment new $ENV\`."

echo
echo "== $ENV: bootstrap girdileri, $FILE içinde"
input GEO_API_KEY required generate '^[A-Za-z0-9_-]{16,}$' "bu ortamın nafru-geo anahtarı (adres formları: ülke, il, ilçe)" \
  "Var olan anahtar: nafru-geo'nun .env.keys dosyasında PAYRUDDER_GEO_API_KEY (ngeo_payrudder_...); yapıştır." \
  "Ya da Enter yeni bir tane üretir; o zaman nafru-geo'nun API_KEYS listesine de eklenmeli (sonraki adımlar anlatıyor)."
input NAFRU_BASE_URL optional plain '^https://' "Nafru'nun API adresi, platformun bütün e-postaları için (kayıt doğrulama, faturalar; sandbox da)" \
  "Nafru panelinden (https://nafru.com); gönderen alan adı payrudder.com doğrulanmış olmalı: SPF/DKIM kayıtları Cloudflare DNS'e girer."
input NAFRU_API_KEY optional secret '.{16,}' "bu ortamın Nafru API anahtarı" "Nafru paneli: payrudder-$ENV adında yeni bir API anahtarı."
input S3_ENDPOINT required plain '^https://[0-9a-f]{32}\.r2\.cloudflarestorage\.com$' "Cloudflare R2'nin S3 adresi (faturalar ve dosyalar)" \
  "https://<Cloudflare hesap ID>.r2.cloudflarestorage.com; hesap ID: npx wrangler whoami. Bucket'lar: payrudder-invoices-$ENV, payrudder-assets-$ENV."
input S3_ACCESS_KEY required plain '^[0-9a-f]{32}$' "R2 API token'ının Access Key ID'si" \
  "https://dash.cloudflare.com → R2 Object Storage → Manage API tokens → Create Account API token: Object Read & Write," \
  "yalnız payrudder-invoices-$ENV ve payrudder-assets-$ENV bucket'larına. Access Key ID ve Secret Access Key bir kez gösterilir."
input S3_SECRET_KEY required secret '^[0-9a-f]{64}$' "R2 API token'ının Secret Access Key'i" "Aynı sayfada, Access Key ID'nin altında."
input OPERATOR_EMAILS required plain '^[^@ ,]+@[^@ ,]+(,[^@ ,]+@[^@ ,]+)*$' "platformu yönetenler (operatör konsolu), virgülle ayrılmış" \
  "Kendi e-posta adres(ler)in; doğruladıktan sonra operatör konsolunu görürler."
input ERROR_TRACKING_URL optional plain '^https://[^@]+@' "hataların raporlandığı DSN" \
  "https://sentry.io (ya da https://app.glitchtip.com) → payrudder-$ENV projesi → Settings → Client Keys (DSN): https://<anahtar>@host/<id>."
input PAYRUDDER_GOOGLE_CLIENT_ID optional plain '\.apps\.googleusercontent\.com$' "console'da 'Google ile devam et' (secret'ı sonra sorulur)" \
  "https://console.cloud.google.com/apis/credentials → Create credentials → OAuth client ID → Web application, adı payrudder-$ENV." \
  "Authorised redirect URI: https://$CONSOLE/gw/auth/google/callback"
if is_set PAYRUDDER_GOOGLE_CLIENT_ID; then
  input PAYRUDDER_GOOGLE_CLIENT_SECRET required secret '.{10,}' "o OAuth client'ın secret'ı" "Client ID'nin yanında görünür (Download JSON'da da var)."
fi

if [[ $missing -gt 0 ]]; then echo; echo "$missing zorunlu kalem eksik: betiği tekrar çalıştır"; exit 1; fi

cat <<EOF

== $ENV için sonraki adımlar
1. nafru-geo: GEO_API_KEY nafru-geo'nun API_KEYS listesinde değilse ($FILE içinde) nafrucom/nafru-geo'nun .env.keys listesine ekle, sonra orada:
     printf '%s' "<tüm anahtarlar, virgülle>" | bunx wrangler secret put API_KEYS
2. Railway: payrudder projesinde '$ENV' ortamı var (https://railway.com/dashboard → payrudder → Settings → Environments), \`railway login\`, \`railway link\`.
3. Backend (önce --dry-run çıktısını oku; production: release'in tag'inden alınmış bir checkout'tan):
     (set -a; . $FILE; set +a; deploy/railway/bootstrap.sh --dry-run --environment $ENV --console-url https://$CONSOLE \\
       --api-domain api$SUFFIX.payrudder.com --allowed-environments sandbox)
4. Railway'in yazdırdığı DNS kayıtları → Cloudflare, DNS only (gri bulut). assets$SUFFIX.payrudder.com R2 bucket'ına bağlı, kayıt gerekmez.
5. Console'un proxy secret'ı, Worker'a aynı değer:
     railway variable list --environment $ENV --service gateway --kv | grep PAYRUDDER_TRUSTED_PROXY_SECRET
     (cd web/admin && npx wrangler secret put EDGE_AUTH_SECRET --env $ENV)
EOF
