#!/usr/bin/env bash
# Runs TestSandbox of one payment adapter against the provider's sandbox when its repository secrets exist (.github/workflows/nightly-providers.yml).
# Without them it says "skipped: no secrets" and succeeds: a repository (or a fork) without sandbox accounts must not get a red nightly. With them, a test that
# fails is a failure, and so is a test that skipped itself anyway (a secret that is set but not the one it needs would otherwise pass for nothing).
#
#   scripts/provider-sandbox.sh stripe|paddle|iyzico
set -euo pipefail

provider=${1:-}
case $provider in
  stripe) need=(STRIPE_SANDBOX_API_KEY) ;;
  paddle) need=(PADDLE_SANDBOX_API_KEY) ;;
  iyzico) need=(IYZICO_SANDBOX_API_KEY IYZICO_SANDBOX_SECRET_KEY IYZICO_SANDBOX_CARD) ;;
  *) echo "usage: $0 stripe|paddle|iyzico" >&2; exit 2 ;;
esac

report() { echo "$*"; if [[ -n ${GITHUB_STEP_SUMMARY:-} ]]; then echo "- $provider: $*" >>"$GITHUB_STEP_SUMMARY"; fi; }

for v in "${need[@]}"; do
  if [[ -z ${!v:-} ]]; then report "skipped: no secrets ($v is not set)"; exit 0; fi
done

cd "$(dirname "$0")/../pkg"
out=$(mktemp)
trap 'rm -f "$out"' EXIT
if ! CGO_ENABLED=0 go test -count=1 -v -run '^TestSandbox$' "./payments/$provider" 2>&1 | tee "$out"; then
  report "FAILED against the sandbox"; exit 1
fi
if ! grep -q -- '--- PASS: TestSandbox' "$out"; then
  report "FAILED: the secrets are set but TestSandbox skipped itself (see the output above)"; exit 1
fi
report "passed against the sandbox"
