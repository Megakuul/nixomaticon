package sip

import (
	"errors"
	"strconv"
	"strings"

	wire "github.com/emiago/sipgo/sip"
)

func redirectTarget(current wire.Uri, response *wire.Response, visited map[string]bool) (wire.Uri, error) {
	var selected wire.Uri
	best := -1.0
	for _, header := range response.GetHeaders("Contact") {
		var uri wire.Uri
		params := wire.NewParams()
		if _, err := wire.ParseAddressValue(header.Value(), &uri, &params); err != nil {
			continue
		}
		if uri.Host == "" || (uri.Scheme != "sip" && uri.Scheme != "sips") || len(uri.Headers) > 0 || visited[uri.String()] {
			continue
		}
		if current.Scheme == "sips" && uri.Scheme != "sips" {
			continue
		}
		if value, ok := params.Get("expires"); ok && value == "0" {
			continue
		}
		q := 1.0
		if value, ok := params.Get("q"); ok {
			var err error
			q, err = strconv.ParseFloat(value, 64)
			if err != nil || q < 0 || q > 1 {
				continue
			}
		}
		if q > best {
			selected = uri
			best = q
		}
	}
	if best < 0 {
		return selected, errors.New("redirect has no permitted unvisited SIP destination")
	}
	return selected, nil
}

func removeCredentials(req *wire.Request) {
	for _, header := range append([]wire.Header(nil), req.Headers()...) {
		switch strings.ToLower(header.Name()) {
		case "authorization", "proxy-authorization", "authentication-info", "proxy-authentication-info":
			req.RemoveHeader(header.Name())
		}
	}
}

func sameAuthority(a, b wire.Uri) bool {
	port := func(uri wire.Uri) int {
		if uri.Port != 0 {
			return uri.Port
		}
		if uri.Scheme == "sips" {
			return 5061
		}
		return 5060
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host) && port(a) == port(b)
}
