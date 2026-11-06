package rtp

import pion "github.com/pion/rtp"

// JitterBuffer reorders a bounded window. Its caller owns it and ticks Pop at the
// negotiated packet duration; no worker or wall clock is hidden here.
type JitterBuffer struct {
	packets map[uint16]*pion.Packet
	next    uint16
	started bool
	waiting int
	delay   int
	Lost    uint64
	Late    uint64
}

func NewJitterBuffer(delayFrames int) *JitterBuffer {
	return &JitterBuffer{packets: make(map[uint16]*pion.Packet), delay: max(1, min(delayFrames, 10))}
}
func (b *JitterBuffer) Push(p *pion.Packet) {
	if !b.started {
		b.next = p.SequenceNumber
		b.started = true
	}
	offset := int16(p.SequenceNumber - b.next)
	if offset < 0 {
		b.Late++
		return
	}
	if offset > 50 {
		clear(b.packets)
		b.next = p.SequenceNumber
		b.waiting = 0
	}
	if len(b.packets) >= 50 {
		return
	}
	b.packets[p.SequenceNumber] = p
}
func (b *JitterBuffer) Pop() *pion.Packet {
	if !b.started {
		return nil
	}
	if b.waiting < b.delay {
		b.waiting++
		return nil
	}
	if len(b.packets) == 0 {
		return nil
	}
	p := b.packets[b.next]
	delete(b.packets, b.next)
	b.next++
	if p == nil {
		b.Lost++
	}
	return p
}

func (b *JitterBuffer) Peek() *pion.Packet { return b.packets[b.next] }
