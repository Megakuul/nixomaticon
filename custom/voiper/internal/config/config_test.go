package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAccountPaths(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../outside", "a/b", "a\\b", "a\x00b"} {
		if _, err := Path(t.TempDir(), name); err == nil {
			t.Errorf("accepted unsafe name %q", name)
		}
	}
	if _, err := Path(t.TempDir(), "Office account"); err != nil {
		t.Fatal(err)
	}
}

func TestAuthenticatedPortChangesRequireProtectedSDES(t *testing.T) {
	for _, transport := range []string{"udp", "tcp", "tls"} {
		for _, security := range []string{"disabled", "optional", "required", "dtls"} {
			cfg := Config{Server: "pbx.invalid", Username: "101", Transport: transport, MediaSecurity: security, SymmetricRTP: true}
			wantValid := transport == "tls" && security == "required"
			if err := Validate(cfg); (err == nil) != wantValid {
				t.Errorf("%s/%s: %v", transport, security, err)
			}
		}
	}
}
func TestEncryptedAccountRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts", "office")
	want := &Config{Server: "pbx.invalid", Username: "101", Password: "private", Transport: "tcp"}
	if err := WriteConfig(want, path, "test key"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path + ".toml.secure")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions %o", info.Mode().Perm())
	}
	if _, err = LoadConfig(path, "wrong key"); err == nil {
		t.Fatal("wrong key accepted")
	}
	got, err := LoadConfig(path, "test key")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("config did not round trip: %#v", got)
	}
}
