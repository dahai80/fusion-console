package config

import (
	"fmt"
	"log"
	"os"

	"github.com/zalando/go-keyring"
)

const keyringService = "fusion-console"

// StoreAPIKey saves an API key in the OS keychain under service
// "fusion-console", account keyID. Config files only keep keyID references.
func StoreAPIKey(keyID, secret string) error {
	if err := keyring.Set(keyringService, keyID, secret); err != nil {
		return fmt.Errorf("config: keychain store %s: %w", keyID, err)
	}
	log.Printf("config: api key %s stored in OS keychain", keyID)
	return nil
}

// GetAPIKey resolves an API key. Resolution order:
//  1. Environment override FUSION_CONSOLE_APIKEY_<ID uppercased, non-alnum to _>
//  2. OS keychain
//
// Environment override exists for CI and headless hosts without a keychain.
func GetAPIKey(keyID string) (string, error) {
	envKey := "FUSION_CONSOLE_APIKEY_" + sanitizeEnvSuffix(keyID)
	if v := os.Getenv(envKey); v != "" {
		return v, nil
	}
	secret, err := keyring.Get(keyringService, keyID)
	if err != nil {
		return "", fmt.Errorf("config: keychain read %s (set env %s to override): %w", keyID, envKey, err)
	}
	return secret, nil
}

// DeleteAPIKey removes an API key from the OS keychain.
func DeleteAPIKey(keyID string) error {
	if err := keyring.Delete(keyringService, keyID); err != nil {
		return fmt.Errorf("config: keychain delete %s: %w", keyID, err)
	}
	return nil
}

func sanitizeEnvSuffix(keyID string) string {
	out := make([]rune, 0, len(keyID))
	for _, r := range keyID {
		switch {
		case r >= 'A' && r <= 'Z':
			out = append(out, r)
		case r >= 'a' && r <= 'z':
			out = append(out, r-32)
		case r >= '0' && r <= '9':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
