package provider

// #3462 B7 (R2 2026-11-15): read the API key from a gyro CLI profile (~/.config/gyro/config.toml) and, for
// token_storage = "keyring", from the OS key store (macOS Keychain, Windows Credential Manager, Secret Service on Linux)
// the same way gyro CLI stores it (service "gyro-cli", account = profile name).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/zalando/go-keyring"
)

const cliKeyringService = "gyro-cli"

type cliProfile struct {
	APIURL       string `toml:"api_url"`
	Token        string `toml:"token"`
	TokenStorage string `toml:"token_storage"`
}

type cliConfig struct {
	DefaultProfile string                 `toml:"default_profile"`
	Profiles       map[string]*cliProfile `toml:"profiles"`
}

// cliConfigPath: the gyro CLI rule (GYRO_CONFIG_FILE > $XDG_CONFIG_HOME/gyro/config.toml > ~/.config/gyro/config.toml).
func cliConfigPath() string {
	if p := os.Getenv("GYRO_CONFIG_FILE"); p != "" {
		return p
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "gyro", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "gyro", "config.toml")
}

// keyringGet is replaceable in tests.
var keyringGet = keyring.Get

// profileToken returns the API key and URL of a gyro CLI profile. explicit = the profile was named (provider argument or
// GYRO_PROFILE): then a missing file / profile / key is an error; otherwise (default profile) it is just "not found".
func profileToken(name string, explicit bool) (token, apiURL, from string, err error) {
	path := cliConfigPath()
	var cfg cliConfig
	b, rerr := os.ReadFile(path)
	if errors.Is(rerr, os.ErrNotExist) {
		if explicit {
			return "", "", "", fmt.Errorf("profile %q: gyro CLI configuration %s not found (create it with `gyro auth login --profile %s`)", name, path, name)
		}
		return "", "", "", nil
	}
	if rerr != nil {
		return "", "", "", fmt.Errorf("reading %s: %w", path, rerr)
	}
	if _, derr := toml.Decode(string(b), &cfg); derr != nil {
		return "", "", "", fmt.Errorf("%s: %w", path, derr)
	}
	if name == "" {
		name = cfg.DefaultProfile
		if name == "" {
			name = "default"
		}
	}
	p := cfg.Profiles[name]
	if p == nil {
		if explicit {
			return "", "", "", fmt.Errorf("profile %q not found in %s", name, path)
		}
		return "", "", "", nil
	}
	if p.TokenStorage == "keyring" {
		t, kerr := keyringGet(cliKeyringService, name)
		if kerr != nil {
			return "", "", "", fmt.Errorf("profile %q keeps its key in the OS key store, reading it failed: %w", name, kerr)
		}
		return t, p.APIURL, "keyring:" + name, nil
	}
	if p.Token == "" && explicit {
		return "", "", "", fmt.Errorf("profile %q in %s has no API key", name, path)
	}
	return p.Token, p.APIURL, "config:" + name, nil
}
