package config

import "testing"

func TestParseSwitch(t *testing.T) {
	for in, want := range map[string]bool{"on": true, "ON": true, " true ": true, "1": true, "yes": true, "off": false, "Off": false, "false": false, "0": false, "no": false, "": false} {
		got, err := ParseSwitch("PAYRUDDER_SAAS", in)
		if err != nil || got != want {
			t.Errorf("ParseSwitch(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"maybe", "of", "2", "enabled"} {
		if _, err := ParseSwitch("PAYRUDDER_SAAS", bad); err == nil {
			t.Errorf("ParseSwitch(%q) accepted", bad)
		}
	}
}
