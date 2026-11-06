package sip

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	wire "github.com/emiago/sipgo/sip"
)

// Request and Response form a narrow boundary for optional SIP extensions.
// Headers are copied; the extension never owns a transport or transaction.
type Request struct {
	Method, From, To, CallID, ContentType string
	Headers                               map[string]string
	Body                                  []byte
}
type Response struct {
	Status  int
	Reason  string
	Headers map[string]string
	Body    []byte
}

// SendRequest executes one authenticated out-of-dialog extension transaction.
func (c *Client) SendRequest(ctx context.Context, method, target, contentType string, body []byte, headers map[string]string) (Response, error) {
	switch method {
	case "MESSAGE", "SUBSCRIBE", "PUBLISH", "OPTIONS", "REFER":
	default:
		return Response{}, errors.New("unsupported extension request method")
	}
	uri, err := c.recipient(target)
	if err != nil {
		return Response{}, err
	}
	if len(body) > 65536 || strings.ContainsAny(contentType, "\r\n") {
		return Response{}, errors.New("invalid extension body or content type")
	}
	req := c.request(wire.RequestMethod(method), uri, body)
	if contentType != "" {
		req.AppendHeader(wire.NewHeader("Content-Type", contentType))
	}
	for key, value := range headers {
		if strings.ContainsAny(key, "\r\n: ") || strings.ContainsAny(value, "\r\n") {
			return Response{}, errors.New("invalid extension header")
		}
		switch strings.ToLower(key) {
		case "via", "from", "to", "call-id", "cseq", "contact", "content-length", "authorization", "proxy-authorization":
			return Response{}, fmt.Errorf("extension cannot override %s", key)
		}
		req.AppendHeader(wire.NewHeader(key, value))
	}
	res, err := c.do(ctx, req)
	result := Response{}
	if res != nil {
		result.Status = res.StatusCode
		result.Reason = res.Reason
		result.Body = append([]byte(nil), res.Body()...)
		result.Headers = make(map[string]string)
		for _, h := range res.Headers() {
			result.Headers[h.Name()] = h.Value()
		}
	}
	return result, err
}

// SetExtensionHandler installs a handler for unsupported MESSAGE content and
// unmatched NOTIFY/REFER requests. Returning false leaves standard handling in
// charge. Install only extensions understood by the application and authorize
// their sender/dialog within the handler. The handler must return promptly.
func (c *Client) SetExtensionHandler(handler func(Request) (Response, bool)) {
	c.mu.Lock()
	c.extensionHandler = handler
	c.mu.Unlock()
}

func (c *Client) handleExtension(req *wire.Request, tx wire.ServerTransaction) bool {
	c.mu.Lock()
	handler := c.extensionHandler
	c.mu.Unlock()
	if handler == nil || len(req.Body()) > 65536 {
		return false
	}
	request := Request{Method: string(req.Method), Headers: make(map[string]string), Body: append([]byte(nil), req.Body()...)}
	if req.From() != nil {
		request.From = req.From().Address.String()
	}
	if req.To() != nil {
		request.To = req.To().Address.String()
	}
	if req.CallID() != nil {
		request.CallID = string(*req.CallID())
	}
	if req.ContentType() != nil {
		request.ContentType = req.ContentType().Value()
	}
	for _, h := range req.Headers() {
		request.Headers[h.Name()] = h.Value()
	}
	result, handled := handler(request)
	if !handled {
		return false
	}
	if result.Status < 200 || result.Status > 699 || strings.ContainsAny(result.Reason, "\r\n") || len(result.Body) > 65536 {
		respond(req, tx, 500, "Invalid Extension Response")
		return true
	}
	response := wire.NewResponseFromRequest(req, result.Status, result.Reason, result.Body)
	for key, value := range result.Headers {
		if !strings.ContainsAny(key, "\r\n: ") && !strings.ContainsAny(value, "\r\n") {
			response.AppendHeader(wire.NewHeader(key, value))
		}
	}
	_ = tx.Respond(response)
	return true
}

// TransferToCall asks the original peer to replace the consultation call.
func (c *Client) TransferToCall(ctx context.Context, id, consultationID string) error {
	if id == consultationID {
		return errors.New("cannot transfer a call to itself")
	}
	consultation, err := c.getCall(consultationID)
	if err != nil {
		return err
	}
	consultation.mu.Lock()
	if !consultation.ready || consultation.done {
		consultation.mu.Unlock()
		return errors.New("consultation call is not connected")
	}
	var localTag, remoteTag string
	if consultation.incoming != nil {
		localTag, _ = consultation.incoming.InviteRequest.To().Params.Get("tag")
		remoteTag, _ = consultation.incoming.InviteRequest.From().Params.Get("tag")
	} else {
		localTag, _ = consultation.outgoing.InviteRequest.From().Params.Get("tag")
		remoteTag, _ = consultation.outgoing.InviteResponse.To().Params.Get("tag")
	}
	uri := *consultation.remote.Clone()
	consultation.mu.Unlock()
	if uri.Headers == nil {
		uri.Headers = wire.NewParams()
	}
	uri.Headers.Add("Replaces", url.QueryEscape(consultationID+";to-tag="+remoteTag+";from-tag="+localTag))
	return c.transfer(ctx, id, uri)
}
