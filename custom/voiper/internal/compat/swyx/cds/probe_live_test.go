package cds

// Temporary live probe to confirm a Swyx client-API login and reveal which
// chat system the account uses. It prints no password or token.
//
//	VOIPER_SWYX_SERVER=10.1.1.17 \
//	VOIPER_SWYX_USER='DOMAIN\user' VOIPER_SWYX_PASS=... \
//	go test -count=1 -run TestLiveChatProbe -v ./internal/compat/swyx/cds
//
// Delete this file after use.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestLiveChatProbe(t *testing.T) {
	server, user, pass := os.Getenv("VOIPER_SWYX_SERVER"), os.Getenv("VOIPER_SWYX_USER"), os.Getenv("VOIPER_SWYX_PASS")
	if server == "" || user == "" || pass == "" {
		t.Skip("set VOIPER_SWYX_SERVER, VOIPER_SWYX_USER and VOIPER_SWYX_PASS")
	}
	client, err := New(Config{Server: server, Username: user, Password: pass,
		Trust: func(host, fingerprint string) error { fmt.Println("certificate", host, fingerprint); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Login(ctx); err != nil {
		t.Fatal("login:", err)
	}
	fmt.Println("login ok, user id", client.UserID())
	show := func(path string, keep ...string) {
		var raw json.RawMessage
		if err := client.call(ctx, "GET", path, nil, &raw); err != nil {
			fmt.Println(path, "error:", err)
			return
		}
		if len(keep) > 0 {
			var value map[string]any
			if json.Unmarshal(raw, &value) == nil {
				filtered := map[string]any{}
				for _, k := range keep {
					if v, ok := value[k]; ok {
						filtered[k] = v
					}
				}
				raw, _ = json.Marshal(filtered)
			}
		}
		fmt.Println(path, string(raw))
	}
	id := fmt.Sprint(client.UserID())
	// MessagingSystem: 0 Classic IM, 1 Coligo IM. ConnectorEndpoint is where a
	// chat transport would live if one is reachable after login.
	show("/user/"+id, "MessagingSystem")
	show("/user/" + id + "/chatconfiguration")
	show("/login/tenantinfo", "ConnectorEndpoint", "ConnectorPorts", "AuthenticationEndpoint", "AuthenticationPorts", "TenantAuthenticationDomain")
}
