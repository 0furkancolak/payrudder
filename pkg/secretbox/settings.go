package secretbox

import "fmt"

// Settings are the environment variables of a Box, for a service to embed in its Config. Normally only SecretsKey is set. While a key is replaced the new one goes
// there with SecretsKeyID (2, then 3, ...) and the old one moves to SecretsKeyPrevious, where it only opens (docs/runbooks/secrets-key-rotation.md).
type Settings struct {
	// SecretsKey (base64, 32 bytes) seals the boxes made from now on. Without it the features that keep secrets are off.
	SecretsKey string `env:"PAYRUDDER_SECRETS_KEY"`
	// SecretsKeyID is the id SecretsKey carries in the boxes it seals; empty is 1, the id of every box sealed before keys had ids.
	SecretsKeyID int `env:"PAYRUDDER_SECRETS_KEY_ID"`
	// SecretsKeyPrevious (base64, 32 bytes) is the key being replaced: it opens the boxes it sealed until a re-seal has moved them all to SecretsKey.
	SecretsKeyPrevious string `env:"PAYRUDDER_SECRETS_KEY_PREVIOUS"`
	// SecretsKeyPreviousID is the id SecretsKeyPrevious has in the boxes it sealed; empty is 1.
	SecretsKeyPreviousID int `env:"PAYRUDDER_SECRETS_KEY_PREVIOUS_ID"`
}

// SecretBox is the Box the settings describe, or nil when there is no SecretsKey. A mistake in them (a key that is not base64 of 32 bytes, an id outside 1 to 255,
// the same id for both keys, a previous key without a key) is an error, which a service reports at startup.
func (s Settings) SecretBox() (*Box, error) {
	if s.SecretsKey == "" {
		if s.SecretsKeyPrevious != "" {
			return nil, fmt.Errorf("PAYRUDDER_SECRETS_KEY_PREVIOUS needs PAYRUDDER_SECRETS_KEY")
		}
		return nil, nil
	}
	id, err := keyID("PAYRUDDER_SECRETS_KEY_ID", s.SecretsKeyID)
	if err != nil {
		return nil, err
	}
	key, err := ParseKey(s.SecretsKey)
	if err != nil {
		return nil, fmt.Errorf("PAYRUDDER_SECRETS_KEY: %w", err)
	}
	keys := map[byte][]byte{id: key}
	if s.SecretsKeyPrevious != "" {
		prevID, err := keyID("PAYRUDDER_SECRETS_KEY_PREVIOUS_ID", s.SecretsKeyPreviousID)
		if err != nil {
			return nil, err
		}
		if prevID == id {
			return nil, fmt.Errorf("PAYRUDDER_SECRETS_KEY_ID and PAYRUDDER_SECRETS_KEY_PREVIOUS_ID are both %d: the two keys need different ids", id)
		}
		prev, err := ParseKey(s.SecretsKeyPrevious)
		if err != nil {
			return nil, fmt.Errorf("PAYRUDDER_SECRETS_KEY_PREVIOUS: %w", err)
		}
		keys[prevID] = prev
	}
	return NewRing(id, keys)
}

// keyID reads an id from the settings: 0 (unset) is FirstKeyID.
func keyID(name string, v int) (byte, error) {
	switch {
	case v == 0:
		return FirstKeyID, nil
	case v < 1 || v > 255:
		return 0, fmt.Errorf("%s must be 1 to 255, got %d", name, v)
	}
	return byte(v), nil
}
