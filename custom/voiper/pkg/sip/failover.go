package sip

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

func resolveRegistrarTargets(ctx context.Context, config Config, discover bool) ([]string, error) {
	host, port := config.Server, config.Port
	if config.OutboundProxy != "" {
		var value string
		host, value, _ = net.SplitHostPort(config.OutboundProxy)
		port, _ = strconv.Atoi(value)
		discover = false
	}
	if ip := net.ParseIP(host); ip != nil {
		return []string{net.JoinHostPort(ip.String(), strconv.Itoa(port))}, nil
	}
	var targets []string
	appendHost := func(host string, port int) error {
		addresses, err := config.Resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return err
		}
		for _, address := range addresses {
			if address.IP.IsUnspecified() || address.IP.IsMulticast() || address.Zone != "" {
				continue
			}
			destination := net.JoinHostPort(address.IP.String(), strconv.Itoa(port))
			duplicate := false
			for _, previous := range targets {
				duplicate = duplicate || previous == destination
			}
			if !duplicate && len(targets) < 8 {
				targets = append(targets, destination)
			}
		}
		return nil
	}
	if discover {
		service, protocol := "sip", config.Transport
		if protocol == "tls" {
			service, protocol = "sips", "tcp"
		}
		_, records, err := config.Resolver.LookupSRV(ctx, service, protocol, host)
		if err == nil && len(records) > 0 {
			// net.Resolver orders SRV records by priority and randomizes equal
			// priorities by weight. A published set must not fall back to host A.
			for _, record := range records[:min(len(records), 16)] {
				if record.Target == "." {
					return nil, errors.New("DNS declares SIP service unavailable")
				}
				if record.Port == 0 {
					continue
				}
				_ = appendHost(strings.TrimSuffix(record.Target, "."), int(record.Port))
				if len(targets) == 8 || ctx.Err() != nil {
					break
				}
			}
			if len(targets) == 0 {
				return nil, errors.New("SIP SRV records have no usable addresses")
			}
			return targets, nil
		}
		var dnsError *net.DNSError
		if err != nil && (!errors.As(err, &dnsError) || !dnsError.IsNotFound) {
			return nil, fmt.Errorf("resolve SIP service: %w", err)
		}
	}
	if err := appendHost(host, port); err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, errors.New("SIP server has no usable addresses")
	}
	return targets, nil
}

func (c *Client) registration(ctx context.Context, expiry time.Duration, retry bool) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()
	c.mu.Lock()
	targets := append([]string(nil), c.registrarTargets...)
	selected, resolved := c.selectedTarget, c.resolvedTargetsAt
	c.mu.Unlock()
	// net.Resolver does not expose TTLs. Refresh this bounded cache every minute;
	// a healthy selected flow stays first while it remains in the DNS result.
	if len(targets) == 0 || time.Since(resolved) >= time.Minute {
		lookupCtx, stopLookup := context.WithTimeout(ctx, 3*time.Second)
		var err error
		targets, err = resolveRegistrarTargets(lookupCtx, c.config, c.registrar.Port == 0)
		stopLookup()
		if err != nil {
			return expiry, err
		}
		c.mu.Lock()
		c.registrarTargets = append([]string(nil), targets...)
		c.resolvedTargetsAt = time.Now()
		c.mu.Unlock()
	}
	for index, target := range targets {
		if target == selected {
			copy(targets[1:index+1], targets[:index])
			targets[0] = selected
			break
		}
	}
	var lastError error
	for _, target := range targets {
		if ctx.Err() != nil {
			return expiry, ctx.Err()
		}
		attemptCtx, stopAttempt := context.WithTimeout(ctx, 3*time.Second)
		granted, err := c.registrationAt(attemptCtx, expiry, retry, target)
		stopAttempt()
		if err == nil {
			return granted, nil
		}
		lastError = err
		if !IsTransientRegistrationError(err) && !errors.Is(err, context.DeadlineExceeded) {
			return expiry, err
		}
	}
	return expiry, lastError
}
