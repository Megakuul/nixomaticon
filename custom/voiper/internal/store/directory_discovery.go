package store

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
)

// DiscoverDirectoryServers returns advertised LDAP SRV endpoints (RFC 2782).
// Selecting and authenticating to one remains an explicit user action.
func DiscoverDirectoryServers(ctx context.Context, domain string) ([]string, error) {
	domain = strings.TrimSuffix(strings.TrimSpace(domain), ".")
	if domain == "" || len(domain) > 253 || net.ParseIP(domain) != nil {
		return nil, errors.New("enter a DNS domain, such as example.org")
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return nil, errors.New("invalid directory discovery domain")
		}
		for _, ch := range label {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-') {
				return nil, errors.New("use an ASCII or punycode DNS domain")
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, records, err := net.DefaultResolver.LookupSRV(ctx, "ldap", "tcp", domain)
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return []string{}, nil
		}
		return nil, err
	}
	endpoints := []string{}
	seen := map[string]bool{}
	for _, record := range records {
		host := strings.TrimSuffix(record.Target, ".")
		if host == "" || record.Port == 0 {
			continue
		}
		endpoint := "ldap://" + net.JoinHostPort(host, strconv.Itoa(int(record.Port)))
		if seen[endpoint] {
			continue
		}
		endpoints = append(endpoints, endpoint)
		seen[endpoint] = true
		if len(endpoints) == 16 {
			break
		}
	}
	return endpoints, nil
}
