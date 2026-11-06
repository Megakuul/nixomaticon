package sip

import (
	"bytes"
	"context"
	"errors"
	"time"

	wire "github.com/emiago/sipgo/sip"
)

type DelayedAnswer struct {
	SDP []byte
	// Select activates this candidate after its final response wins the call.
	Select func() error
}

type DelayedOfferOptions struct {
	// Prepare runs for each fork and its early UPDATE offers. Its context owns
	// the candidate and is
	// canceled when another fork wins or the call ends. Honor cancellation while
	// gathering; audio capture must wait until connected.
	Prepare func(context.Context, []byte) (DelayedAnswer, error)
}

// DialDelayedOptionsID supports offers in reliable provisional responses and
// final responses. Unselected candidates are canceled without being activated.
func (c *Client) DialDelayedOptionsID(ctx context.Context, id, target string, options DelayedOfferOptions) error {
	if options.Prepare == nil {
		return errors.New("a delayed offer preparation callback is required")
	}
	return c.dialID(ctx, id, target, nil, nil, &options)
}

type delayedBranchKey struct {
	invite inviteKey
	tag    string
}

type delayedBranch struct {
	offer        []byte
	initialOffer []byte
	answer       DelayedAnswer
	cancel       context.CancelFunc
	err          error
	reliable     bool
}

type delayedNegotiation struct {
	ctx      context.Context
	prepare  func(context.Context, []byte) (DelayedAnswer, error)
	branches map[delayedBranchKey]*delayedBranch
	selected *delayedBranch
}

func (d *delayedNegotiation) branch(response *wire.Response) (*delayedBranch, error) {
	if response.To() == nil || len(response.Body()) > 65536 {
		return nil, errors.New("delayed offer has invalid headers or exceeds 64 KiB")
	}
	invite, valid := initialInviteKey(response)
	tag, _ := response.To().Params.Get("tag")
	if !valid || tag == "" {
		return nil, errors.New("delayed offer has no dialog identity")
	}
	key := delayedBranchKey{invite, tag}
	if branch := d.branches[key]; branch != nil {
		if len(response.Body()) > 0 && !bytes.Equal(bytes.TrimSpace(response.Body()), bytes.TrimSpace(branch.offer)) && !bytes.Equal(bytes.TrimSpace(response.Body()), bytes.TrimSpace(branch.initialOffer)) {
			return branch, errors.New("delayed offer changed after reliable negotiation")
		}
		return branch, nil
	}
	if len(d.branches) >= 16 {
		return nil, errors.New("too many delayed offer candidates")
	}
	branch := d.prepareBranch(response)
	d.branches[key] = branch
	return branch, nil
}

func (d *delayedNegotiation) prepareBranch(response *wire.Response) *delayedBranch {
	ctx, cancel := context.WithCancel(d.ctx)
	branch := &delayedBranch{cancel: cancel, offer: append([]byte(nil), response.Body()...)}
	branch.initialOffer = branch.offer
	// The preparation deadline ends when the callback returns. The surviving
	// context continues to own selected media for the whole call.
	timer := time.AfterFunc(5*time.Second, cancel)
	answer, err := prepareDelayedAnswer(ctx, response, func(ctx context.Context, offer []byte) ([]byte, error) {
		var err error
		branch.answer, err = d.prepare(ctx, offer)
		return branch.answer.SDP, err
	})
	if !timer.Stop() {
		cancel()
		if err == nil {
			err = context.DeadlineExceeded
		}
	}
	branch.err = err
	if err != nil {
		cancel()
		branch.answer = DelayedAnswer{SDP: rejectDelayedOffer(response.Body())}
	} else {
		branch.answer.SDP = append([]byte(nil), answer...)
	}
	return branch
}

func (d *delayedNegotiation) provisional(response *wire.Response) ([]byte, error) {
	if len(response.Body()) == 0 {
		return nil, nil
	}
	branch, err := d.branch(response)
	if err != nil {
		return nil, err
	}
	if branch.reliable {
		return nil, nil
	}
	if len(branch.answer.SDP) == 0 {
		return nil, branch.err
	}
	branch.reliable = true
	return branch.answer.SDP, nil
}

func (d *delayedNegotiation) selectFinal(response *wire.Response) ([]byte, []byte, error) {
	branch, err := d.branch(response)
	if branch == nil {
		return rejectDelayedOffer(response.Body()), nil, err
	}
	var ack []byte
	if !branch.reliable {
		ack = branch.answer.SDP
	}
	if err == nil {
		err = branch.err
	}
	if err == nil && d.ctx.Err() != nil {
		err = d.ctx.Err()
	}
	if err == nil && branch.answer.Select != nil {
		err = branch.answer.Select()
	}
	if err != nil {
		if !branch.reliable {
			ack = rejectDelayedOffer(branch.offer)
		}
		return ack, nil, err
	}
	d.selected = branch
	d.cancelUnselected()
	return ack, append([]byte(nil), branch.offer...), nil
}

func (d *delayedNegotiation) cancelUnselected() {
	for _, branch := range d.branches {
		if branch != d.selected {
			branch.cancel()
		}
	}
}
