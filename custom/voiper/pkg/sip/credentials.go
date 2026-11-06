package sip

import (
	"net"
	"strconv"
	"strings"

	wire "github.com/emiago/sipgo/sip"
)

func (c *Client) accountAuthority(uri wire.Uri) bool {
	if uri.Scheme != "sip" && uri.Scheme != "sips" {
		return false
	}
	if uri.Scheme == "sips" && c.config.Transport != "tls" {
		return false
	}
	port := uri.Port
	if port == 0 {
		port = c.config.Port
	}
	return port == c.config.Port && (strings.EqualFold(uri.Host, c.config.Server) || strings.EqualFold(uri.Host, c.config.Domain))
}

func (c *Client) credentialRoute(req *wire.Request) bool {
	destination := req.Destination()
	c.mu.Lock()
	for _, target := range c.registrarTargets {
		if destination == target {
			c.mu.Unlock()
			return true
		}
	}
	c.mu.Unlock()
	host, portText, err := net.SplitHostPort(destination)
	if err != nil {
		return false
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return false
	}
	if port == 0 {
		port = int(wire.DefaultPort(c.config.Transport))
	}
	route := wire.Uri{Scheme: "sip", Host: host, Port: port}
	if c.accountAuthority(route) {
		return true
	}
	if c.config.OutboundProxy == "" {
		return false
	}
	proxyHost, proxyPort, err := net.SplitHostPort(c.config.OutboundProxy)
	if err != nil {
		proxyHost = c.config.OutboundProxy
		proxyPort = strconv.Itoa(int(wire.DefaultPort(c.config.Transport)))
	}
	return strings.EqualFold(host, proxyHost) && portText == proxyPort
}

// Origin credentials are limited to the account authority, even when a trusted
// proxy carries the request. Proxy credentials require a configured next hop.
func (c *Client) credentialsAllowed(req *wire.Request, response *wire.Response, origin wire.Uri) bool {
	switch response.StatusCode {
	case 401:
		return c.accountAuthority(origin) && c.accountAuthority(req.Recipient) && c.credentialRoute(req)
	case 407:
		return c.credentialRoute(req)
	default:
		return true
	}
}
