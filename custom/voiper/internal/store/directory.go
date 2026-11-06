package store

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

type DirectoryConfig struct {
	URL, BaseDN, BindDN, Password, CAFile string
	Limit                                 int
}

type DirectoryResult struct {
	Contacts  []Contact
	Truncated bool
	Skipped   int
}

// LookupDirectory performs a read-only lookup. ImportDirectory is a separate,
// explicit step so an unavailable server never removes cached or local contacts.
func LookupDirectory(ctx context.Context, cfg DirectoryConfig) (DirectoryResult, error) {
	result := DirectoryResult{Contacts: []Contact{}}
	if strings.TrimSpace(cfg.BaseDN) == "" {
		return result, errors.New("directory base DN is required")
	}
	if _, err := ldap.ParseDN(cfg.BaseDN); err != nil {
		return result, errors.New("invalid directory base DN")
	}
	if cfg.Limit <= 0 {
		cfg.Limit = 1000
	}
	if cfg.Limit > 5000 {
		return result, errors.New("directory result limit must not exceed 5000")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, cleanup, err := connectDirectory(ctx, cfg)
	if err != nil {
		return result, err
	}
	defer cleanup()
	attributes := []string{"entryUUID", "objectGUID", "displayName", "cn", "telephoneNumber", "mobile", "otherTelephone", "homePhone", "ipPhone", "sipURI", "msRTCSIP-PrimaryUserAddress"}
	paging := ldap.NewControlPaging(100)
	request := ldap.NewSearchRequest(cfg.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, cfg.Limit+1, 10, false,
		"(|(telephoneNumber=*)(mobile=*)(otherTelephone=*)(homePhone=*)(ipPhone=*)(sipURI=*)(msRTCSIP-PrimaryUserAddress=*))", attributes, []ldap.Control{paging})
	seen := map[string]bool{}
	for page := 0; page < 100; page++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		response, err := conn.Search(request)
		limited := ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded)
		if err != nil && !limited {
			return result, fmt.Errorf("directory search: %w", err)
		}
		if response == nil {
			return result, errors.New("directory returned no search response")
		}
		for _, entry := range response.Entries {
			if seen[entry.DN] {
				continue
			}
			seen[entry.DN] = true
			contact, skipped := directoryContact(entry)
			result.Skipped += skipped
			if contact.Address == "" {
				continue
			}
			if len(result.Contacts) == cfg.Limit {
				result.Truncated = true
				return result, nil
			}
			result.Contacts = append(result.Contacts, contact)
		}

		if limited {
			result.Truncated = true
			return result, nil
		}
		control, ok := ldap.FindControl(response.Controls, ldap.ControlTypePaging).(*ldap.ControlPaging)
		if !ok || len(control.Cookie) == 0 {
			return result, nil
		}
		paging.SetCookie(control.Cookie)
	}
	result.Truncated = true
	return result, nil
}

func (s *Store) ImportDirectory(contacts []Contact) (int, error) {
	if len(contacts) > 5000 {
		return 0, errors.New("directory import exceeds 5000 contacts")
	}
	for i := range contacts {
		contacts[i].Source = "directory"
	}
	return s.ImportContacts(contacts)
}

func directoryContact(entry *ldap.Entry) (Contact, int) {
	name := strings.TrimSpace(entry.GetAttributeValue("displayName"))
	if name == "" {
		name = strings.TrimSpace(entry.GetAttributeValue("cn"))
	}
	contact := Contact{Name: name, Source: "directory", DirectoryID: entry.DN, Numbers: []ContactNumber{}}
	if id := entry.GetAttributeValue("entryUUID"); id != "" {
		contact.DirectoryID = "uuid:" + id
	} else if id := entry.GetRawAttributeValue("objectGUID"); len(id) != 0 {
		contact.DirectoryID = fmt.Sprintf("guid:%x", id)
	}
	skipped := 0
	seen := map[string]bool{}
	for _, attribute := range []struct{ name, label string }{
		{"telephoneNumber", "Work"}, {"mobile", "Mobile"}, {"otherTelephone", "Other"}, {"homePhone", "Home"}, {"ipPhone", "Extension"}, {"sipURI", "SIP"}, {"msRTCSIP-PrimaryUserAddress", "SIP"},
	} {
		for _, value := range entry.GetAttributeValues(attribute.name) {
			address := strings.TrimSpace(value)
			if seen[address] {
				continue
			}
			candidate := Contact{Name: name, Address: address}
			if err := validContact(candidate); err != nil {
				skipped++
				continue
			}
			seen[address] = true
			if contact.Address == "" {
				contact.Address = address
				continue
			}
			if len(contact.Numbers) >= 20 {
				skipped++
				continue
			}
			contact.Numbers = append(contact.Numbers, ContactNumber{Label: attribute.label, Address: address})
		}
	}
	if contact.Name == "" {
		contact.Name = contact.Address
	}
	return contact, skipped
}

func connectDirectory(ctx context.Context, cfg DirectoryConfig) (*ldap.Conn, func(), error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || (u.Scheme != "ldap" && u.Scheme != "ldaps") || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, nil, errors.New("use an ldap:// or ldaps:// server URL without credentials, path, or query")
	}
	if cfg.BindDN == "" && cfg.Password != "" {
		return nil, nil, errors.New("directory login name is required with a password")
	}
	port := u.Port()
	if port == "" {
		port = "389"
		if u.Scheme == "ldaps" {
			port = "636"
		}
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	raw, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
	if err != nil {
		return nil, nil, fmt.Errorf("connect directory: %w", err)
	}
	tlsConfig := &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}
	if cfg.CAFile != "" {
		file, err := os.Open(cfg.CAFile)
		if err != nil {
			raw.Close()
			return nil, nil, fmt.Errorf("directory CA file: %w", err)
		}
		pem, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
		file.Close()
		if err != nil || len(pem) > 1024*1024 {
			raw.Close()
			return nil, nil, errors.New("cannot read directory CA file (maximum 1 MiB)")
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			raw.Close()
			return nil, nil, errors.New("directory CA file contains no certificates")
		}
		tlsConfig.RootCAs = roots
	}
	if u.Scheme == "ldaps" {
		secure := tls.Client(raw, tlsConfig)
		if err := secure.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, nil, fmt.Errorf("directory TLS: %w", err)
		}
		raw = secure
	}
	conn := ldap.NewConn(raw, u.Scheme == "ldaps")
	conn.Start()
	conn.SetTimeout(10 * time.Second)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	cleanup := func() { stop(); conn.Close() }
	if u.Scheme == "ldap" {
		if err := conn.StartTLS(tlsConfig); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("directory StartTLS: %w", err)
		}
	}
	if cfg.BindDN != "" {
		if err := conn.Bind(cfg.BindDN, cfg.Password); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("directory login: %w", err)
		}
	}
	return conn, cleanup, nil
}

// DiscoverDirectory reads advertised naming contexts from the configured server's
// RootDSE. It never derives a server or credentials from a SIP account.
func DiscoverDirectory(ctx context.Context, cfg DirectoryConfig) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, cleanup, err := connectDirectory(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	result, err := conn.Search(ldap.NewSearchRequest("", ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 10, false, "(objectClass=*)", []string{"defaultNamingContext", "namingContexts"}, nil))
	if err != nil {
		return nil, fmt.Errorf("directory base discovery: %w", err)
	}
	bases := []string{}
	for _, entry := range result.Entries {
		for _, attribute := range []string{"defaultNamingContext", "namingContexts"} {
			for _, base := range entry.GetAttributeValues(attribute) {
				if len(bases) >= 32 {
					return bases, nil
				}
				if base == "" || len(base) > 4096 || slices.Contains(bases, base) {
					continue
				}
				if _, err := ldap.ParseDN(base); err == nil {
					bases = append(bases, base)
				}
			}
		}
	}
	return bases, nil
}
