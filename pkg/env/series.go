package env

import "strings"

// SandboxNumberPrefix starts every document number of a sandbox tenant (invoices SBX-2026-000123, credit notes SBX-CN-2026-000007). A live tenant's numbers
// never start with it, so a sandbox document cannot be taken for a real one, and the database refuses a number of the wrong series (billing_db CHECKs).
const SandboxNumberPrefix = "SBX-"

// SeriesOf is the environment a document number belongs to: the sandbox series or the real one.
func SeriesOf(number string) Env {
	if strings.HasPrefix(number, SandboxNumberPrefix) {
		return Sandbox
	}
	return Live
}

// NumberMatches reports whether a document number is in the series of the environment. An empty number (a document not yet numbered) matches either.
func NumberMatches(e Env, number string) bool {
	return number == "" || SeriesOf(number) == e
}
