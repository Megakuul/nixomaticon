package sip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	wire "github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
)

func (c *Client) authorize(req *wire.Request, response *wire.Response, target *wire.Request) error {
	challengeName, authorizationName := "WWW-Authenticate", "Authorization"
	if response.StatusCode == 407 {
		challengeName, authorizationName = "Proxy-Authenticate", "Proxy-Authorization"
	}
	header := response.GetHeader(challengeName)
	if header == nil {
		return errors.New("authentication response has no challenge")
	}
	challenge, err := digest.ParseChallenge(header.Value())
	if err != nil {
		return err
	}
	challenge.Algorithm = strings.ToUpper(challenge.Algorithm)
	credentials, err := digest.Digest(challenge, digest.Options{Method: string(req.Method), URI: req.Recipient.Addr(), Username: c.config.AuthUsername, Password: c.config.Password, GetBody: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(req.Body())), nil }})
	if err != nil {
		return err
	}
	target.RemoveHeader(authorizationName)
	target.AppendHeader(wire.NewHeader(authorizationName, credentials.String()))
	return nil
}

// The template has no dialog headers; rebuilding each retry avoids accumulating
// Route headers and lets sipgo advance the dialog CSeq after authentication.
func (c *Client) dialogExchange(ctx context.Context, cl *call, template *wire.Request) (*wire.Request, *wire.Response, wire.ClientTransaction, error) {
	authAttempts, intervalAttempts := 0, 0
	for {
		req := template.Clone()
		var tx wire.ClientTransaction
		var err error
		if cl.incoming != nil {
			tx, err = cl.incoming.TransactionRequest(ctx, req)
		} else {
			tx, err = cl.outgoing.TransactionRequest(ctx, req)
		}
		if err != nil {
			return req, nil, nil, err
		}
		var response *wire.Response
	waiting:
		for {
			select {
			case response = <-tx.Responses():
				if response == nil {
					tx.Terminate()
					return req, nil, nil, errors.New("SIP transaction ended without a response")
				}
				if !response.IsProvisional() {
					break waiting
				}
				if req.Method == wire.INVITE {
					if err := c.sendPRACK(ctx, cl, response, nil); err != nil {
						tx.Terminate()
						return req, nil, nil, err
					}
				}
			case <-ctx.Done():
				tx.Terminate()
				return req, nil, nil, ctx.Err()
			case <-tx.Done():
				return req, nil, nil, errors.Join(errors.New("SIP transaction ended"), tx.Err())
			}
		}
		cl.mu.Lock()
		allowAuth := cl.allowAuth
		origin := cl.authOrigin
		cl.mu.Unlock()
		if (!allowAuth && (response.StatusCode == 401 || response.StatusCode == 407)) || !c.credentialsAllowed(req, response, origin) {
			return req, response, tx, nil
		}
		switch {
		case (response.StatusCode == 401 || response.StatusCode == 407) && authAttempts < 2:
			tx.Terminate()
			authAttempts++
			if err := c.authorize(req, response, template); err != nil {
				return req, response, nil, err
			}
		case response.StatusCode == 422 && (req.Method == wire.INVITE || req.Method == wire.UPDATE) && intervalAttempts < 1:
			tx.Terminate()
			intervalAttempts++
			minimum, _, err := parseSession(response.GetHeader("Min-SE"))
			if err != nil || minimum < 90*time.Second {
				return req, response, nil, errors.New("invalid Min-SE in 422 response")
			}
			interval, refresher, _ := parseSession(template.GetHeader("Session-Expires"))
			if refresher == "" {
				refresher = "uac"
			}
			template.RemoveHeader("Session-Expires")
			template.AppendHeader(wire.NewHeader("Session-Expires", fmt.Sprintf("%d;refresher=%s", int(max(interval, minimum)/time.Second), refresher)))
			template.RemoveHeader("Min-SE")
			template.AppendHeader(wire.NewHeader("Min-SE", fmt.Sprint(int(minimum/time.Second))))

		default:
			return req, response, tx, nil
		}
	}
}

func (c *Client) doDialog(ctx context.Context, cl *call, request *wire.Request) (*wire.Response, error) {
	_, response, tx, err := c.dialogExchange(ctx, cl, request)
	if tx != nil {
		defer tx.Terminate()
	}
	if err != nil {
		return response, err
	}
	if !response.IsSuccess() {
		return response, &ResponseError{Method: string(request.Method), Status: response.StatusCode, Reason: response.Reason}
	}
	return response, nil
}
