package config

import (
	"fmt"
	"strings"
)

// ParseSwitch reads an on/off setting such as PAYRUDDER_SAAS: "on", "true", "1" and "yes" switch it on; "off", "false", "0", "no" and the empty string
// switch it off. Anything else is an error, so a typo does not leave a limit on or off by accident.
func ParseSwitch(name, value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true", "1", "yes":
		return true, nil
	case "off", "false", "0", "no", "":
		return false, nil
	}
	return false, fmt.Errorf("%s: %q is not on or off", name, value)
}
