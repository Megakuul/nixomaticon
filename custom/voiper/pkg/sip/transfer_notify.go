package sip

import (
	"context"
	"errors"
	"mime"
	"strconv"
	"strings"
	"time"

	wire "github.com/emiago/sipgo/sip"
)

type outboundTransfer struct {
	sequence       uint32
	remoteSequence uint32
	accepted       bool
	complete       bool
	requireID      bool
	terminal       *Event
	deadline       time.Time
	done           chan struct{}
}

func (c *Client) beginTransfer(cl *call) (*outboundTransfer, error) {
	cl.mu.Lock()
	if cl.done || !cl.ready || (cl.outboundTransfer != nil && !cl.outboundTransfer.complete) {
		cl.mu.Unlock()
		return nil, errors.New("call is not connected or a transfer is pending")
	}
	cl.transferCount++
	transfer := &outboundTransfer{requireID: cl.transferCount > 1, deadline: time.Now().Add(2 * time.Minute), done: make(chan struct{})}
	cl.outboundTransfer = transfer
	cl.mu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		c.completeTransferRequest(cl, transfer, nil, errors.New("SIP client is closed"))
		return nil, errors.New("SIP client is closed")
	}
	c.workers.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.workers.Done()
		timer := time.NewTimer(time.Until(transfer.deadline))
		defer timer.Stop()
		select {
		case <-cl.ctx.Done():
		case <-transfer.done:
		case <-timer.C:
			cl.mu.Lock()
			active := cl.outboundTransfer == transfer && !transfer.complete && !cl.done
			if active {
				transfer.complete = true
				close(transfer.done)
			}
			cl.mu.Unlock()
			if active {
				c.emit(Event{Type: "transfer", CallID: cl.id, State: "failed", StatusCode: 408, Message: "transfer notification timed out"})
			}
		}
	}()
	return transfer, nil
}

// Capture the assigned dialog CSeq before a fast peer can send its NOTIFY.
func (c *Client) rememberTransferRequest(req *wire.Request) {
	if req.CallID() == nil || req.CSeq() == nil {
		return
	}
	cl, err := c.getCall(string(*req.CallID()))
	if err != nil {
		return
	}
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if transfer := cl.outboundTransfer; transfer != nil && !transfer.complete {
		transfer.sequence = req.CSeq().SeqNo
		transfer.terminal = nil
	}
}

func (c *Client) completeTransferRequest(cl *call, transfer *outboundTransfer, response *wire.Response, err error) {
	cl.mu.Lock()
	if cl.outboundTransfer != transfer || transfer.complete {
		cl.mu.Unlock()
		return
	}
	var terminal *Event
	if err != nil {
		transfer.complete = true
		close(transfer.done)
	} else if header := response.GetHeader("Refer-Sub"); header != nil && strings.EqualFold(strings.TrimSpace(header.Value()), "false") {
		transfer.complete = true
		close(transfer.done)
		terminal = &Event{Type: "transfer", CallID: cl.id, State: "failed", Message: "peer disabled transfer result notifications"}
	} else {
		transfer.accepted = true
		terminal = transfer.terminal
		if terminal != nil {
			transfer.complete = true
			close(transfer.done)
		}
	}
	cl.mu.Unlock()
	if terminal != nil {
		c.emit(*terminal)
	}
}

func transferStatus(req *wire.Request) (int, string, int) {
	if req.ContentType() == nil {
		return 0, "", 415
	}
	mediaType, _, err := mime.ParseMediaType(req.ContentType().Value())
	if err != nil || mediaType != "message/sipfrag" {
		return 0, "", 415
	}
	if len(req.Body()) == 0 || len(req.Body()) > 8192 {
		return 0, "", 400
	}
	line, _, _ := strings.Cut(string(req.Body()), "\n")
	fields := strings.Fields(line)
	if !strings.HasPrefix(line, "SIP/2.0 ") || len(fields) < 3 || fields[0] != "SIP/2.0" || len(fields[1]) != 3 {
		return 0, "", 400
	}
	status, err := strconv.Atoi(fields[1])
	if err != nil || status < 100 || status > 699 {
		return 0, "", 400
	}
	state, _, err := mime.ParseMediaType(req.GetHeader("Subscription-State").Value())
	if err != nil || (state != "active" && state != "pending" && state != "terminated") || (status >= 200 && state != "terminated") {
		return 0, "", 400
	}
	if status < 200 {
		if state == "terminated" {
			return 408, "failed", 0
		}
		return status, "pending", 0
	}
	if status < 300 {
		return status, "success", 0
	}
	return status, "failed", 0
}

func (c *Client) onTransferNotify(req *wire.Request, tx wire.ServerTransaction) {
	cl := c.match(req)
	if cl == nil {
		respond(req, tx, 481, "Transfer Subscription Does Not Exist")
		return
	}
	_, parameters, err := mime.ParseMediaType(req.GetHeader("Event").Value())
	if err != nil {
		respond(req, tx, 400, "Invalid Refer Event")
		return
	}
	status, state, failure := transferStatus(req)
	if failure != 0 {
		respond(req, tx, failure, "Invalid Transfer Notification")
		return
	}
	cl.mu.Lock()
	transfer := cl.outboundTransfer
	id, hasID := parameters["id"]
	if transfer == nil || time.Now().After(transfer.deadline) || (transfer.requireID && !hasID) || (hasID && id != strconv.FormatUint(uint64(transfer.sequence), 10)) {
		cl.mu.Unlock()
		respond(req, tx, 481, "Transfer Subscription Does Not Match")
		return
	}
	if transfer.complete || transfer.terminal != nil || req.CSeq().SeqNo <= transfer.remoteSequence {
		cl.mu.Unlock()
		respond(req, tx, 200, "OK")
		return
	}
	if cl.incoming != nil {
		err = cl.incoming.ReadRequest(req, tx)
	} else {
		err = cl.outgoing.ReadRequest(req, tx)
	}
	if err != nil {
		cl.mu.Unlock()
		respond(req, tx, 500, "Invalid Sequence")
		return
	}
	transfer.remoteSequence = req.CSeq().SeqNo
	event := Event{Type: "transfer", CallID: cl.id, StatusCode: status, State: state, Body: append([]byte(nil), req.Body()...), ContentType: "message/sipfrag"}
	emit := state == "pending" || transfer.accepted
	if state != "pending" {
		transfer.terminal = &event
		if transfer.accepted {
			transfer.complete = true
			close(transfer.done)
		}
	}
	cl.mu.Unlock()
	respond(req, tx, 200, "OK")
	if emit {
		c.emit(event)
	}
}

func (c *Client) transfer(ctx context.Context, id string, recipient wire.Uri) error {
	cl, err := c.getCall(id)
	if err != nil {
		return err
	}
	transfer, err := c.beginTransfer(cl)
	if err != nil {
		return err
	}
	response, err := c.inDialog(ctx, id, wire.REFER, nil, wire.NewHeader("Refer-To", "<"+recipient.String()+">"))
	c.completeTransferRequest(cl, transfer, response, err)
	return err
}
