package sip

import (
	"context"
	"errors"
	"time"

	"github.com/emiago/sipgo"
	wire "github.com/emiago/sipgo/sip"
)

type inviteKey struct {
	callID, localTag, branch string
	sequence                 uint32
}

type inviteSnapshot struct {
	request       *wire.Request
	delayed       bool
	earlyAnswered map[string]bool
	origin        wire.Uri
	allowAuth     bool
	selectedTag   string
	settled       bool
	expires       time.Time
	forks         map[string]*inviteFork
	sequences     map[string]uint32
}

type inviteFork struct {
	response *wire.Response
	ack      *wire.Request
	answer   []byte
	started  bool
}

// sipgo's request seam captures headers before sending, including internal
// digest retries. Reading its mutable InviteRequest from a transport callback
// would race those retries. Transactions still belong to sipgo.
type inviteRequester struct{ client *Client }

func (r inviteRequester) Request(ctx context.Context, request *wire.Request) (wire.ClientTransaction, error) {
	c := r.client
	if request.Method == wire.ACK {
		// WriteRequest supplies no caller context; late ACKs belong to the
		// account lifetime, even after the original call was canceled.
		ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
		defer cancel()
		connection, err := c.ua.TransportLayer().ClientRequestConnection(ctx, request)
		if err != nil {
			return nil, err
		}
		defer connection.TryClose()
		stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
		defer stop()
		return nil, connection.WriteMsg(request)
	}
	if request.Method == wire.INVITE {
		if tag, _ := request.To().Params.Get("tag"); tag == "" {
			if err := c.rememberInitialInvite(request); err != nil {
				return nil, err
			}
		}
	}
	if request.Method == wire.REFER {
		c.rememberTransferRequest(request)
	}
	c.rememberDialogSequence(request)
	return c.ua.TransactionLayer().Request(ctx, request)
}

func initialInviteKey(message wire.Message) (inviteKey, bool) {
	if message.CallID() == nil || message.From() == nil || message.CSeq() == nil || message.Via() == nil {
		return inviteKey{}, false
	}
	local, _ := message.From().Params.Get("tag")
	branch, _ := message.Via().Params.Get("branch")
	return inviteKey{string(*message.CallID()), local, branch, message.CSeq().SeqNo}, local != "" && branch != ""
}

func (c *Client) rememberInitialInvite(request *wire.Request) error {
	key, ok := initialInviteKey(request)
	if !ok {
		return errors.New("initial INVITE has incomplete transaction identity")
	}
	cl, err := c.getCall(key.callID)
	if err != nil {
		return nil
	}
	cl.mu.Lock()
	origin, allowAuth := cl.authOrigin, cl.allowAuth
	cl.mu.Unlock()
	c.inviteMu.Lock()
	defer c.inviteMu.Unlock()
	count := 0
	for existing := range c.invites {
		if existing.callID == key.callID {
			count++
		}
	}
	if len(c.invites) >= 256 || count >= 16 {
		return errors.New("outgoing INVITE transaction limit reached")
	}
	snapshotRequest := request.Clone()
	snapshotRequest.SetBody(nil)
	c.invites[key] = &inviteSnapshot{request: snapshotRequest, delayed: len(request.Body()) == 0, origin: origin, allowAuth: allowAuth, forks: make(map[string]*inviteFork), sequences: make(map[string]uint32)}
	return nil
}

func (c *Client) rememberDialogSequence(request *wire.Request) {
	if request.CallID() == nil || request.From() == nil || request.To() == nil || request.CSeq() == nil || request.Method == wire.CANCEL {
		return
	}
	remote, _ := request.To().Params.Get("tag")
	local, _ := request.From().Params.Get("tag")
	if remote == "" {
		return
	}
	c.inviteMu.Lock()
	defer c.inviteMu.Unlock()
	for key, snapshot := range c.invites {
		if key.callID == string(*request.CallID()) && key.localTag == local {
			if _, known := snapshot.sequences[remote]; known || len(snapshot.sequences) < 16 {
				snapshot.sequences[remote] = max(snapshot.sequences[remote], request.CSeq().SeqNo)
			}
		}
	}
}

func (c *Client) expireInvites() {
	defer c.workers.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			c.inviteMu.Lock()
			clear(c.invites)
			c.inviteMu.Unlock()
			return
		case now := <-ticker.C:
			c.inviteMu.Lock()
			for key, snapshot := range c.invites {
				if !snapshot.expires.IsZero() && !now.Before(snapshot.expires) {
					delete(c.invites, key)
				}
			}
			c.inviteMu.Unlock()
		}
	}
}

func (c *Client) settleInvites(cl *call, selected *wire.Response) bool {
	var selectedKey inviteKey
	var selectedTag string
	if selected != nil {
		selectedKey, _ = initialInviteKey(selected)
		selectedTag, _ = selected.To().Params.Get("tag")
	}
	selectedOK := selected == nil
	var pending []*wire.Response
	c.inviteMu.Lock()
	for key, snapshot := range c.invites {
		if key.callID != cl.id {
			continue
		}
		if snapshot.settled {
			if key == selectedKey && snapshot.selectedTag == selectedTag && selectedTag != "" {
				selectedOK = true
			}
			continue
		}
		snapshot.settled = true
		snapshot.expires = time.Now().Add(32 * time.Second)
		if key == selectedKey {
			snapshot.selectedTag = selectedTag
			selectedOK = selectedTag != ""
		}
		for _, fork := range snapshot.forks {
			pending = append(pending, fork.response)
		}
	}
	c.inviteMu.Unlock()
	for _, response := range pending {
		c.handleForkResponse(response)
	}
	return selectedOK
}

func (c *Client) rememberInitialACK(request *wire.Request, response *wire.Response, ack *wire.Request) {
	key, ok := initialInviteKey(request)
	if !ok {
		return
	}
	tag, _ := response.To().Params.Get("tag")
	c.inviteMu.Lock()
	defer c.inviteMu.Unlock()
	if snapshot := c.invites[key]; snapshot != nil {
		fork := snapshot.forks[tag]
		if fork == nil && c.forkCount(key.callID) < 16 {
			copy := response.Clone()
			copy.SetBody(nil)
			fork = &inviteFork{response: copy}
			snapshot.forks[tag] = fork
		}
		if fork != nil {
			fork.ack = ack.Clone()
		}
	}
}

func (c *Client) forkCount(callID string) int {
	count := 0
	for key, snapshot := range c.invites {
		if key.callID == callID {
			count += len(snapshot.forks)
		}
	}
	return count
}

func (c *Client) handleForkResponse(response *wire.Response) bool {
	key, ok := initialInviteKey(response)
	if !ok {
		return false
	}
	tag, _ := response.To().Params.Get("tag")
	if tag == "" || response.Contact() == nil {
		return false
	}
	c.inviteMu.Lock()
	snapshot := c.invites[key]
	if snapshot == nil || (!snapshot.expires.IsZero() && time.Now().After(snapshot.expires)) {
		c.inviteMu.Unlock()
		return false
	}
	fork := snapshot.forks[tag]
	if fork == nil {
		if c.forkCount(key.callID) >= 16 {
			c.inviteMu.Unlock()
			return false
		}
		copy := response.Clone()
		copy.SetBody(nil)
		fork = &inviteFork{response: copy}
		if snapshot.delayed {
			fork.answer = rejectDelayedOffer(response.Body())
		}
		snapshot.forks[tag] = fork
	}
	var ack *wire.Request
	if fork.ack != nil {
		ack = fork.ack.Clone()
	}
	cleanup := snapshot.settled && tag != snapshot.selectedTag && !fork.started
	sequence := max(key.sequence, snapshot.sequences[tag]) + 1
	if cleanup {
		fork.started = true
	}
	c.inviteMu.Unlock()
	if ack != nil {
		_ = c.client.WriteRequest(ack)
	}
	if cleanup {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return true
		}
		c.workers.Add(1)
		c.mu.Unlock()
		go func() {
			defer c.workers.Done()
			c.endInviteFork(snapshot, fork, sequence)
		}()
	}
	return true
}

func (c *Client) endInviteFork(snapshot *inviteSnapshot, fork *inviteFork, sequence uint32) {
	c.inviteMu.Lock()
	tag, _ := fork.response.To().Params.Get("tag")
	answer := fork.answer
	if snapshot.earlyAnswered[tag] {
		answer = nil
	}
	c.inviteMu.Unlock()
	ack, err := buildInviteACK(snapshot.request, fork.response, answer)
	if err != nil {
		return
	}
	c.inviteMu.Lock()
	fork.ack = ack.Clone()
	c.inviteMu.Unlock()
	if err := c.client.WriteRequest(ack); err != nil {
		return
	}
	bye := ack.Clone()
	bye.Method = wire.BYE
	bye.CSeq().MethodName = wire.BYE
	bye.CSeq().SeqNo = sequence
	bye.RemoveHeader("Via")
	bye.RemoveHeader("Content-Type")
	bye.SetBody(nil)
	removeCredentials(bye)
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	response, err := c.client.Do(ctx, bye)
	for attempt := 0; err == nil && response != nil && (response.StatusCode == 401 || response.StatusCode == 407) && attempt < 2; attempt++ {
		if !snapshot.allowAuth || !c.credentialsAllowed(bye, response, snapshot.origin) {
			return
		}
		response, err = c.client.DoDigestAuth(ctx, bye, response, sipgo.DigestAuth{Username: c.config.AuthUsername, Password: c.config.Password})
	}
}

func (c *Client) rememberEarlyAnswer(response *wire.Response) {
	key, ok := initialInviteKey(response)
	if !ok {
		return
	}
	tag, _ := response.To().Params.Get("tag")
	c.inviteMu.Lock()
	defer c.inviteMu.Unlock()
	if snapshot := c.invites[key]; snapshot != nil {
		if snapshot.earlyAnswered == nil {
			snapshot.earlyAnswered = make(map[string]bool)
		}
		if len(snapshot.earlyAnswered) < 16 {
			snapshot.earlyAnswered[tag] = true
		}
	}
}
