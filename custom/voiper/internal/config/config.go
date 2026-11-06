package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/crypto/pbkdf2"
)

const (
	CONFIG_EXTENSION  = ".toml"
	SECURE_EXTENSION  = ".secure"
	SALT_LENGTH       = 32
	PBKDF2_ITERATIONS = 310_000
)

type FeatureCode struct{ Name, Target, Action string }
type ICEServer struct {
	URLs                 []string
	Username, Credential string
}
type Config struct {
	SymmetricRTP     bool          `toml:"symmetric_rtp,omitempty"`
	DelayedOffer     bool          `toml:"delayed_offer,omitempty"`
	ICEPolicy        string        `toml:"ice_policy,omitempty"`
	ICEServers       []ICEServer   `toml:"ice_servers,omitempty"`
	MaxRedirects     int           `toml:"max_redirects,omitempty"`
	ForwardAlways    string        `toml:"forward_always,omitempty"`
	ForwardBusy      string        `toml:"forward_busy,omitempty"`
	ForwardNoAnswer  string        `toml:"forward_no_answer,omitempty"`
	NoAnswerSeconds  int           `toml:"no_answer_seconds,omitempty"`
	Features         []FeatureCode `toml:"features,omitempty"`
	Codecs           []string      `toml:"codecs,omitempty"`
	TLSCAFile        string        `toml:"tls_ca_file,omitempty"`
	AutoEnable       bool          `toml:"auto_enable,omitempty"`
	UseSecretService bool          `toml:"use_secret_service,omitempty"`
	MediaSecurity    string        `toml:"media_security,omitempty"`
	Server           string        `toml:"server"`
	Port             int           `toml:"port"`
	Username         string        `toml:"username"`
	Password         string        `toml:"password"`
	DisplayName      string        `toml:"display_name"`
	Domain           string        `toml:"domain,omitempty"`
	AuthUsername     string        `toml:"auth_username,omitempty"`
	Transport        string        `toml:"transport,omitempty"`
	LocalAddress     string        `toml:"local_address,omitempty"`
	MediaAddress     string        `toml:"media_address,omitempty"`
	OutboundProxy    string        `toml:"outbound_proxy,omitempty"`
	PresenceMode     string        `toml:"presence_mode,omitempty"`
	MessagingMode    string        `toml:"messaging_mode,omitempty"`
	DTMFMode         string        `toml:"dtmf_mode,omitempty"`
	Voicemail        string        `toml:"voicemail,omitempty"`
	// Swyx client-API login, used for status and chat when the server is a Swyx
	// PBX. It is a separate identity (usually the Windows/AD account), not the
	// SIP user name. SwyxPassword follows the wallet like Password.
	SwyxUsername string `toml:"swyx_username,omitempty"`
	SwyxPassword string `toml:"swyx_password,omitempty"`
}

func Path(base, name string) (string, error) {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, "/\\\x00") {
		return "", fmt.Errorf("invalid configuration name")
	}
	return filepath.Join(base, name), nil
}

// ListConfigs lists all configurations in the basePath.
// It returns a map with [key: {configPath - extensions} value: encrypted?].
func ListConfigs(basePath string) (map[string]bool, error) {
	entries, err := os.ReadDir(basePath)
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}

	configs := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if strings.HasSuffix(entry.Name(), CONFIG_EXTENSION+SECURE_EXTENSION) {
			configs[strings.TrimSuffix(
				entry.Name(), CONFIG_EXTENSION+SECURE_EXTENSION,
			)] = true
		} else if strings.HasSuffix(entry.Name(), CONFIG_EXTENSION) {
			configs[strings.TrimSuffix(
				entry.Name(), CONFIG_EXTENSION,
			)] = false
		}
	}
	return configs, nil
}

// LoadConfig loads and deserializes the configuration from disk.
// Fileextensions are added automatically.
// If a decryptionKey is set, the configuration is decrypted with aes256.
func LoadConfig(path, decryptionKey string) (*Config, error) {
	path += CONFIG_EXTENSION
	if decryptionKey != "" {
		path += SECURE_EXTENSION
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	rawConfig, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	if err != nil {
		return nil, err
	}

	if len(rawConfig) > 1024*1024 {
		return nil, fmt.Errorf("configuration exceeds 1 MiB")
	}
	if decryptionKey != "" {
		if len(rawConfig) < SALT_LENGTH {
			return nil, fmt.Errorf("invalid ciphertext")
		}
		salt, ciphertext := rawConfig[:SALT_LENGTH], rawConfig[SALT_LENGTH:]

		block, err := aes.NewCipher(pbkdf2.Key(
			[]byte(decryptionKey), salt, PBKDF2_ITERATIONS, SALT_LENGTH, sha256.New,
		))
		if err != nil {
			return nil, err
		}

		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}

		if len(ciphertext) < gcm.NonceSize() {
			return nil, fmt.Errorf("invalid ciphertext")
		}
		rawConfig, err = gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], nil)
		if err != nil {
			return nil, err
		}
	}

	config := &Config{}
	err = toml.Unmarshal(rawConfig, config)
	if err != nil {
		return nil, err
	}

	return config, nil
}

// WriteConfig serializes the configuration and writes it to disk.
// Fileextensions are added automatically.
// If an encryptionKey is set, the configuration is encrypted with aes256.
func WriteConfig(config *Config, path, encryptionKey string) error {
	path += CONFIG_EXTENSION
	rawConfig, err := toml.Marshal(config)
	if err != nil {
		return err
	}

	if encryptionKey != "" {
		path += SECURE_EXTENSION

		salt := make([]byte, SALT_LENGTH)
		_, err := rand.Read(salt)
		if err != nil {
			return fmt.Errorf("failed to generate salt: %w", err)
		}

		block, err := aes.NewCipher(pbkdf2.Key(
			[]byte(encryptionKey), salt, PBKDF2_ITERATIONS, SALT_LENGTH, sha256.New,
		))
		if err != nil {
			return err
		}

		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return err
		}

		nonce := make([]byte, gcm.NonceSize())
		_, err = rand.Read(nonce)
		if err != nil {
			return fmt.Errorf("failed to generate nonce: %w", err)
		}

		rawConfig = append(append(salt, nonce...), gcm.Seal(nil, nonce, rawConfig, nil)...)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".voiper-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()

	_, err = file.Write(rawConfig)
	if err != nil {
		return err
	}

	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// RemoveConfig deletes a configuration from disk.
// Fileextensions are added automatically.
func RemoveConfig(path string, encrypted bool) error {
	path += CONFIG_EXTENSION
	if encrypted {
		path += SECURE_EXTENSION
	}

	return os.Remove(path)
}
