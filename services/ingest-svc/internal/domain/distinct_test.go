package domain

import "testing"

func TestCheckDistinctProperty(t *testing.T) {
	cases := []struct {
		name     string
		props    map[string]string
		property string
		reject   bool
	}{
		{"not a UNIQUE meter", nil, "", false},
		{"the property is there", map[string]string{"user_id": "alice"}, "user_id", false},
		{"missing", map[string]string{"other": "x"}, "user_id", true},
		{"empty", map[string]string{"user_id": ""}, "user_id", true},
		{"no properties", nil, "user_id", true},
	}
	for _, c := range cases {
		rej := CheckDistinctProperty(c.props, c.property)
		if (rej != nil) != c.reject {
			t.Errorf("%s: reject = %v", c.name, rej)
		}
		if rej != nil && rej.Code != CodeInvalidProperties {
			t.Errorf("%s: code %s, want %s", c.name, rej.Code, CodeInvalidProperties)
		}
	}
}
