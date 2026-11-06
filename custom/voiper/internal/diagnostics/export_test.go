package diagnostics

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExportOmitsUnapprovedText(t *testing.T) {
	secret := "password-from-private-account"
	input := Input{Version: secret, AccountStates: []string{"registered", secret}, Calls: []Call{{State: "connected", Direction: secret, Codec: secret, Transport: secret, ICEState: secret, ICECandidateType: secret, PacketsReceived: 12}}, Audio: Audio{Backend: secret}, DeviceNames: []string{secret}}
	data, err := Export(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "deviceNames") {
		t.Fatalf("private text leaked: %s", data)
	}
	var result map[string]json.RawMessage
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result["calls"]), `"packetsReceived": 12`) {
		t.Fatal("approved metric was lost")
	}
	input.IncludeDeviceNames = true
	data, err = Export(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"deviceNames"`) {
		t.Fatal("explicit device-name export omitted")
	}
}
