package sip

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"mime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emiago/sipgo"
	wire "github.com/emiago/sipgo/sip"
	"github.com/google/uuid"
)

type call struct {
	termination         *atomic.Bool
	session             sessionTimer
	delayedOffer        bool
	transferring        bool
	outboundTransfer    *outboundTransfer
	transferCount       uint32
	allowAuth           bool
	authOrigin          wire.Uri
	ackSDP              []byte
	answerHeaders       []wire.Header
	acks                map[uint32]cachedACK
	delayed             *delayedNegotiation
	earlyDialogs        map[earlyDialogID]*earlyDialog
	earlyMedia          earlyDialogID
	earlyRequest        *wire.Request
	localRSeq           uint32
	remoteRSeq          map[string]uint32
	remoteRSeqInvite    uint32
	prack               chan struct{}
	pendingInvite       *inviteExchange
	transaction         wire.ServerTransaction
	id                  string
	incoming            *sipgo.DialogServerSession
	outgoing            *sipgo.DialogClientSession
	ctx                 context.Context
	cancel              context.CancelFunc
	cancelCause         context.CancelCauseFunc
	mu                  sync.Mutex
	operation           operationLock
	ready               bool
	done                bool
	remote              wire.Uri
	localSDP            []byte
	negotiationRevision uint64
}

func (c *Client) Dial(ctx context.Context, target string, offer []byte) (string, error) {
	id := uuid.NewString()
	return id, c.DialID(ctx, id, target, offer)
}

// DialID lets the caller associate media with a call before signaling events arrive.
func (c *Client) DialID(ctx context.Context, id, target string, offer []byte) error {
	if len(offer) == 0 || len(offer) > 65536 {
		return errors.New("an SDP offer is required (maximum 64 KiB)")
	}
	return c.dialID(ctx, id, target, offer, nil, nil)
}

// DialDelayedID sends a bodyless INVITE. answerOffer prepares an SDP answer to
// the selected final response; it must honor cancellation and must not wait for
// media connectivity before returning. Media can start after connected, whose
// SDP is the remote offer and whose DelayedOffer flag is true.
func (c *Client) DialDelayedID(ctx context.Context, id, target string, answerOffer func(context.Context, []byte) ([]byte, error)) error {
	if answerOffer == nil {
		return errors.New("a delayed offer answer callback is required")
	}
	return c.dialID(ctx, id, target, nil, answerOffer, nil)
}

func (c *Client) dialID(ctx context.Context, id, target string, offer []byte, answerOffer func(context.Context, []byte) ([]byte, error), options *DelayedOfferOptions) error {
	uri, replaces, err := c.inviteTarget(target)
	if err != nil {
		return err
	}
	if id == "" || strings.ContainsAny(id, "\r\n ") {
		return errors.New("invalid call ID")
	}
	callCtx, cancelCause := context.WithCancelCause(ctx)
	cancel := func() { cancelCause(context.Canceled) }
	cl := &call{termination: new(atomic.Bool), authOrigin: uri, allowAuth: true, operation: make(operationLock, 1), id: id, ctx: callCtx, cancel: cancel, cancelCause: cancelCause, remote: uri, localSDP: append([]byte(nil), offer...)}
	if options != nil {
		cl.delayed = &delayedNegotiation{ctx: callCtx, prepare: options.Prepare, branches: make(map[delayedBranchKey]*delayedBranch)}
	}
	c.mu.Lock()
	if c.closed || (c.config.Transport == "tls" && !c.registered) {
		c.mu.Unlock()
		cancel()
		return errors.New("SIP client is closed")
	}
	if _, ok := c.calls[id]; ok {
		c.mu.Unlock()
		cancel()
		return errors.New("call ID already exists")
	}
	c.calls[id] = cl
	c.workers.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.workers.Done()
		stop := context.AfterFunc(c.ctx, cancel)
		defer stop()
		delayed := cl.delayed
		if delayed != nil {
			defer func() {
				_ = cl.operation.Lock(context.Background())
				delayed.cancelUnselected()
				cl.operation.Unlock()
			}()
		}
		req := c.request(wire.INVITE, uri, offer)
		if replaces != "" {
			req.AppendHeader(wire.NewHeader("Replaces", replaces))
			req.AppendHeader(wire.NewHeader("Require", "replaces"))
		}
		callID := wire.CallIDHeader(id)
		req.AppendHeader(&callID)
		if len(offer) > 0 {
			req.AppendHeader(wire.NewHeader("Content-Type", "application/sdp"))
		}
		if answerOffer == nil {
			req.AppendHeader(wire.NewHeader("Supported", "100rel, timer"))
		} else {
			req.AppendHeader(wire.NewHeader("Supported", "timer"))
		}
		req.AppendHeader(wire.NewHeader("Allow", allowedMethods))
		req.AppendHeader(wire.NewHeader("Session-Expires", fmt.Sprintf("%d;refresher=uac", int(c.config.SessionExpires/time.Second))))
		req.AppendHeader(wire.NewHeader("Min-SE", fmt.Sprint(int(c.config.MinSessionExpires/time.Second))))
		var dlg *sipgo.DialogClientSession
		var provisionalErr error
		redirects, intervalRetries := 0, 0
		visited := map[string]bool{uri.String(): true}
		for {
			dlg, err = c.dialogUA().WriteInvite(callCtx, req)
			if err != nil {
				c.finish(cl, err.Error())
				return
			}
			cl.mu.Lock()
			cl.outgoing = dlg
			cl.mu.Unlock()
			defer dlg.Close()
			err = dlg.WaitAnswer(callCtx, sipgo.AnswerOptions{Username: c.config.AuthUsername, Password: c.config.Password, OnResponse: func(res *wire.Response) error {
				if redirects > 0 && !sameAuthority(uri, req.Recipient) && (res.StatusCode == 401 || res.StatusCode == 407) {
					return &ResponseError{Method: "INVITE", Status: res.StatusCode, Reason: "authentication refused for redirected authority"}
				}
				if !c.credentialsAllowed(req, res, uri) {
					return &ResponseError{Method: "INVITE", Status: res.StatusCode, Reason: "authentication refused outside account scope"}
				}
				if answerOffer != nil && res.IsProvisional() && hasToken(res.GetHeaders("Require"), "100rel") {
					provisionalErr = errors.New("reliable provisional offers are not supported for delayed outgoing calls")
					cancel()
					return nil
				}
				if res.IsProvisional() && res.StatusCode > 100 {
					if err := cl.operation.Lock(callCtx); err != nil {
						return err
					}
					defer cl.operation.Unlock()
					c.rememberEarlyDialog(cl, res)
				}
				var prepare func(*wire.Response) ([]byte, error)
				if delayed != nil {
					prepare = delayed.provisional
				}
				if err := c.sendPRACK(callCtx, cl, res, prepare); err != nil {
					cancel()
					return err
				}
				if res.StatusCode == 180 || res.StatusCode == 183 {
					var earlySDP []byte
					if answerOffer == nil && delayed == nil {
						earlySDP = c.earlyMediaSDP(cl, res)
					}
					c.emit(Event{Type: "ringing", CallID: id, State: "ringing", SDP: earlySDP, DelayedOffer: answerOffer != nil || delayed != nil})
				}
				return nil
			}})
			if provisionalErr != nil {
				err = provisionalErr
			}
			if err == nil || dlg.InviteResponse == nil {
				break
			}
			response := dlg.InviteResponse
			req = dlg.InviteRequest.Clone()
			switch {
			case response.StatusCode == 422 && intervalRetries < 1:
				intervalRetries++
				minimum, _, parseErr := parseSession(response.GetHeader("Min-SE"))
				if parseErr != nil || minimum < 90*time.Second {
					err = errors.New("invalid Min-SE in 422 response")
					break
				}
				req.ReplaceHeader(wire.NewHeader("Session-Expires", fmt.Sprintf("%d;refresher=uac", int(max(c.config.SessionExpires, minimum)/time.Second))))
				req.ReplaceHeader(wire.NewHeader("Min-SE", fmt.Sprint(int(minimum/time.Second))))
				err = nil
			case response.StatusCode >= 300 && response.StatusCode <= 302 && redirects < c.config.MaxRedirects && replaces == "":
				var destination wire.Uri
				destination, err = redirectTarget(req.Recipient, response, visited)
				if err == nil {
					_, err = c.recipient(destination.String())
				}
				if err != nil {
					break
				}
				redirects++
				visited[destination.String()] = true
				req.Recipient = destination
				req.To().Address = destination
				c.routeInitialRequest(req)
				cl.mu.Lock()
				cl.remote = destination
				cl.allowAuth = sameAuthority(uri, destination)
				cl.mu.Unlock()
				c.emit(Event{Type: "redirect", CallID: id, RemoteURI: destination.String(), State: "dialing"})
			}
			if err != nil {
				break
			}
			req.RemoveHeader("Via")
			removeCredentials(req)
			req.CSeq().SeqNo++

		}
		cl.mu.Lock()
		alreadyDone := cl.done
		cl.mu.Unlock()
		if err != nil || alreadyDone || callCtx.Err() != nil {
			c.settleInvites(cl, nil)
			message := "canceled"
			if err != nil {
				message = err.Error()
			}
			c.finish(cl, message)
			return
		}
		if !c.settleInvites(cl, dlg.InviteResponse) {
			c.finish(cl, "canceled")
			return
		}
		if err := cl.operation.Lock(c.ctx); err != nil {
			c.finish(cl, err.Error())
			return
		}
		defer cl.operation.Unlock()
		var answer []byte
		remoteSDP := append([]byte(nil), dlg.InviteResponse.Body()...)
		var answerTo, negotiatedLocal []byte
		if answerOffer == nil && delayed == nil {
			remoteSDP, answerTo, negotiatedLocal = c.finalEarlySDP(cl, dlg.InviteResponse, offer)
		}
		var negotiationErr error
		if delayed != nil {
			answer, remoteSDP, negotiationErr = delayed.selectFinal(dlg.InviteResponse)
		}
		if answerOffer != nil {
			answer, negotiationErr = prepareDelayedAnswer(callCtx, dlg.InviteResponse, answerOffer)
			if negotiationErr != nil {
				answer = rejectDelayedOffer(dlg.InviteResponse.Body())
			}
		}
		err = c.acknowledgeInvite(cl, dlg.InviteRequest, dlg.InviteResponse, answer)
		if err != nil {
			c.finish(cl, err.Error())
			return
		}
		if dlg.InviteResponse.Contact() == nil {
			c.finish(cl, "SIP answer has no Contact header")
			return
		}
		c.restoreEarlySequence(cl, dlg.InviteResponse)
		cl.mu.Lock()
		cl.ready = true
		if negotiationErr == nil {
			if delayed != nil {
				cl.localSDP = append([]byte(nil), delayed.selected.answer.SDP...)
			} else if answerOffer != nil {
				cl.localSDP = append([]byte(nil), answer...)
			} else if len(negotiatedLocal) > 0 {
				cl.localSDP = negotiatedLocal
			}
		}
		cl.remote = dlg.InviteResponse.Contact().Address
		ended := cl.done || callCtx.Err() != nil
		cl.mu.Unlock()
		if ended || negotiationErr != nil {
			byeCtx, byeCancel := context.WithTimeout(c.ctx, 5*time.Second)
			defer byeCancel()
			_ = c.byeDialog(byeCtx, cl)
			message := "canceled"
			if negotiationErr != nil {
				message = negotiationErr.Error()
			}
			c.finish(cl, message)
			return
		}
		if err := c.acceptSession(cl, dlg.InviteResponse, true); err != nil {
			_ = c.byeDialog(c.ctx, cl)
			c.finish(cl, err.Error())
			return
		}
		c.startSession(cl)
		c.emit(Event{Type: "connected", CallID: id, State: "connected", RemoteURI: dlg.InviteRequest.Recipient.String(), SDP: remoteSDP, AnswerTo: answerTo, DelayedOffer: answerOffer != nil || delayed != nil})
	}()
	return nil
}

func (c *Client) getCall(id string) (*call, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cl := c.calls[id]
	if cl == nil {
		return nil, errors.New("call does not exist")
	}
	return cl, nil
}

func (c *Client) finish(cl *call, message string) {
	cl.mu.Lock()
	if cl.done {
		cl.mu.Unlock()
		return
	}
	cl.done = true
	cl.mu.Unlock()
	c.settleInvites(cl, nil)
	cl.cancel()
	c.mu.Lock()
	delete(c.calls, cl.id)
	c.mu.Unlock()
	reason := ""
	if cl.termination != nil && cl.termination.Load() {
		reason = "answered-elsewhere"
	}
	c.emit(Event{Type: "ended", CallID: cl.id, State: "ended", Message: message, TerminationReason: reason})
}

func (c *Client) Answer(ctx context.Context, id string, answer []byte) error {
	cl, err := c.getCall(id)
	if err != nil {
		return err
	}
	if len(answer) == 0 || len(answer) > 65536 {
		return errors.New("an SDP answer is required (maximum 64 KiB)")
	}
	if err := cl.operation.Lock(ctx); err != nil {
		return err
	}
	defer cl.operation.Unlock()
	cl.mu.Lock()
	incoming, ready, done := cl.incoming, cl.ready, cl.done
	cl.mu.Unlock()
	if incoming == nil || ready || done {
		return errors.New("call is not awaiting an answer")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// sipgo waits for the ACK while its transaction retransmits the final answer.
	if err := c.respondCall(ctx, cl, func() error {
		headers := append([]wire.Header{wire.NewHeader("Content-Type", "application/sdp")}, cl.answerHeaders...)
		return incoming.Respond(200, "OK", answer, headers...)
	}); err != nil {
		c.finish(cl, err.Error())
		return err
	}
	cl.mu.Lock()
	remoteSDP := append([]byte(nil), incoming.InviteRequest.Body()...)
	if cl.delayedOffer {
		remoteSDP = append([]byte(nil), cl.ackSDP...)
	}
	cl.mu.Unlock()
	if cl.delayedOffer && len(remoteSDP) == 0 {
		cleanupCtx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
		_ = c.byeDialog(cleanupCtx, cl)
		cancel()
		c.finish(cl, "ACK did not contain a valid SDP answer")
		return errors.New("ACK did not contain a valid SDP answer")
	}
	c.restoreEarlySequence(cl, nil)
	cl.mu.Lock()
	cl.ready = true
	cl.localSDP = append([]byte(nil), answer...)
	cl.negotiationRevision++
	cl.mu.Unlock()
	_ = c.acceptSession(cl, incoming.InviteResponse, false)
	c.startSession(cl)
	c.emit(Event{Type: "connected", CallID: id, State: "connected", RemoteURI: cl.remote.String(), SDP: remoteSDP, DelayedOffer: cl.delayedOffer})
	return nil
}

func (c *Client) Reject(ctx context.Context, id string) error {
	cl, err := c.getCall(id)
	if err != nil {
		return err
	}
	if err := cl.operation.Lock(ctx); err != nil {
		return err
	}
	defer cl.operation.Unlock()
	cl.mu.Lock()
	incoming, ready := cl.incoming, cl.ready
	cl.mu.Unlock()
	if incoming == nil || ready {
		return errors.New("call is not awaiting an answer")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err = c.respondCall(ctx, cl, func() error { return incoming.Respond(486, "Busy Here", nil) })
	c.finish(cl, "rejected")
	return err
}

func (c *Client) Hangup(ctx context.Context, id string) error {
	cl, err := c.getCall(id)
	if err != nil {
		return err
	}
	cl.mu.Lock()
	ready, incoming := cl.ready, cl.incoming
	cl.mu.Unlock()
	if !ready {
		if incoming != nil {
			return c.Reject(ctx, id)
		}
		cl.cancel()
		return nil
	}
	if err := cl.operation.Lock(ctx); err != nil {
		return err
	}
	defer cl.operation.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err = c.byeDialog(ctx, cl)
	c.finish(cl, "hangup")
	return err
}

func (c *Client) inDialog(ctx context.Context, id string, method wire.RequestMethod, body []byte, headers ...wire.Header) (*wire.Response, error) {
	return c.inDialogOffer(ctx, id, method, body, nil, headers...)
}

func (c *Client) inDialogOffer(ctx context.Context, id string, method wire.RequestMethod, body []byte, buildOffer func() ([]byte, error), headers ...wire.Header) (*wire.Response, error) {
	cl, err := c.getCall(id)
	if err != nil {
		return nil, err
	}
	cl.mu.Lock()
	ready, done, remote := cl.ready, cl.done, cl.remote
	cl.mu.Unlock()
	if !ready || done {
		return nil, errors.New("call is not connected")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()
	stopCall := context.AfterFunc(cl.ctx, cancel)
	defer stopCall()
	req := wire.NewRequest(method, remote)
	req.SetBody(body)
	for _, header := range headers {
		req.AppendHeader(header)
	}
	if method == wire.INVITE || method == wire.UPDATE {
		req.AppendHeader(wire.NewHeader("Supported", "100rel, timer"))
		req.AppendHeader(wire.NewHeader("Allow", allowedMethods))
		cl.mu.Lock()
		session := cl.session
		cl.mu.Unlock()
		if session.interval > 0 {
			refresher := "uas"
			if session.localRefresher {
				refresher = "uac"
			}
			req.AppendHeader(wire.NewHeader("Session-Expires", fmt.Sprintf("%d;refresher=%s", int(session.interval/time.Second), refresher)))
			req.AppendHeader(wire.NewHeader("Min-SE", fmt.Sprint(int(c.config.MinSessionExpires/time.Second))))
		}
	}
	var sent *wire.Request
	var res *wire.Response
	var tx wire.ClientTransaction
	for attempt := 0; ; attempt++ {
		if err := cl.operation.Lock(ctx); err != nil {
			return nil, err
		}
		cl.mu.Lock()
		ended := cl.done
		revision := cl.negotiationRevision
		req.Recipient = cl.remote
		cl.mu.Unlock()
		if ended {
			cl.operation.Unlock()
			return nil, errors.New("call ended during operation")
		}
		if buildOffer != nil {
			body, err = buildOffer()
			if err != nil || len(body) == 0 || len(body) > 65536 {
				cl.operation.Unlock()
				if err != nil {
					return nil, err
				}
				return nil, errors.New("invalid SDP offer")
			}
			req.SetBody(append([]byte(nil), body...))
		}
		sent, res, tx, err = c.dialogExchange(ctx, cl, req)
		if err != nil || res.StatusCode != 491 || attempt >= 2 || (method != wire.INVITE && method != wire.UPDATE) {
			break
		}
		tx.Terminate()
		cl.operation.Unlock()
		delay := time.Duration(rand.IntN(2000)) * time.Millisecond
		if cl.outgoing != nil {
			delay += 2100 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		cl.mu.Lock()
		changed := cl.negotiationRevision != revision
		cl.mu.Unlock()
		if changed && len(body) > 0 && buildOffer == nil {
			return nil, errors.New("session changed during glare retry; create a fresh offer")
		}
	}
	defer cl.operation.Unlock()
	if err != nil {
		return res, err
	}
	req = sent
	retain := false
	defer func() {
		if !retain {
			tx.Terminate()
		}
	}()
	if !res.IsSuccess() {
		return res, &ResponseError{Method: string(method), Status: res.StatusCode, Reason: res.Reason}
	}
	if method == wire.INVITE {
		if err := c.acknowledgeInvite(cl, req, res, nil); err != nil {
			return nil, err
		}
		tx.OnRetransmission(func(response *wire.Response) { c.onWireMessage(response) })
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil, errors.New("SIP client is closed")
		}
		c.workers.Add(1)
		c.mu.Unlock()
		retain = true
		go func() {
			defer c.workers.Done()
			defer tx.Terminate()
			timer := time.NewTimer(32 * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-cl.ctx.Done():
			case <-c.ctx.Done():
			case <-tx.Done():
			}
		}()

	}
	if method == wire.INVITE || method == wire.UPDATE {
		c.updateRemoteTarget(cl, res.Contact())
		if err := c.acceptSession(cl, res, true); err != nil {
			return nil, err
		}
		c.startSession(cl)
		if len(body) > 0 {
			mediaType := ""
			var typeErr error
			if header := res.ContentType(); header != nil {
				mediaType, _, typeErr = mime.ParseMediaType(header.Value())
			}
			if len(res.Body()) == 0 || len(res.Body()) > 65536 || typeErr != nil || mediaType != "application/sdp" {
				err = errors.New("response did not contain an SDP answer")
			} else {
				err = c.commitAnswer(id, req.Body(), res.Body())
			}
			if err != nil {
				c.emit(Event{Type: "renegotiation-failed", CallID: id, State: "failed", Message: err.Error()})
				return res, err
			}
			cl.mu.Lock()
			cl.localSDP = append([]byte(nil), req.Body()...)
			cl.negotiationRevision++
			cl.mu.Unlock()
			c.emit(Event{Type: "updated", CallID: id, SDP: append([]byte(nil), res.Body()...)})
		}
	}
	return res, nil
}

func (c *Client) Reinvite(ctx context.Context, id string, sdp []byte) error {
	if len(sdp) == 0 || len(sdp) > 65536 {
		return errors.New("invalid SDP offer")
	}
	_, err := c.inDialog(ctx, id, wire.INVITE, sdp, wire.NewHeader("Content-Type", "application/sdp"))
	return err
}

// ReinviteWithOffer constructs the offer after acquiring the call operation,
// keeping offer generation and answer application in the same negotiation.
func (c *Client) ReinviteWithOffer(ctx context.Context, id string, buildOffer func() ([]byte, error)) error {
	if buildOffer == nil {
		return errors.New("offer builder is required")
	}
	_, err := c.inDialogOffer(ctx, id, wire.INVITE, nil, buildOffer, wire.NewHeader("Content-Type", "application/sdp"))
	return err
}

func (c *Client) SendDTMF(ctx context.Context, id, digit string) error {
	if len(digit) != 1 || !strings.Contains("0123456789*#ABCD", digit) {
		return errors.New("invalid DTMF digit")
	}
	_, err := c.inDialog(ctx, id, wire.INFO, []byte("Signal="+digit+"\r\nDuration=160\r\n"), wire.NewHeader("Content-Type", "application/dtmf-relay"))
	return err
}

// Transfer requests a blind transfer. A successful REFER only means acceptance;
// transfer progress is reported separately through NOTIFY events.
func (c *Client) Transfer(ctx context.Context, id, target string) error {
	uri, err := c.recipient(target)
	if err != nil {
		return err
	}
	return c.transfer(ctx, id, uri)
}

// Bound the library's ACK wait with the caller's cancellation and a fixed deadline.
func (c *Client) respondCall(ctx context.Context, cl *call, send func() error) error {
	ctx, cancel := context.WithTimeout(ctx, 32*time.Second)
	defer cancel()
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()
	stopCall := context.AfterFunc(cl.ctx, cancel)
	defer stopCall()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("SIP client is closed")
	}
	c.workers.Add(1)
	c.mu.Unlock()
	result := make(chan error, 1)
	go func() { defer c.workers.Done(); result <- send() }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		cl.transaction.Terminate()
		return ctx.Err()
	}
}

// Redirect returns a 302 response for an unanswered incoming call.
func (c *Client) Redirect(ctx context.Context, id, target string) error {
	uri, err := c.recipient(target)
	if err != nil {
		return err
	}
	cl, err := c.getCall(id)
	if err != nil {
		return err
	}
	if err := cl.operation.Lock(ctx); err != nil {
		return err
	}
	defer cl.operation.Unlock()
	cl.mu.Lock()
	incoming, ready, done := cl.incoming, cl.ready, cl.done
	cl.mu.Unlock()
	if incoming == nil || ready || done {
		return errors.New("only a ringing incoming call can be redirected")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err = c.respondCall(ctx, cl, func() error {
		return incoming.Respond(302, "Moved Temporarily", nil, &wire.ContactHeader{Address: uri})
	})
	c.finish(cl, "redirected")
	return err
}

// AbortCall ends local call state when orderly signaling cannot meet a deadline.
// It cancels pending work; it does not claim that the remote peer received BYE.
func (c *Client) AbortCall(id string) error {
	cl, err := c.getCall(id)
	if err != nil {
		return err
	}
	if cl.cancelCause != nil {
		cl.cancelCause(sipgo.WaitAnswerForceCancelErr)
	} else {
		cl.cancel()
	}
	if cl.transaction != nil {
		cl.transaction.Terminate()
	}
	c.finish(cl, "call aborted locally")
	return nil
}
