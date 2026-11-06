package media

import (
	"encoding/binary"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/megakuul/voiper/pkg/audio"
)

var conferenceMu sync.Mutex

type conferenceLink struct {
	queue              *audio.Ring
	resampler          *audio.Resampler
	reference          atomic.Pointer[audio.Ring]
	referenceResampler *audio.Resampler
	stopReference      func()
	sourceRate         int
}
type conferenceLeg struct {
	mu                 sync.Mutex
	closed             bool
	incoming, outgoing []*conferenceLink
}

// JoinConference bridges fixed device PCM rates using mix-minus.
// The desktop audio server mixes local playback. Outgoing speech excludes the
// recipient's audio; echo cancellation references all locally played legs.
func JoinConference(calls ...*Call) error {
	if len(calls) < 2 || len(calls) > 3 {
		return errors.New("a local conference requires two or three connected calls")
	}
	conferenceMu.Lock()
	defer conferenceMu.Unlock()
	seen := make(map[*Call]bool)
	rates := make([]int, len(calls))
	streams := make([]*audio.Stream, len(calls))
	for i, call := range calls {
		if call == nil || seen[call] {
			return errors.New("conference calls must be distinct")
		}
		seen[call] = true
		call.mu.Lock()
		connected := call.connected && !call.closed && !call.held.Load() && !call.connecting
		rates[i] = call.deviceRate
		streams[i] = call.stream
		backend := call.settings.Backend
		if call.stream != nil {
			backend = call.stream.Backend
		}
		call.mu.Unlock()
		if !connected {
			return errors.New("resume and connect every call before joining a conference")
		}
		if backend == "alsa" {
			return errors.New("local conferencing requires shared PulseAudio or PipeWire devices")
		}
		if call.conference.Load() != nil {
			return errors.New("leave the existing conference before joining another")
		}
	}
	legs := make([]*conferenceLeg, len(calls))
	for i := range legs {
		legs[i] = &conferenceLeg{}
	}
	committed := false
	defer func() {
		if !committed {
			for _, leg := range legs {
				leg.close()
			}
		}
	}()
	for source := range calls {
		for target := range calls {
			if source == target {
				continue
			}
			converter, err := audio.NewResampler(rates[source], rates[target])
			if err != nil {
				return err
			}
			referenceConverter, err := audio.NewResampler(rates[source], rates[target])
			if err != nil {
				converter.Close()
				return err
			}
			link := &conferenceLink{queue: audio.NewRing(rates[target] * 2 / 5), resampler: converter, referenceResampler: referenceConverter, sourceRate: rates[source]}
			legs[source].outgoing = append(legs[source].outgoing, link)
			legs[target].incoming = append(legs[target].incoming, link)
		}
	}
	for i, leg := range legs {
		if streams[i] != nil {
			if err := leg.attachReferences(streams[i]); err != nil {
				return err
			}
		}
	}
	for _, call := range calls {
		call.mu.Lock()
	}
	defer func() {
		for _, call := range calls {
			call.mu.Unlock()
		}
	}()
	for i, call := range calls {
		if call.closed || !call.connected || call.connecting || call.held.Load() || call.deviceRate != rates[i] || call.stream != streams[i] || call.conference.Load() != nil {
			return errors.New("call changed while starting the conference; try again")
		}
	}
	for i, call := range calls {
		call.conference.Store(legs[i])
	}
	committed = true
	return nil
}
func (c *Call) LeaveConference() {
	if leg := c.conference.Swap(nil); leg != nil {
		leg.close()
	}
}
func (leg *conferenceLeg) close() {
	leg.mu.Lock()
	defer leg.mu.Unlock()
	if leg.closed {
		return
	}
	leg.closed = true
	for _, link := range leg.outgoing {
		link.resampler.Close()
		if link.stopReference != nil {
			link.stopReference()
		}
	}
	for _, link := range leg.incoming {
		link.referenceResampler.Close()
	}
}
func (leg *conferenceLeg) attachReferences(stream *audio.Stream) error {
	leg.mu.Lock()
	defer leg.mu.Unlock()
	if leg.closed {
		return nil
	}
	for _, link := range leg.outgoing {
		queue, stop, err := stream.TapPlayback(link.sourceRate * 2 / 5)
		if err != nil {
			return err
		}
		previous := link.stopReference
		link.reference.Store(queue)
		link.stopReference = stop
		if previous != nil {
			previous()
		}
	}
	return nil
}
func (leg *conferenceLeg) mix(pcm []int16) {
	leg.mu.Lock()
	defer leg.mu.Unlock()
	if leg.closed {
		return
	}
	sum := make([]int32, len(pcm))
	for i, sample := range pcm {
		sum[i] = int32(sample)
	}
	data := make([]byte, len(pcm)*2)
	for _, link := range leg.incoming {
		n := link.queue.Read(data)
		for i := 0; i+1 < n; i += 2 {
			sum[i/2] += int32(int16(binary.LittleEndian.Uint16(data[i:])))
		}
	}
	for i, sample := range sum {
		pcm[i] = int16(max(-32768, min(32767, sample)))
	}
}
func (leg *conferenceLeg) mixReference(pcm []int16) error {
	leg.mu.Lock()
	defer leg.mu.Unlock()
	if leg.closed {
		return nil
	}
	sum := make([]int32, len(pcm))
	for i, sample := range pcm {
		sum[i] = int32(sample)
	}
	for _, link := range leg.incoming {
		queue := link.reference.Load()
		if queue == nil {
			continue
		}
		raw := make([]byte, link.sourceRate/50*2)
		queue.Read(raw)
		input := make([]int16, len(raw)/2)
		for i := range input {
			input[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
		}
		converted, err := link.referenceResampler.Process(input)
		if err != nil {
			return err
		}
		for i, sample := range converted {
			if i < len(sum) {
				sum[i] += int32(sample)
			}
		}
	}
	for i, sample := range sum {
		pcm[i] = int16(max(-32768, min(32767, sample)))
	}
	return nil
}
func (leg *conferenceLeg) forward(pcm []int16) error {
	leg.mu.Lock()
	defer leg.mu.Unlock()
	if leg.closed {
		return nil
	}
	for _, link := range leg.outgoing {
		output, err := link.resampler.Process(pcm)
		if err != nil {
			return err
		}
		data := make([]byte, len(output)*2)
		for i, sample := range output {
			binary.LittleEndian.PutUint16(data[i*2:], uint16(sample))
		}
		link.queue.Write(data)
	}
	return nil
}
