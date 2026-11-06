package rtp

import (
	"context"
	"testing"
	"time"

	"github.com/pion/rtcp"
	pion "github.com/pion/rtp"
)

func TestLoopbackRTPAndShutdown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a, err := New(ctx, "127.0.0.1:0", 8000)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := New(ctx, "127.0.0.1:0", 8000)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err = a.SetRemote(b.LocalAddr(), b.ControlAddr(), 8000); err != nil {
		t.Fatal(err)
	}
	if err = b.SetRemote(a.LocalAddr(), a.ControlAddr(), 8000); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err = a.Send(8, []byte{byte(i)}, 160, false); err != nil {
			t.Fatal(err)
		}
	}
	var timestamp uint32
	for i := 0; i < 3; i++ {
		select {
		case packet := <-b.Packets():
			if packet.Payload[0] != byte(i) {
				t.Fatalf("payload %v", packet.Payload)
			}
			if i > 0 && packet.Timestamp-timestamp != 160 {
				t.Fatalf("timestamp step %d", packet.Timestamp-timestamp)
			}
			timestamp = packet.Timestamp
		case <-ctx.Done():
			t.Fatal("RTP receive timed out")
		}
	}
	if got := b.Stats(); got.PacketsReceived != 3 || got.PacketsLost != 0 {
		t.Fatalf("stats: %+v", got)
	}
	cancel()
	done := make(chan struct{})
	go func() { a.Close(); b.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("session shutdown blocked")
	}
}
func TestSequenceWrapAndDuplicates(t *testing.T) {
	s := &Session{clock: 8000}
	now := time.Now()
	for i, sequence := range []uint16{65534, 65535, 0, 2, 1, 2} {
		p := &pion.Packet{Header: pion.Header{SSRC: 1, SequenceNumber: sequence, Timestamp: uint32(i * 160)}}
		accepted := s.track(p, 20, now.Add(time.Duration(i)*20*time.Millisecond))
		if i == 5 && accepted {
			t.Fatal("accepted duplicate")
		}
	}
	if stats := s.Stats(); stats.PacketsReceived != 5 || stats.PacketsLost != 0 {
		t.Fatalf("stats: %+v", stats)
	}
}
func TestJitterBufferReordersAndConceals(t *testing.T) {
	buffer := NewJitterBuffer(1)
	for _, sequence := range []uint16{65535, 1, 0, 3} {
		buffer.Push(&pion.Packet{Header: pion.Header{SequenceNumber: sequence}})
	}
	if buffer.Pop() != nil {
		t.Fatal("did not wait for initial playout delay")
	}
	for _, sequence := range []uint16{65535, 0, 1} {
		packet := buffer.Pop()
		if packet == nil || packet.SequenceNumber != sequence {
			t.Fatalf("wanted %d, got %v", sequence, packet)
		}
	}
	if buffer.Pop() != nil || buffer.Lost != 1 {
		t.Fatal("missing packet not recorded")
	}
	if packet := buffer.Pop(); packet == nil || packet.SequenceNumber != 3 {
		t.Fatal("failed to continue after loss")
	}
}

func TestSRTPRejectsTamperingAndReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	a, err := New(ctx, "127.0.0.1:0", 8000)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := New(ctx, "127.0.0.1:0", 8000)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	keyA := make([]byte, 30)
	keyB := make([]byte, 30)
	keyA[0] = 1
	keyB[0] = 2
	if err = a.ConfigureSRTP(keyA, keyB); err != nil {
		t.Fatal(err)
	}
	if err = b.ConfigureSRTP(keyB, keyA); err != nil {
		t.Fatal(err)
	}
	if err = a.SetRemote(b.LocalAddr(), b.ControlAddr(), 8000); err != nil {
		t.Fatal(err)
	}
	if err = b.SetRemote(a.LocalAddr(), a.ControlAddr(), 8000); err != nil {
		t.Fatal(err)
	}
	packet := pion.Packet{Header: pion.Header{Version: 2, PayloadType: 8, SSRC: 123, SequenceNumber: 1}, Payload: []byte{42}}
	plaintext, err := packet.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	encrypted, err := a.outgoing.EncryptRTP(nil, plaintext, nil)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), encrypted...)
	tampered[len(tampered)-1] ^= 1
	for _, data := range [][]byte{plaintext, tampered, encrypted, encrypted} {
		if _, err = a.conn.WriteToUDP(data, b.LocalAddr()); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case received := <-b.Packets():
		if received.Payload[0] != 42 {
			t.Fatal("wrong decrypted payload")
		}
	case <-ctx.Done():
		t.Fatal("SRTP receive timed out")
	}
	// A subsequent valid packet proves the receiver processed the replay first.
	packet.SequenceNumber = 2
	packet.Timestamp = 160
	plaintext, _ = packet.Marshal()
	a.mu.Lock()
	encrypted, err = a.outgoing.EncryptRTP(nil, plaintext, nil)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.conn.WriteToUDP(encrypted, b.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	select {
	case received := <-b.Packets():
		if received.SequenceNumber != 2 {
			t.Fatalf("replay accepted: %d", received.SequenceNumber)
		}
	case <-ctx.Done():
		t.Fatal("second SRTP packet timed out")
	}
	if stats := b.Stats(); stats.PacketsDropped != 3 || stats.PacketsReceived != 2 {
		t.Fatalf("SRTP stats: %+v", stats)
	}
}

func TestJitterBufferResumesAfterSilence(t *testing.T) {
	buffer := NewJitterBuffer(1)
	buffer.Push(&pion.Packet{Header: pion.Header{SequenceNumber: 1}})
	buffer.Pop()
	buffer.Pop()
	for i := 0; i < 100; i++ {
		buffer.Pop()
	}
	buffer.Push(&pion.Packet{Header: pion.Header{SequenceNumber: 2}})
	if packet := buffer.Pop(); packet == nil || packet.SequenceNumber != 2 {
		t.Fatal("silence advanced the sequence beyond resumed media")
	}
}

func TestSignaledSourceChangeResetsReceiverSequence(t *testing.T) {
	s := &Session{clock: 8000}
	now := time.Now()
	first := &pion.Packet{Header: pion.Header{SSRC: 1, SequenceNumber: 100}}
	if !s.track(first, 20, now) {
		t.Fatal("first source rejected")
	}
	replacement := &pion.Packet{Header: pion.Header{SSRC: 2, SequenceNumber: 900}}
	if s.track(replacement, 20, now) {
		t.Fatal("unsignaled source accepted")
	}
	s.sourceCanChange = true
	if !s.track(replacement, 20, now) {
		t.Fatal("signaled source rejected")
	}
	if stats := s.Stats(); stats.PacketsReceived != 2 || stats.PacketsLost != 0 {
		t.Fatalf("source change stats %+v", stats)
	}
}

func TestRTTUsesPreviousSenderReport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	a, err := New(ctx, "127.0.0.1:0", 8000)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := New(ctx, "127.0.0.1:0", 8000)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err = a.SetRemote(b.LocalAddr(), b.ControlAddr(), 8000); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.sentReports[123] = time.Now().Add(-100 * time.Millisecond)
	a.sentReports[124] = time.Now()
	source := a.ssrc
	a.mu.Unlock()
	report := &rtcp.ReceiverReport{SSRC: 77, Reports: []rtcp.ReceptionReport{{SSRC: source, LastSenderReport: 123}}}
	data, err := report.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.control.WriteToUDP(data, a.ControlAddr()); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("RTT from previous sender report was ignored")
		case <-ticker.C:
			if a.Stats().RTTMilliseconds >= 100 {
				return
			}
		}
	}
}

func TestClockRateChangeStartsNewSenderSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	a, err := New(ctx, "127.0.0.1:0", 8000)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := New(ctx, "127.0.0.1:0", 8000)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	a.SetRemote(b.LocalAddr(), b.ControlAddr(), 8000)
	b.SetRemote(a.LocalAddr(), a.ControlAddr(), 8000)
	a.Send(8, []byte{1}, 160, false)
	var oldSource uint32
	select {
	case packet := <-b.Packets():
		oldSource = packet.SSRC
	case <-ctx.Done():
		t.Fatal("old RTP missing")
	}
	a.SetRemote(b.LocalAddr(), b.ControlAddr(), 48000)
	b.SetRemote(a.LocalAddr(), a.ControlAddr(), 48000)
	var timestamp uint32
	for frame := 0; frame < 2; frame++ {
		if err = a.Send(111, []byte{2}, 960, frame == 0); err != nil {
			t.Fatal(err)
		}
		select {
		case packet := <-b.Packets():
			if packet.SSRC == oldSource {
				t.Fatal("clock-rate change reused SSRC")
			}
			if frame == 1 && packet.Timestamp-timestamp != 960 {
				t.Fatal("RTP timestamp uses previous clock")
			}
			timestamp = packet.Timestamp
		case <-ctx.Done():
			t.Fatal("new RTP missing")
		}
	}
	if a.Stats().PacketsSent != 3 || a.senderPackets != 2 {
		t.Fatal("sender statistics did not distinguish new SSRC")
	}
}
