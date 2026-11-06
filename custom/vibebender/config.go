package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config holds all runtime configuration for the VPN session.
type Config struct {
	Server      string `json:"server"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Password    string `json:"password,omitempty"`
	Domain      string `json:"domain"`
	Fingerprint string `json:"fingerprint,omitempty"`
	MaxLine     int    `json:"-"`
	DNSBackend  string `json:"dnsBackend"` // "auto", "systemd-resolved", "resolvconf", "networkmanager", "none"
	// AutoReconnect re-establishes dropped sessions with backoff.
	AutoReconnect bool `json:"autoReconnect"`
}

func configFilePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "vibebender", "config.json")
}

// legacyCachePath is where the CLI version stored its settings.
func legacyCachePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "nxbender", "config.json")
}

// loadConfig returns the persisted profile. The password is never stored.
func loadConfig() Config {
	c := Config{Port: 443, DNSBackend: "auto", AutoReconnect: true}
	data, err := os.ReadFile(configFilePath())
	if err != nil {
		var legacy struct {
			Server      string `json:"server"`
			Port        int    `json:"port"`
			Username    string `json:"username"`
			Domain      string `json:"domain"`
			Fingerprint string `json:"fingerprint"`
			DNSBackend  string `json:"dns_backend"`
		}
		if data, err := os.ReadFile(legacyCachePath()); err == nil && json.Unmarshal(data, &legacy) == nil {
			c = Config{
				Server: legacy.Server, Port: legacy.Port, Username: legacy.Username,
				Domain: legacy.Domain, Fingerprint: legacy.Fingerprint, DNSBackend: legacy.DNSBackend,
				AutoReconnect: true,
			}
		}
	} else {
		json.Unmarshal(data, &c)
	}
	c.Password = ""
	if c.Port == 0 {
		c.Port = 443
	}
	if c.DNSBackend == "" {
		c.DNSBackend = "auto"
	}
	return c
}

func saveConfig(c Config) error {
	c.Password = ""
	path := configFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(path, data, 0o600)
}
