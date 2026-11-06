package sip

import wire "github.com/emiago/sipgo/sip"

func (c *Client) updateRemoteTarget(cl *call, contact *wire.ContactHeader) {
	if contact != nil {
		copy := wire.HeaderClone(contact).(*wire.ContactHeader)
		cl.mu.Lock()
		cl.remote = copy.Address
		cl.mu.Unlock()
	}
}
