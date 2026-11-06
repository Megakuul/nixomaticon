package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseRoute(t *testing.T) {
	cases := map[string]string{
		"10.0.0.0/24":              "10.0.0.0/24",
		"10.0.0.0/255.255.255.0":   "10.0.0.0/24",
		"192.168.10.7/255.255.0.0": "192.168.0.0/16",
	}
	for in, want := range cases {
		got, err := parseRoute(in)
		if err != nil || got.String() != want {
			t.Errorf("parseRoute(%q) = %v, %v; want %s", in, got, err, want)
		}
	}
	for _, bad := range []string{"10.0.0.0", "nope/24", "10.0.0.0/nope"} {
		if _, err := parseRoute(bad); err == nil {
			t.Errorf("parseRoute(%q) should fail", bad)
		}
	}
}

func useTempHome(t *testing.T) string {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

func TestConfigDefaultsAndRoundTrip(t *testing.T) {
	useTempHome(t)
	c := loadConfig()
	if c.Port != 443 || c.DNSBackend != "auto" || !c.AutoReconnect {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	c.Server, c.Password, c.AutoReconnect = "vpn.test", "secret", false
	if err := saveConfig(c); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(configFilePath())
	if got := loadConfig(); got.Server != "vpn.test" || got.Password != "" || got.AutoReconnect {
		t.Fatalf("round trip lost data or kept password: %+v (%s)", got, data)
	}
}

func TestConfigLegacyImport(t *testing.T) {
	home := useTempHome(t)
	legacy := filepath.Join(home, ".cache", "nxbender", "config.json")
	os.MkdirAll(filepath.Dir(legacy), 0o700)
	os.WriteFile(legacy, []byte(`{"server":"old.vpn","port":4433,"username":"u","domain":"D","dns_backend":"resolvconf"}`), 0o600)
	c := loadConfig()
	if c.Server != "old.vpn" || c.Port != 4433 || c.DNSBackend != "resolvconf" || !c.AutoReconnect {
		t.Fatalf("legacy import failed: %+v", c)
	}
}
