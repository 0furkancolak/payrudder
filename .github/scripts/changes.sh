#!/bin/sh
# Reads the names of the files a pull request changes (one per line) on stdin and prints which jobs of ci.yml have to run, as key=value lines for $GITHUB_OUTPUT.
# (Plans and top-level notes are ignored by ci.yml's own paths-ignore; the lint and generated-code job runs for everything else.)
# Only the folders listed here are ever skipped; a path nobody listed runs everything, and so does an empty list. Go tests read docs/*.md, deploy/**/README.md and
# web/admin/src (locales, currencies), so those are not "docs" or "web only" for the Go jobs.
files=$(cat)

# folders with a workflow of their own and no Go in them, plus the plans and the top-level notes
nogo='^(docs/plans/|[^/]+\.md$|LICENSE$|NOTICE$|(sdk/(typescript|php|python)|mcp|examples|docs-site)/)'

# true when some changed file is NOT matched by the pattern (an empty list counts as such a file)
outside() { printf '%s\n' "$files" | grep -Evq "$1"; }
touches() { printf '%s\n' "$files" | grep -Eq "$1"; }
yn() { if "$@"; then echo true; else echo false; fi; }

go=$(yn outside "$nogo")
web=$(yn touches '^(web/admin/|api/openapi/|\.github/)')
[ -z "$files" ] && web=true
echo "go=$go"
echo "web=$web"
