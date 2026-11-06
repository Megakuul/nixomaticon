// Package audio connects bounded PCM queues to shared Linux desktop audio.
package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
)

var callbackEpoch = time.Now()

type Settings struct{ InputDevice, OutputDevice, Backend string }
type Device struct{ ID, Name, Kind, Backend string }
type Stats struct {
	Stopped                            bool
	CaptureOverruns, PlaybackUnderruns uint64
	InputLevel, OutputLevel            float64
}

type Stream struct {
	tapsMu            sync.Mutex
	taps              atomic.Pointer[referenceTaps]
	lastCallback      atomic.Int64
	stopped           atomic.Bool
	Capture, Playback *Ring
	Reference         *Ring
	context           *malgo.AllocatedContext
	device            *malgo.Device
	Backend           string
	inputPeak         atomic.Uint32
	outputPeak        atomic.Uint32
	overruns          atomic.Uint64
	underruns         atomic.Uint64
	once              sync.Once
}

func backends(name string) ([]malgo.Backend, error) {
	switch strings.ToLower(name) {
	case "", "auto":
		return []malgo.Backend{malgo.BackendPulseaudio, malgo.BackendAlsa}, nil
	case "pulse", "pulseaudio", "pipewire":
		return []malgo.Backend{malgo.BackendPulseaudio}, nil
	case "alsa":
		return []malgo.Backend{malgo.BackendAlsa}, nil
	default:
		return nil, fmt.Errorf("unknown audio backend %q", name)
	}
}
func backendName(b malgo.Backend) string {
	if b == malgo.BackendPulseaudio {
		return "pulse"
	}
	return "alsa"
}

func Devices() ([]Device, error) {
	var devices []Device
	var failures []error
	for _, backend := range []malgo.Backend{malgo.BackendPulseaudio, malgo.BackendAlsa} {
		ctx, err := malgo.InitContext([]malgo.Backend{backend}, malgo.ContextConfig{}, nil)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, kind := range []malgo.DeviceType{malgo.Capture, malgo.Playback} {
			infos, err := ctx.Devices(kind)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			label := "output"
			if kind == malgo.Capture {
				label = "input"
			}
			for _, info := range infos {
				devices = append(devices, Device{backendName(backend) + ":" + info.ID.String(), info.Name(), label, backendName(backend)})
			}
		}
		_ = ctx.Uninit()
		ctx.Free()
	}
	if len(devices) == 0 {
		if err := errors.Join(failures...); err != nil {
			return nil, fmt.Errorf("no audio devices found: %w", err)
		}
		return nil, errors.New("no audio devices found")
	}
	return devices, nil
}

func Open(settings Settings, sampleRate int) (*Stream, error) {
	return open(settings, sampleRate, malgo.Duplex)
}
func OpenPlayback(settings Settings, sampleRate int) (*Stream, error) {
	return open(settings, sampleRate, malgo.Playback)
}
func OpenCapture(settings Settings, sampleRate int) (*Stream, error) {
	return open(settings, sampleRate, malgo.Capture)
}
func open(settings Settings, sampleRate int, kind malgo.DeviceType) (*Stream, error) {
	if sampleRate < 8000 || sampleRate > 48000 {
		return nil, fmt.Errorf("unsupported sample rate %d", sampleRate)
	}
	candidates, err := backends(settings.Backend)
	if err != nil {
		return nil, err
	}
	var failures []error
	for _, backend := range candidates {
		stream, err := openBackend(settings, sampleRate, backend, kind)
		if err == nil {
			return stream, nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", backendName(backend), err))
	}
	return nil, fmt.Errorf("open audio devices: %w", errors.Join(failures...))
}

func openBackend(settings Settings, rate int, backend malgo.Backend, kind malgo.DeviceType) (*Stream, error) {
	ctx, err := malgo.InitContext([]malgo.Backend{backend}, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, err
	}
	s := &Stream{context: ctx, Capture: NewRing(rate * 2 / 5), Playback: NewRing(rate * 2 / 5), Reference: NewRing(rate * 2 / 5), Backend: backendName(backend)}
	s.lastCallback.Store(time.Since(callbackEpoch).Nanoseconds())
	config := malgo.DefaultDeviceConfig(kind)
	config.SampleRate = uint32(rate)
	config.PeriodSizeInMilliseconds = 20
	config.Capture.Format = malgo.FormatS16
	config.Capture.Channels = 1
	config.Playback.Format = malgo.FormatS16
	config.Playback.Channels = 1
	config.Alsa.NoMMap = 1
	config.Pulse.StreamNameCapture = "Voiper microphone"
	config.Pulse.StreamNamePlayback = "Voiper call"
	var ids []unsafe.Pointer
	defer func() {
		for _, id := range ids {
			freeDeviceID(id)
		}
	}()
	for _, selection := range []struct {
		id     string
		kind   malgo.DeviceType
		target *unsafe.Pointer
	}{{settings.InputDevice, malgo.Capture, &config.Capture.DeviceID}, {settings.OutputDevice, malgo.Playback, &config.Playback.DeviceID}} {
		if selection.id == "" || (kind != malgo.Duplex && kind != selection.kind) {
			continue
		}
		infos, err := ctx.Devices(selection.kind)
		if err != nil {
			s.Close()
			return nil, err
		}
		found := false
		for _, info := range infos {
			if selection.id == s.Backend+":"+info.ID.String() {
				id := info.ID
				pointer := id.Pointer()
				ids = append(ids, pointer)
				*selection.target = pointer
				found = true
				break
			}
		}
		if !found {
			s.Close()
			return nil, fmt.Errorf("selected audio device %q is unavailable", selection.id)
		}
	}
	s.device, err = malgo.InitDevice(ctx.Context, config, malgo.DeviceCallbacks{Data: func(output, input []byte, _ uint32) { s.processFrames(output, input) }, Stop: func() { s.stopped.Store(true) }})
	if err != nil {
		s.Close()
		return nil, err
	}
	if err = s.device.Start(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func (s *Stream) processFrames(output, input []byte) {
	s.lastCallback.Store(time.Since(callbackEpoch).Nanoseconds())
	s.inputPeak.Store(peak(input))
	if n := s.Capture.Write(input); n < len(input) {
		s.overruns.Add(1)
	}
	n := s.Playback.Read(output)
	if n < len(output) {
		clear(output[n:])
		s.underruns.Add(1)
	}
	s.Reference.Write(output)
	s.outputPeak.Store(peak(output))

	if taps := s.taps.Load(); taps != nil {
		for _, queue := range taps.queues {
			queue.Write(output)
		}
	}
}
func (s *Stream) Stats() Stats {
	lastCallback := s.lastCallback.Load()
	stopped := s.stopped.Load() || (lastCallback > 0 && time.Since(callbackEpoch).Nanoseconds()-lastCallback > int64(2*time.Second))
	return Stats{Stopped: stopped, CaptureOverruns: s.overruns.Load(), PlaybackUnderruns: s.underruns.Load(), InputLevel: float64(s.inputPeak.Load()) / 32768, OutputLevel: float64(s.outputPeak.Load()) / 32768}
}
func (s *Stream) Close() error {
	s.stopped.Store(true)
	s.once.Do(func() {
		if s.device != nil {
			// Stop wakes the Pulse main loop before Uninit joins its thread. Once
			// the server disconnects, no further callbacks arrive to wake it.
			if s.Backend == "pulse" {
				_ = s.device.Stop()
			}
			s.device.Uninit()
		}
		if s.context != nil {
			_ = s.context.Uninit()
			s.context.Free()
		}
	})
	return nil
}

func peak(data []byte) uint32 {
	var maximum int32
	for i := 0; i+1 < len(data); i += 2 {
		sample := int32(int16(binary.LittleEndian.Uint16(data[i:])))
		if sample < 0 {
			sample = -sample
		}
		maximum = max(maximum, sample)
	}
	return uint32(maximum)
}
