package oidc

import (
	"crypto/hmac"
	"crypto/sha256"
)

// StateKey is the key a caller may use to sign the cookie of a login in progress. It is derived from the client secret, which every replica shares and an
// attacker does not have; the derivation keeps the secret itself out of the MAC.
func (c *Client) StateKey() []byte {
	m := hmac.New(sha256.New, []byte(c.cfg.ClientSecret))
	m.Write([]byte("quotify:oidc:login-state:v1"))
	return m.Sum(nil)
}
