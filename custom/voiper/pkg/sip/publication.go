package sip

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"strconv"
	"strings"
	"time"

	wire "github.com/emiago/sipgo/sip"
)

// Publication identifies server-owned state and its granted lifetime. Calls for
// the same publication must be serialized, since each response replaces its tag.
type Publication struct {
	EntityTag string
	Expires   time.Duration
}

// PublishPresence publishes PIDF. Keep the returned tag for updates/removal;
// expiry zero removes the publication. Use PublishPresenceLease when refreshing.
func (c *Client) PublishPresence(ctx context.Context, available bool, note, entityTag string, expiry time.Duration) (string, error) {
	publication, err := c.PublishPresenceLease(ctx, available, note, entityTag, expiry)
	return publication.EntityTag, err
}

func (c *Client) PublishPresenceLease(ctx context.Context, available bool, note, entityTag string, expiry time.Duration) (Publication, error) {
	if expiry == 0 {
		return c.RefreshPresence(ctx, entityTag, expiry)
	}
	basic := "closed"
	if available {
		basic = "open"
	}
	var escapedNote, escapedURI bytes.Buffer
	xml.EscapeText(&escapedNote, []byte(note))
	xml.EscapeText(&escapedURI, []byte("sip:"+c.config.Username+"@"+c.config.Domain))
	body := []byte(`<?xml version="1.0" encoding="UTF-8"?><presence xmlns="urn:ietf:params:xml:ns:pidf" entity="` + escapedURI.String() + `"><tuple id="voiper"><status><basic>` + basic + `</basic></status><note>` + escapedNote.String() + `</note></tuple></presence>`)
	return c.publishPresence(ctx, body, entityTag, expiry)
}

// RefreshPresence refreshes without resending state, or removes it with expiry
// zero. A 412 response means the tag is gone: publish the desired state anew.
func (c *Client) RefreshPresence(ctx context.Context, entityTag string, expiry time.Duration) (Publication, error) {
	if entityTag == "" {
		return Publication{}, errors.New("publication entity tag is required")
	}
	return c.publishPresence(ctx, nil, entityTag, expiry)
}

func (c *Client) publishPresence(ctx context.Context, body []byte, entityTag string, expiry time.Duration) (Publication, error) {
	if expiry < 0 || (expiry > 0 && expiry < time.Second) || (entityTag != "" && !publicationToken(entityTag)) {
		return Publication{}, errors.New("invalid publication expiry or entity tag")
	}
	contentType := ""
	if len(body) > 0 {
		contentType = "application/pidf+xml"
	}
	target := wire.Uri{Scheme: c.registrar.Scheme, User: c.config.Username, Host: c.config.Domain, Port: c.registrar.Port}
	for attempt := 0; ; attempt++ {
		headers := map[string]string{"Event": "presence", "Expires": strconv.FormatInt(int64(expiry/time.Second), 10)}
		if entityTag != "" {
			headers["SIP-If-Match"] = entityTag
		}
		result, err := c.SendRequest(ctx, "PUBLISH", target.String(), contentType, body, headers)
		if result.Status == 423 && attempt == 0 && expiry > 0 {
			minimum, parseErr := publicationExpiry(result, "Min-Expires")
			if parseErr != nil || minimum <= expiry {
				return Publication{}, errors.New("invalid Min-Expires in publication response")
			}
			expiry = minimum
			continue
		}
		if err != nil {
			var response *ResponseError
			if errors.As(err, &response) {
				value := strings.Fields(strings.Split(publicationHeader(result, "Retry-After"), ";")[0])
				if len(value) > 0 {
					if seconds, parseErr := strconv.ParseUint(value[0], 10, 32); parseErr == nil {
						response.RetryAfter = time.Duration(seconds) * time.Second
					}
				}
			}
			return Publication{}, err
		}
		if expiry == 0 {
			return Publication{}, nil
		}
		granted, err := publicationExpiry(result, "Expires")
		if err != nil || granted <= 0 || granted > expiry {
			return Publication{}, errors.New("invalid Expires in publication response")
		}
		tag := publicationHeader(result, "SIP-ETag")
		if !publicationToken(tag) {
			return Publication{}, errors.New("invalid SIP-ETag in publication response")
		}
		return Publication{EntityTag: tag, Expires: granted}, nil
	}
}

func publicationExpiry(response Response, name string) (time.Duration, error) {
	seconds, err := strconv.ParseUint(publicationHeader(response, name), 10, 32)
	return time.Duration(seconds) * time.Second, err
}

func publicationHeader(response Response, name string) string {
	for key, value := range response.Headers {
		if strings.EqualFold(key, name) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func publicationToken(value string) bool {
	if value == "" || len(value) > 1024 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-.!%*_+`'~", c)) {
			return false
		}
	}
	return true
}
