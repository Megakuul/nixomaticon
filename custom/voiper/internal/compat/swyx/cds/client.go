// Package cds talks to the SwyxWare "CDS Client REST API" that SwyxIt! and
// other Swyx clients use next to SIP. SwyxWare rejects SIP PUBLISH and MESSAGE
// (405), so own status, status text and the colleagues' status text are only
// reachable here. The contract follows the server's own Swagger document; see
// ../PROTOCOL.md for the observed endpoints.
package cds

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultPort is where SwyxWare serves the client API next to SIP.
const DefaultPort = 9100

const apiPath = "/ippbx/client/v1.0"

// Presence states reported in Presence.Status.
const (
	StatusLoggedOff    = 0
	StatusLoggedOn     = 1
	StatusActive       = 2
	StatusNone         = 3
	StatusAway         = 4
	StatusDoNotDisturb = 5
)

// Messaging systems reported in User.MessagingSystem.
const (
	MessagingClassic = 0
	MessagingColigo  = 1
)

type Config struct {
	// Server is the SwyxWare host, optionally with a port (default DefaultPort).
	Server             string
	Username, Password string
	// RootCAs verifies the server normally. Without it, the server presents a
	// self-signed certificate, so Trust decides on its SHA-256 fingerprint.
	RootCAs *x509.CertPool
	Trust   func(host, fingerprint string) error
}

type Client struct {
	base               string
	http               *http.Client
	username, password string

	mu           sync.Mutex
	accessToken  string
	refreshToken string
	userID       int
}

type Error struct {
	Status  int
	Code    string
	Details string
}

func (e *Error) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("Swyx API %d: %s", e.Status, e.Details)
	}
	return fmt.Sprintf("Swyx API %d %s", e.Status, e.Code)
}

func New(cfg Config) (*Client, error) {
	if cfg.Server == "" || cfg.Username == "" || cfg.Password == "" {
		return nil, errors.New("Swyx API requires server, user name and password")
	}
	host := Address(cfg.Server)
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: cfg.RootCAs}
	if cfg.RootCAs == nil {
		if cfg.Trust == nil {
			return nil, errors.New("Swyx API needs a CA or a certificate trust decision")
		}
		// Verification happens below on the fingerprint instead of a CA chain.
		tlsConfig.InsecureSkipVerify = true
		tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("Swyx API presented no certificate")
			}
			sum := sha256.Sum256(state.PeerCertificates[0].Raw)
			return cfg.Trust(host, hex.EncodeToString(sum[:]))
		}
	}
	transport := &http.Transport{TLSClientConfig: tlsConfig, Proxy: nil, MaxIdleConns: 2, IdleConnTimeout: time.Minute, TLSHandshakeTimeout: 10 * time.Second}
	return &Client{
		base:     "https://" + host + apiPath,
		http:     &http.Client{Transport: transport, Timeout: 20 * time.Second},
		username: cfg.Username,
		password: cfg.Password,
	}, nil
}

// Address is the host:port of the API for a SwyxWare server; certificate
// trust decisions are keyed by it.
func Address(server string) string {
	if _, _, err := net.SplitHostPort(server); err == nil {
		return server
	}
	return net.JoinHostPort(strings.Trim(server, "[]"), strconv.Itoa(DefaultPort))
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

// UserID is the Swyx user the login belongs to, or 0 before the first login.
func (c *Client) UserID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.userID
}

type authentication struct {
	AccessToken  string
	RefreshToken string
	UserId       int
}

// Login acquires a token pair with the account credentials.
func (c *Client) Login(ctx context.Context) error {
	var result authentication
	body := map[string]string{"UserName": c.username, "Password": c.password}
	if err := c.send(ctx, http.MethodPost, "/login/acquiretoken", "", body, &result); err != nil {
		return err
	}
	return c.storeTokens(result)
}

func (c *Client) refresh(ctx context.Context, token string) error {
	var result authentication
	if err := c.send(ctx, http.MethodPost, "/login/refreshtoken?token="+url.QueryEscape(token), "", nil, &result); err != nil {
		return err
	}
	return c.storeTokens(result)
}

func (c *Client) storeTokens(result authentication) error {
	if result.AccessToken == "" || result.UserId == 0 {
		return errors.New("Swyx API login returned no token")
	}
	c.mu.Lock()
	c.accessToken, c.userID = result.AccessToken, result.UserId
	if result.RefreshToken != "" {
		c.refreshToken = result.RefreshToken
	}
	c.mu.Unlock()
	return nil
}

// call performs an authorized request. An expired token is refreshed, or the
// client logs in again, and the request is repeated once.
func (c *Client) call(ctx context.Context, method, path string, body, result any) error {
	c.mu.Lock()
	token, refreshToken := c.accessToken, c.refreshToken
	c.mu.Unlock()
	if token == "" {
		if err := c.Login(ctx); err != nil {
			return err
		}
		return c.call(ctx, method, path, body, result)
	}
	err := c.send(ctx, method, path, token, body, result)
	var apiError *Error
	if !errors.As(err, &apiError) || apiError.Status != http.StatusUnauthorized {
		return err
	}
	if refreshToken == "" || c.refresh(ctx, refreshToken) != nil {
		if err := c.Login(ctx); err != nil {
			return err
		}
	}
	c.mu.Lock()
	token = c.accessToken
	c.mu.Unlock()
	return c.send(ctx, method, path, token, body, result)
}

func (c *Client) send(ctx context.Context, method, path, token string, body, result any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "bearer "+token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		var failure struct {
			ErrorDetails, SecurityErrorCode, ErrorInfo string
		}
		_ = json.Unmarshal(raw, &failure)
		code := failure.SecurityErrorCode
		if code == "" {
			code = failure.ErrorInfo
		}
		return &Error{Status: res.StatusCode, Code: code, Details: failure.ErrorDetails}
	}
	if result == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, result); err != nil {
		return fmt.Errorf("decode Swyx API %s: %w", path, err)
	}
	return nil
}
