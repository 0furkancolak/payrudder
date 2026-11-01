#!/usr/bin/env bash
# Validates the UBL that pkg/einvoice/ubl builds against the official rules: the same command runs in CI (.github/workflows/einvoice-schematron.yml) and locally.
#
#   scripts/einvoice-validate.sh            # builds the sample documents, validates Peppol and UBL-TR ones
#   scripts/einvoice-validate.sh peppol     # only the Peppol BIS Billing 3.0 / EN 16931 documents
#   scripts/einvoice-validate.sh tr         # only the UBL-TR (e-fatura, e-arsiv) documents
#   scripts/einvoice-validate.sh peppol DIR # validate every peppol-*.xml in DIR instead of building the samples
#
# What it checks (docs/einvoice-integrator-contract.md, docs/plans/09-einvoice-integrators.md):
#   peppol-*.xml    UBL 2.1 XSD (OASIS) + EN 16931 schematron (CEN, EUPL-1.2) + Peppol BIS Billing 3.0 schematron (OpenPEPPOL)
#   ubl-tr-*.xml    UBL-TR 1.2.1 XSD + UBL-TR schematron, both from GIB's public e-Fatura / UBL-TR packages (ebelge.gib.gov.tr)
# A document passes when the XSD accepts it and no SVRL failed-assert / successful-report carries flag "fatal" or "error". Warnings are printed, not failed.
# Nothing official is committed to the repository: every artefact is downloaded once into the cache, pinned by version and sha256, and refused on a mismatch.
#
# Needs: java (11+), curl, unzip, xmllint (libxml2-utils) and go. Cache: $EINVOICE_VALIDATE_CACHE (default ~/.cache/payrudder/einvoice-validate); JAVA names the java binary.
# EINVOICE_TR_OPTIONAL=1 turns a failed download of the GIB packages into a warning (CI sets it; whether GitHub's runners can reach ebelge.gib.gov.tr is UNVERIFIED).
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cache="${EINVOICE_VALIDATE_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/payrudder/einvoice-validate}"
java="${JAVA:-java}"
set_name="${1:-all}"
docs_dir="${2:-}"

# --- pinned artefacts (versions, URLs and sha256 are recorded in docs/plans/09-einvoice-integrators.md) ---
saxon_ver=12.9;        saxon_sha=8f3a9216a537367132293eacbba9df062eace8f8b16a184af59e2e4839d4cd41
resolver_ver=5.3.3;    resolver_sha=1fe4d5b92f708dcdb82dbce12919e0171e6b5ca62c6dca6220483625098feb5f
schxslt_ver=1.10.1;    schxslt_sha=4f4f21edab7b37f96ad59ae12a344d3510f1092ac46b6d81a4efa0120b73cb58
cen_ver=1.3.17;        cen_sha=bf02a0e3f6cbe692a4a28e70be7bdcaded5612b4169b543e13d6cb4f33c29e92
peppol_tag=3.0.21;     peppol_sha=9616cfffb418f349b532c1e25d3c831b8af6a47f4f5607ca0df4b1336f48e5f3
oasis_sha=60b80d76394a8a2add90723ecb8e0e2e9d826775de9749df37a72d60703f86ed
gib_efatura_sha=12901476cd424fd65c779a4fb44cee798f9921ce55b003f8a076c936ddf8b583
gib_ubltr_sha=cb583941b8a8a239c59902c6bc455c0f75d48f2bb81d7d3fbe1ae827f981f7db
maven=https://repo1.maven.org/maven2

sum() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }

# fetch NAME URL SHA256: into $cache/dl/NAME, verified; a cached file with the right sum is not downloaded again.
fetch() {
  local f="$cache/dl/$1" got
  mkdir -p "$cache/dl"
  if [ -f "$f" ] && [ "$(sum "$f")" = "$3" ]; then return 0; fi
  curl -fsSL --retry 3 --max-time 900 -o "$f.part" "$2" || { rm -f "$f.part"; echo "download failed: $2" >&2; return 1; }
  got="$(sum "$f.part")"
  if [ "$got" != "$3" ]; then rm -f "$f.part"; echo "checksum mismatch for $2: want $3, got $got" >&2; return 1; fi
  mv "$f.part" "$f"
}

saxon() { "$java" -cp "$cache/dl/Saxon-HE-$saxon_ver.jar:$cache/dl/xmlresolver-$resolver_ver.jar" net.sf.saxon.Transform "$@"; }

tools() {
  fetch "Saxon-HE-$saxon_ver.jar" "$maven/net/sf/saxon/Saxon-HE/$saxon_ver/Saxon-HE-$saxon_ver.jar" "$saxon_sha"
  fetch "xmlresolver-$resolver_ver.jar" "$maven/org/xmlresolver/xmlresolver/$resolver_ver/xmlresolver-$resolver_ver.jar" "$resolver_sha"
  fetch "schxslt-$schxslt_ver.jar" "$maven/name/dmaus/schxslt/schxslt/$schxslt_ver/schxslt-$schxslt_ver.jar" "$schxslt_sha"
  [ -d "$cache/schxslt" ] || unzip -q -o "$cache/dl/schxslt-$schxslt_ver.jar" 'xslt/*' -d "$cache/schxslt"
}

# compile2 SCH OUT: an ISO Schematron with queryBinding xslt2 to an XSLT that writes SVRL (SchXslt 2.0 pipeline).
compile2() { saxon -s:"$1" -xsl:"$cache/schxslt/xslt/2.0/pipeline-for-svrl.xsl" -o:"$2"; }
# compile_tr SCH OUT: GIB's UBL-TR schematron has the default binding and is not valid ISO Schematron; resolve the includes (SchXslt 1.0), repair it with
# einvoice-tr-prep.xsl, then compile as xslt2 (SchXslt 2.0).
compile_tr() {
  local t; t="$(mktemp -d)"
  saxon -s:"$1" -xsl:"$cache/schxslt/xslt/1.0/include.xsl" -o:"$t/1.sch" && saxon -s:"$t/1.sch" -xsl:"$root/scripts/einvoice-tr-prep.xsl" -o:"$t/2.sch" && compile2 "$t/2.sch" "$2"
  rm -rf "$t"
}

prepare_peppol() {
  fetch "en16931-ubl-$cen_ver.zip" "https://github.com/ConnectingEurope/eInvoicing-EN16931/releases/download/validation-$cen_ver/en16931-ubl-$cen_ver.zip" "$cen_sha"
  fetch "PEPPOL-EN16931-UBL-$peppol_tag.sch" "https://raw.githubusercontent.com/OpenPEPPOL/peppol-bis-invoice-3/$peppol_tag/rules/sch/PEPPOL-EN16931-UBL.sch" "$peppol_sha"
  fetch "UBL-2.1.zip" "https://docs.oasis-open.org/ubl/os-UBL-2.1/UBL-2.1.zip" "$oasis_sha"
  [ -f "$cache/cen/xslt/EN16931-UBL-validation.xslt" ] || unzip -q -o "$cache/dl/en16931-ubl-$cen_ver.zip" 'xslt/*' -d "$cache/cen"
  [ -d "$cache/oasis/xsdrt" ] || unzip -q -o "$cache/dl/UBL-2.1.zip" 'xsdrt/*' -d "$cache/oasis"
  mkdir -p "$cache/compiled"
  [ -f "$cache/compiled/peppol-$peppol_tag.xsl" ] || compile2 "$cache/dl/PEPPOL-EN16931-UBL-$peppol_tag.sch" "$cache/compiled/peppol-$peppol_tag.xsl"
}

prepare_tr() {
  fetch "gib-e-FaturaPaketi.zip" "https://ebelge.gib.gov.tr/dosyalar/kilavuzlar/e-FaturaPaketi%20(29).zip" "$gib_efatura_sha" || return 1
  fetch "gib-UBL-TR1.2.1_Paketi.zip" "https://ebelge.gib.gov.tr/dosyalar/kilavuzlar/UBL-TR1.2.1_Paketi.zip" "$gib_ubltr_sha" || return 1
  [ -d "$cache/gib/schematron" ] || unzip -q -o "$cache/dl/gib-e-FaturaPaketi.zip" 'e-FaturaPaketi/schematron/UBL-TR_*.xml' -d "$cache/gib"
  [ -d "$cache/gib/UBLTR_1.2.1_Paketi/xsdrt" ] || unzip -q -o "$cache/dl/gib-UBL-TR1.2.1_Paketi.zip" 'UBLTR_1.2.1_Paketi/xsdrt/*' -d "$cache/gib"
  mkdir -p "$cache/compiled"
  [ -f "$cache/compiled/ubl-tr.xsl" ] || compile_tr "$cache/gib/e-FaturaPaketi/schematron/UBL-TR_Main_Schematron.xml" "$cache/compiled/ubl-tr.xsl"
}

# Severity: an assertion that fails is an error unless its flag says warning or information (ISO Schematron; GIB's rules carry no flag at all, so every one is an error).
# known-failures.txt: "set: fixed string" per line, a failure message of that set ("peppol" or "tr") that is a documented gap rather than a defect
# (docs/plans/09-einvoice-integrators.md). A line that matches nothing in a run is an error, so the list can only shrink.
known="$root/scripts/einvoice-known-failures.txt"
mkdir -p "$cache"; : >"$cache/known.hits"
begin_set() { cur_set="$1"; sed -n "s/^$1: *//p" "$known" >"$cache/known.$1"; }
# problems SVRL: the message of every failed assertion and report that makes a document invalid, one per line, minus the known failures of the current set.
problems() {
  xmllint --xpath "//*[(local-name()='failed-assert' or local-name()='successful-report') and not(@flag='warning' or @flag='information')]/*[local-name()='text']" "$1" 2>/dev/null |
    sed -e 's#</[^>]*:text>#\n#g' -e 's/<[^>]*>//g' | sed '/^[[:space:]]*$/d' | while IFS= read -r line; do
      if [ -s "$cache/known.$cur_set" ] && grep -qF -f "$cache/known.$cur_set" <<<"$line"; then echo "$line" >>"$cache/known.hits"; else echo "  $line"; fi
    done || true # xmllint exits 10 when nothing matches, which is the good case
}
warnings() { xmllint --xpath "//*[(local-name()='failed-assert' or local-name()='successful-report') and (@flag='warning' or @flag='information')]/*[local-name()='text']/text()" "$1" 2>/dev/null || true; }

bad=0
# check DOC XSD-DIR XSLT...: XSD first, then every schematron; prints what is wrong and counts the document as bad once.
check() {
  local name="$1" doc="$1" xsdroot="$2" ok=1 main out p w tparam; shift 2
  # The integrator puts the e-seal into the empty ext:ExtensionContent of a UBL-TR document; the XSD wants a child there, so a placeholder stands in for it.
  if grep -q '<ext:ExtensionContent/>' "$name"; then
    doc="$cache/stub/$(basename "$name")"; mkdir -p "$cache/stub"
    sed 's#<ext:ExtensionContent/>#<ext:ExtensionContent><seal xmlns="urn:payrudder:unsealed"/></ext:ExtensionContent>#' "$name" >"$doc"
  fi
  main="Invoice"; grep -q '<CreditNote ' "$doc" && main="CreditNote"
  if ! xmllint --noout --nonet --schema "$xsdroot/maindoc/UBL-$main-2.1.xsd" "$doc" 2>"$cache/xsd.err"; then
    echo "FAIL $name: XSD"; sed 's/^/  /' "$cache/xsd.err"; ok=0
  fi
  for x in "$@"; do
    out="$cache/out/$(basename "$doc" .xml)-$(basename "$x" .xsl).svrl"; mkdir -p "$cache/out"
    tparam=""
    # GIB: the e-arsiv profile is checked with the parameter type=earchive, the e-fatura profiles without it.
    case "$x" in *ubl-tr.xsl) if grep -q '<cbc:ProfileID>EARSIVFATURA<' "$doc"; then tparam="type=earchive"; fi ;; esac
    if ! saxon -s:"$doc" -xsl:"$x" -o:"$out" ${tparam:+"$tparam"} 2>"$cache/saxon.err"; then
      echo "FAIL $name: $(basename "$x") could not run"; sed 's/^/  /' "$cache/saxon.err"; ok=0; continue
    fi
    p="$(problems "$out")"
    if [ -n "$p" ]; then echo "FAIL $name: $(basename "$x")"; echo "$p"; ok=0; fi
    w="$(warnings "$out")"; if [ -n "$w" ]; then echo "warn $name: $(echo "$w" | tr '\n' ' ' | cut -c1-240)"; fi
  done
  if [ "$ok" = 1 ]; then echo "ok   $name"; else bad=$((bad + 1)); fi
}

# stale_known SET: every known failure of the set must have been seen in this run.
stale_known() {
  local k
  while IFS= read -r k; do
    [ -n "$k" ] || continue
    if ! grep -qF -- "$k" "$cache/known.hits"; then echo "known failure no longer occurs, delete it from scripts/einvoice-known-failures.txt: $k" >&2; bad=$((bad + 1)); fi
  done <"$cache/known.$1"
}

work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
if [ -z "$docs_dir" ]; then
  # The sample documents are built by the code under test, so what is validated is what the builder produces today, plus the committed golden files.
  samples="$work/samples"; mkdir -p "$samples"
  (cd "$root/pkg" && EINVOICE_SAMPLES_DIR="$samples" CGO_ENABLED=0 go test -count=1 -run '^TestWriteSamples$' ./einvoice/ubl >/dev/null)
  for g in "$root"/pkg/einvoice/ubl/testdata/*.xml; do cp "$g" "$samples/golden-$(basename "$g")"; done
  docs_dir="$samples"
fi

tools
if [ "$set_name" = all ] || [ "$set_name" = peppol ]; then
  prepare_peppol; begin_set peppol
  for d in "$docs_dir"/*peppol-*.xml; do [ -e "$d" ] || continue; check "$d" "$cache/oasis/xsdrt" "$cache/cen/xslt/EN16931-UBL-validation.xslt" "$cache/compiled/peppol-$peppol_tag.xsl"; done
fi
if [ "$set_name" = all ] || [ "$set_name" = tr ]; then
  if prepare_tr; then
    begin_set tr
    for d in "$docs_dir"/*ubl-tr-*.xml; do [ -e "$d" ] || continue; check "$d" "$cache/gib/UBLTR_1.2.1_Paketi/xsdrt" "$cache/compiled/ubl-tr.xsl"; done
    stale_known tr
  elif [ "${EINVOICE_TR_OPTIONAL:-}" = 1 ]; then
    echo "::warning::UBL-TR schematron NOT RUN: the GIB packages could not be downloaded (UNVERIFIED reachability of ebelge.gib.gov.tr)"
  else
    echo "UBL-TR packages could not be downloaded" >&2; bad=$((bad + 1))
  fi
fi

if [ "$bad" -gt 0 ]; then echo "$bad document(s) failed validation" >&2; exit 1; fi
echo "all documents valid"
