package main

import (
	"strings"
	"testing"
)

// The arguments of `payrudderctl audit` are checked before any database is opened, so these run without one.
func TestAuditCommandArguments(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no subcommand":      {nil, "usage: payrudderctl audit verify"},
		"unknown subcommand": {[]string{"delete"}, "usage: payrudderctl audit verify"},
		"prune needs an age": {[]string{"prune"}, "--older-than is required"},
		"prune with apply":   {[]string{"prune", "--apply"}, "--older-than is required"},
		"bad duration":       {[]string{"prune", "--older-than", "a year"}, "invalid value"},
		"unknown flag":       {[]string{"verify", "--everything"}, "flag provided but not defined"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PAYRUDDER_PG_SYSTEM_DSN", "")
			err := auditCmd(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("auditCmd(%v) = %v, want an error containing %q", tc.args, err, tc.want)
			}
		})
	}
}
