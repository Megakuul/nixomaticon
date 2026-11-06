// Package media joins SDP, codecs, RTP and Linux audio without SIP or UI dependencies.
package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/megakuul/voiper/pkg/audio"
	"github.com/megakuul/voiper/pkg/codec"
	"github.com/megakuul/voiper/pkg/rtp"
)

type ICEServer = rtp.ICEServer

type Settings struct {
	SymmetricRTP                                            bool
	ICEPolicy                                               string
	ICEServers                                              []ICEServer
	EchoCancellation, NoiseSuppression, DisableAutoRecovery bool
	MediaSecurity                                           string
	SecureSignaling                                         bool
	InputDevice, OutputDevice, RingerDevice, Backend        string
	BindAddress                                             string
	Codecs                                                  []string
}
type Device = audio.Device

func Devices() ([]Device, error) { return audio.Devices() }

type Stats struct {
	MediaFlow, MediaWarning                                            string
	SendIdleSeconds, ReceiveIdleSeconds                                float64
	EncoderBitrate                                                     int
	RemotePacketLossPercent, ReceiverReportAgeSeconds                  float64
	ReceiverReportAvailable                                            bool
	CleanupPending                                                     bool
	ICERestartPending                                                  bool
	ICEState, ICECandidateType                                         string
	RTCPMux                                                            bool
	AudioRecoveryPending                                               bool
	AudioRecovering                                                    bool
	AudioRecoveryAttempts                                              uint64
	AudioRecoveryError                                                 string
	DSPAvailable, EchoCancellation, NoiseSuppression                   bool
	EarlyMedia                                                         bool
	AudioStopped                                                       bool
	Recording                                                          bool
	RecordingPath                                                      string
	Conference                                                         bool
	InputGain, OutputGain                                              float64
	InputLevel, OutputLevel                                            float64
	Codec                                                              string
	SampleRate, ClockRate, DeviceSampleRate, PacketizationMilliseconds int
	Transport                                                          string
	Encrypted                                                          bool
	LocalAddress, RemoteAddress                                        string
	PacketsSent, PacketsReceived, PacketsLost, PacketsDropped          uint64
	BytesSent, BytesReceived                                           uint64
	JitterMilliseconds, RTTMilliseconds                                float64
	BufferMilliseconds                                                 int
	CaptureOverruns, PlaybackUnderruns                                 uint64
	Muted, Held                                                        bool
	LastError                                                          string
}

var ErrCleanupPending = errors.New("media cleanup is still pending")

var ErrTelephoneEventsNotNegotiated = errors.New("RTP telephone events were not negotiated")

type Call struct {
	mediaStarted                        time.Time
	localOffer                          []byte
	operations                          sync.WaitGroup
	closeTimeout                        time.Duration
	cleanupComplete                     bool
	closeAudio                          func(*audio.Stream) error
	iceDescription                      rtp.ICEDescription
	restart                             *iceRestart
	incoming                            bool
	nativeOpenPending                   atomic.Bool
	devices                             atomic.Pointer[audioDevices]
	recoveryWake                        chan struct{}
	recoveryStarted, audioRecovering    bool
	audioRecoveryAttempts               uint64
	audioRecoveryError                  string
	recoveryInterval, recoveryTimeout   time.Duration
	processor                           *audio.Processor
	early                               bool
	pipeline                            *mediaPipeline
	deviceRate                          int
	captureStream                       *audio.Stream
	openCapture, openPlayback           func(audio.Settings, int) (*audio.Stream, error)
	closedDone                          chan struct{}
	closeErr                            error
	recording                           atomic.Pointer[audio.Recording]
	conference                          atomic.Pointer[conferenceLeg]
	inputGain, outputGain               atomic.Uint64
	connecting                          bool
	secure                              bool
	dtls                                bool
	fingerprint, dtlsSetup, dtlsProfile string
	tlsID                               string
	localKey                            []byte
	cryptoTag                           string
	openAudio                           func(audio.Settings, int) (*audio.Stream, error)
	mu                                  sync.Mutex
	settings                            Settings
	session                             *rtp.Session
	stream                              *audio.Stream
	formats                             []codec.Format
	remote                              description
	selected                            bool
	connected                           bool
	closed                              bool
	id, version                         uint64
	cancel                              context.CancelFunc
	ctx                                 context.Context
	workers                             sync.WaitGroup
	muted, held                         atomic.Bool
	sendEnabled, receiveEnabled         atomic.Bool
	dtmf                                chan byte
	lastError                           string
}

func NewCall(ctx context.Context, id string, settings Settings, remoteOffer []byte) (*Call, error) {
	policy := strings.ToLower(settings.ICEPolicy)
	if policy == "" {
		policy = "disabled"
	}
	if policy != "disabled" && policy != "auto" && policy != "required" {
		return nil, errors.New("ICE policy must be disabled, auto or required")
	}
	settings.ICEPolicy = policy
	security := strings.ToLower(settings.MediaSecurity)
	if security == "" {
		security = "disabled"
	}
	if security != "disabled" && security != "optional" && security != "required" && security != "dtls" {
		return nil, errors.New("media security must be disabled, optional, required or dtls")
	}
	if (security == "required" || security == "dtls") && !settings.SecureSignaling {
		return nil, errors.New("required SRTP requires verified TLS signaling")
	}
	if settings.SymmetricRTP && (security != "required" || !settings.SecureSignaling) {
		return nil, errors.New("authenticated RTP port changes require required SDES-SRTP and verified TLS signaling")
	}
	formats := codec.Available()
	if len(settings.Codecs) > 0 {
		var enabled []codec.Format
		for _, name := range settings.Codecs {
			for _, format := range formats {
				if strings.EqualFold(name, format.Name) {
					enabled = append(enabled, format)
					break
				}
			}
		}
		formats = enabled
	}
	if len(formats) == 0 {
		return nil, errors.New("no enabled codecs are available in this build")
	}
	c := &Call{closeTimeout: 2 * time.Second, closeAudio: func(stream *audio.Stream) error { return stream.Close() }, incoming: len(remoteOffer) > 0, recoveryWake: make(chan struct{}, 1), recoveryInterval: 500 * time.Millisecond, recoveryTimeout: 5 * time.Second, closedDone: make(chan struct{}), secure: security != "disabled" && settings.SecureSignaling, cryptoTag: "1", openAudio: audio.Open, openCapture: audio.OpenCapture, openPlayback: audio.OpenPlayback, settings: settings, formats: formats, version: 1, dtmf: make(chan byte, 16)}
	c.dtls = security == "dtls"
	c.dtlsSetup, c.dtlsProfile = "actpass", "UDP/TLS/RTP/SAVP"
	c.inputGain.Store(math.Float64bits(1))
	c.outputGain.Store(math.Float64bits(1))
	var seed [8]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, err
	}
	c.id = binary.BigEndian.Uint64(seed[:])
	if len(remoteOffer) > 0 {
		remote, err := negotiate(remoteOffer, formats)
		if err != nil {
			return nil, err
		}
		if c.dtls != (remote.fingerprint != "") {
			return nil, errors.New("peer media key exchange does not match configured security mode")
		}
		if c.dtls {
			c.dtlsProfile = remote.dtlsProfile
			c.dtlsSetup = "active"
			if remote.setup == "active" {
				c.dtlsSetup = "passive"
			}
		}
		if remote.secure && (!settings.SecureSignaling || security == "disabled") {
			return nil, errors.New("secure media requires enabled SDES-SRTP and verified TLS signaling")
		}
		if security == "required" && !remote.secure {
			return nil, errors.New("remote peer did not offer required SRTP")
		}
		c.secure = remote.secure
		if remote.secure {
			c.cryptoTag = remote.cryptoTag
		}
		if policy == "required" && (remote.ice.Username == "" || !remote.mux) {
			return nil, errors.New("peer must support ICE and RTCP multiplexing")
		}
		if policy != "disabled" && remote.ice.Username != "" && !remote.mux {
			return nil, errors.New("ICE requires RTCP multiplexing")
		}
		c.remote = remote
		c.selected = true
	}
	if c.secure && !c.dtls {
		c.localKey = make([]byte, 30)
		if _, err := rand.Read(c.localKey); err != nil {
			return nil, err
		}
	}
	c.ctx, c.cancel = context.WithCancel(ctx)
	session, err := rtp.New(c.ctx, settings.BindAddress, formats[0].ClockRate)
	if err != nil {
		c.cancel()
		return nil, err
	}
	c.session = session
	session.SetSymmetricRTP(settings.SymmetricRTP)
	if c.dtls {
		var identity [16]byte
		if _, err = rand.Read(identity[:]); err != nil {
			c.cancel()
			session.Close()
			return nil, err
		}
		c.tlsID = hex.EncodeToString(identity[:])
		c.fingerprint, err = session.PrepareDTLS()
		if err != nil {
			c.cancel()
			session.Close()
			return nil, err
		}
	}
	if policy != "disabled" && (!c.incoming || c.remote.ice.Username != "") {
		c.iceDescription, err = session.GatherICE(c.ctx, settings.ICEServers)
		if err != nil {
			c.cancel()
			session.Close()
			return nil, err
		}
	}
	go func() { <-c.ctx.Done(); c.Close() }()
	return c, nil
}
func (c *Call) LocalSDP(advertisedIP string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.version++
	data := c.sdp(advertisedIP, "", !c.selected)
	if !c.selected {
		c.localOffer = append([]byte(nil), data...)
	}
	return data
}
func (c *Call) sdp(ip string, override string, offer bool) []byte {
	formats := c.formats
	telephone := uint8(101)
	telephoneClock := 8000
	hasTelephone := true
	direction := "sendrecv"
	if c.selected {
		formats = []codec.Format{c.remote.format}
		telephone = c.remote.telephone
		telephoneClock = c.remote.format.ClockRate
		hasTelephone = c.remote.hasTelephone
		if !c.remote.send && !c.remote.receive {
			direction = "inactive"
		} else if !c.remote.send {
			direction = "recvonly"
		} else if !c.remote.receive {
			direction = "sendonly"
		}
	}
	if c.held.Load() {
		direction = "sendonly"
		if c.selected && !c.remote.send {
			direction = "inactive"
		}
	}
	if override != "" {
		direction = override
	}
	if c.iceDescription.Username != "" {
		ip = c.iceDescription.DefaultHost(ip)
	}
	port := c.session.LocalAddr().Port
	if c.iceDescription.Username != "" {
		port = c.iceDescription.DefaultPort(ip, port)
	}
	data := localSDP(c.id, c.version, ip, port, c.session.ControlAddr().Port, formats, telephone, hasTelephone, telephoneClock, direction, c.localKey, c.cryptoTag)
	if len(data) == 0 {
		return nil
	}
	if c.dtls {
		data = bytes.Replace(data, []byte("RTP/AVP"), []byte(c.dtlsProfile), 1)
		setup := c.dtlsSetup
		if offer {
			setup = "actpass"
		}
		data = append(data, []byte("a=fingerprint:"+c.fingerprint+"\r\na=setup:"+setup+"\r\n")...)
		if offer || c.remote.tlsID != "" {
			data = append(data, []byte("a=tls-id:"+c.tlsID+"\r\n")...)
		}
	}
	if !c.selected || c.remote.mux {
		data = append(data, []byte("a=rtcp-mux\r\n")...)
	}
	if c.iceDescription.Username != "" {
		data = append(data, iceAttributes(c.iceDescription)...)
	}
	return data
}
func (c *Call) Connect(remoteSDP []byte) error { return c.connect(remoteSDP, false, false, nil) }

// ConnectEarly receives provisional media without opening the microphone.
func (c *Call) ConnectEarly(remoteSDP []byte) error { return c.connect(remoteSDP, true, false, nil) }
func (c *Call) connect(remoteSDP []byte, early, remoteOffer bool, explicitOffer []byte) (result error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("media call is closed")
	}
	if early && remoteOffer && (!c.early || c.connected) {
		return errors.New("early offers require established early playback")
	}
	c.operations.Add(1)
	defer c.operations.Done()
	defer func() {
		if result == nil && (!early || remoteOffer) && (explicitOffer == nil || bytes.Equal(c.localOffer, explicitOffer)) {
			c.localOffer = nil
		}
	}()
	localOffer := c.localOffer
	if explicitOffer != nil {
		localOffer = explicitOffer
	}
	if !remoteOffer && len(localOffer) > 0 {
		if err := c.validateAnswer(localOffer, remoteSDP); err != nil {
			return err
		}
	}
	var localHeld *bool
	if explicitOffer != nil {
		offered, err := negotiate(explicitOffer, c.formats)
		if err != nil {
			return err
		}
		// A parsed remote description reverses send/receive from our perspective.
		held := !offered.send
		localHeld = &held
	}
	remote, err := negotiate(remoteSDP, c.formats)
	if err != nil {
		return err
	}
	// An answer may list further codecs (RFC 3264 section 6.1). Keep the codec
	// in use while the answer allows it: the peer keeps sending it, and playback
	// decodes only the selected codec.
	if (c.connected || c.early) && !remoteOffer {
		if current, err := negotiate(remoteSDP, []codec.Format{c.remote.format}); err == nil {
			remote = current
		}
	}
	if remote.secure != c.secure || c.dtls != (remote.fingerprint != "") {
		return errors.New("peer changed the negotiated media security profile")
	}
	if err := c.validateDTLS(remote, remoteOffer || (c.incoming && !c.connected && !c.early)); err != nil {
		return err
	}
	if !c.selected {
		offered := false
		for _, format := range c.formats {
			if strings.EqualFold(format.Name, remote.format.Name) && format.ClockRate == remote.format.ClockRate {
				offered = true
			}
		}
		if !offered {
			return errors.New("answer selected a payload mapping that was not offered")
		}
		if remote.secure && !c.dtls && remote.cryptoTag != c.cryptoTag {
			return errors.New("answer selected an SDES tag that was not offered")
		}
	}
	if c.connected && early {
		return nil
	}
	if c.restart != nil {
		return errors.New("ICE restart is in progress")
	}
	if c.connecting {
		return errors.New("media connection is already in progress")
	}
	if c.audioRecovering || c.nativeOpenPending.Load() {
		return errors.New("audio device is opening or recovering")
	}
	active := c.connected || c.early
	changed := active && remote.format != c.remote.format
	if c.selected && !active && remote.format != c.remote.format {
		return errors.New("answer changed the selected codec")
	}
	if active && c.iceDescription.Username != "" && (remote.ice.Username != c.remote.ice.Username || remote.ice.Password != c.remote.ice.Password) {
		return errors.New("ICE restart is not supported during a call")
	}
	if c.deviceRate == 0 {
		c.deviceRate = remote.format.SampleRate
	}
	next := c.pipeline
	if next == nil || changed {
		receivePayloadType := remote.format.PayloadType
		if !remoteOffer && len(localOffer) > 0 {
			if offered, err := negotiate(localOffer, []codec.Format{remote.format}); err == nil {
				receivePayloadType = offered.format.PayloadType
			}
		}
		next, err = newPipeline(c.ctx, remote.format, receivePayloadType, c.deviceRate)
		if err != nil {
			return err
		}
		defer func() {
			if c.pipeline != next {
				next.close()
			}
		}()
	}
	if c.settings.ICEPolicy == "required" && (remote.ice.Username == "" || !remote.mux) {
		return errors.New("peer must support ICE and RTCP multiplexing")
	}
	if c.iceDescription.Username != "" {
		if (c.connected || c.early) && remote.ice.Username == "" {
			return errors.New("removing ICE during a call is not supported")
		}
		if remote.ice.Username != "" && !remote.mux {
			return errors.New("ICE requires RTCP multiplexing")
		}
		c.connecting = true
		c.mu.Unlock()
		if remote.ice.Username != "" {
			err = c.session.ConnectICE(c.ctx, remote.ice, !c.incoming)
		} else {
			c.session.DeclineICE()
		}
		c.mu.Lock()
		c.connecting = false
		if err != nil {
			return err
		}
		if c.closed {
			return errors.New("media call closed during ICE checks")
		}
		if remote.ice.Username == "" {
			c.iceDescription = rtp.ICEDescription{}
		}
	}
	if c.dtls && !active {
		setup := c.dtlsSetup
		if setup == "actpass" {
			setup = "active"
			if remote.setup == "active" {
				setup = "passive"
			}
		}
		if err = c.session.SetRemote(remote.address, remote.address, remote.format.ClockRate); err != nil {
			return err
		}
		c.session.SetRTCPMux(true)
		c.connecting = true
		c.mu.Unlock()
		err = c.session.ConnectDTLS(c.ctx, remote.fingerprint, setup == "active")
		c.mu.Lock()
		c.connecting = false
		if err != nil {
			return err
		}
		if c.closed {
			return errors.New("media call closed during DTLS handshake")
		}
		c.dtlsSetup = setup
	}
	if c.connected || (c.early && early) {
		if changed {
			return c.replacePipeline(remote, next, c.connected, localHeld)
		}
		return c.applyRemote(remote, localHeld)
	}
	if !early && c.processor == nil {
		processor, err := audio.NewProcessor(c.deviceRate, c.settings.EchoCancellation, c.settings.NoiseSuppression)
		if err != nil {
			return err
		}
		c.processor = processor
	}
	opener := c.openAudio
	continuingEarly := c.early
	if early {
		opener = c.openPlayback
	} else if continuingEarly {
		opener = c.openCapture
	}
	c.connecting = true
	c.mu.Unlock()
	opened, err := c.reopenDevices(func() (*audioDevices, error) {
		stream, err := opener(audio.Settings{InputDevice: c.settings.InputDevice, OutputDevice: c.settings.OutputDevice, Backend: c.settings.Backend}, c.deviceRate)
		if err != nil {
			return nil, err
		}
		return &audioDevices{playback: stream}, nil
	})
	c.mu.Lock()
	c.connecting = false
	if err != nil {
		return err
	}
	stream := opened.playback
	if c.closed {
		c.disposeDevices(opened)
		return errors.New("media call closed while opening audio")
	}
	if continuingEarly && changed {
		c.captureStream = stream
		c.devices.Store(&audioDevices{capture: stream, playback: c.stream})
		if err = c.replacePipeline(remote, next, true, localHeld); err != nil {
			return err
		}
		c.connected = true
		c.early = false
		return nil
	}
	if err = c.applyRemote(remote, localHeld); err != nil {
		c.disposeDevices(opened)
		return err
	}
	c.pipeline = next
	c.connected = !early
	c.early = early
	if continuingEarly {
		c.captureStream = stream
		c.devices.Store(&audioDevices{capture: stream, playback: c.stream})
		c.startRecovery()
		c.startPipeline(next, true, false)
		return nil
	}
	c.stream = stream
	if !early {
		c.captureStream = stream
	}
	c.devices.Store(&audioDevices{capture: c.captureStream, playback: stream})
	c.startRecovery()
	c.startPipeline(next, !early, true)
	return nil
}
func (c *Call) AnswerOffer(remote []byte, advertisedIP string) ([]byte, error) {
	c.mu.Lock()
	parsed, err := negotiate(remote, c.formats)
	restart := err == nil && c.connected && c.iceDescription.Username != "" && (parsed.ice.Username != c.remote.ice.Username || parsed.ice.Password != c.remote.ice.Password)
	c.mu.Unlock()
	if restart {
		return c.prepareRestart(c.ctx, advertisedIP, &parsed)
	}
	if err := c.connect(remote, false, true, nil); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.version++
	return c.sdp(advertisedIP, "", false), nil
}

// AnswerEarlyOffer renegotiates existing provisional playback without opening capture.
// Signaling must preserve this negotiation when the INVITE receives its final answer.
func (c *Call) AnswerEarlyOffer(remote []byte, advertisedIP string) ([]byte, error) {
	if err := c.connect(remote, true, true, nil); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.version++
	return c.sdp(advertisedIP, "", false), nil
}
func (c *Call) SetMuted(muted bool) { c.muted.Store(muted) }
func (c *Call) SetHeld(held bool)   { c.held.Store(held) }
func (c *Call) SendDTMF(digit string) error {
	if len(digit) != 1 {
		return errors.New("DTMF requires one digit")
	}
	const digits = "0123456789*#ABCD"
	index := strings.IndexByte(digits, strings.ToUpper(digit)[0])
	if index < 0 {
		return errors.New("invalid DTMF digit")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected || c.closed || c.held.Load() {
		return errors.New("DTMF requires an active, unheld call")
	}
	if !c.remote.hasTelephone || c.remote.events&(1<<uint(index)) == 0 {
		return ErrTelephoneEventsNotNegotiated
	}
	select {
	case c.dtmf <- byte(index):
		return nil
	default:
		return errors.New("DTMF queue is full")
	}
}
func (c *Call) setError(err error) { c.mu.Lock(); c.lastError = err.Error(); c.mu.Unlock() }
func (c *Call) capture(p *mediaPipeline) {
	defer p.workers.Done()
	defer c.workers.Done()
	coder, format := p.coder, p.format
	adaptive, _ := coder.(codec.LossAdaptiveEncoder)
	if adaptive != nil {
		p.encoderBitrate.Store(int64(adaptive.EncoderBitrate()))
	}
	var feedbackAt, checkedAt time.Time
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	raw := make([]byte, p.deviceRate/50*2)
	pcm := make([]int16, len(raw)/2)
	referenceRaw := make([]byte, len(raw))
	reference := make([]int16, len(pcm))
	previousDevices := c.devices.Load()
	if previousDevices.playback.Reference != nil {
		previousDevices.playback.Reference.Discard()
	}
	var event byte
	eventFrame := 0
	firstPacket := true
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			devices := c.devices.Load()
			if devices != previousDevices {
				c.processor.Reset()
				if leg := c.conference.Load(); leg != nil {
					if err := leg.attachReferences(devices.playback); err != nil {
						c.setError(err)
					}
				}
				if devices.playback.Reference != nil {
					devices.playback.Reference.Discard()
				}
				previousDevices = devices
			}
			n := devices.capture.Capture.Read(raw)
			clear(raw[n:])
			clear(referenceRaw)
			if devices.playback.Reference != nil {
				devices.playback.Reference.Read(referenceRaw)
			}
			for i := range pcm {
				pcm[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
				reference[i] = int16(binary.LittleEndian.Uint16(referenceRaw[i*2:]))
			}
			if leg := c.conference.Load(); leg != nil {
				if err := leg.mixReference(reference); err != nil {
					c.setError(err)
					continue
				}
			}
			if err := c.processor.Process(pcm, reference); err != nil {
				c.setError(err)
				continue
			}
			if !c.sendEnabled.Load() || c.held.Load() {
				c.session.Advance(uint32(format.ClockRate / 50))
				continue
			}
			if eventFrame == 0 {
				select {
				case event = <-c.dtmf:
					eventFrame = 1
				default:
				}
			}
			if eventFrame > 0 {
				c.mu.Lock()
				pt := c.remote.telephone
				c.mu.Unlock()
				duration := min(eventFrame, 6) * format.ClockRate / 50
				flags := byte(10)
				if eventFrame >= 6 {
					flags |= 0x80
				}
				payload := []byte{event, flags, byte(duration >> 8), byte(duration)}
				step := uint32(0)
				if eventFrame == 8 {
					step = uint32(format.ClockRate * 160 / 1000)
				}
				if err := c.session.Send(pt, payload, step, eventFrame == 1); err != nil {
					c.setError(err)
				}
				eventFrame++
				if eventFrame > 8 {
					eventFrame = 0
				}
				continue
			}
			applyGain(pcm, math.Float64frombits(c.inputGain.Load()))
			if c.muted.Load() {
				clear(pcm)
			}
			if leg := c.conference.Load(); leg != nil {
				leg.mix(pcm)
			}
			if recording := c.recording.Load(); recording != nil {
				recording.Capture(pcm)
			}
			encoded, err := p.captureConverter.Process(pcm)
			if err != nil {
				c.setError(err)
				continue
			}
			if adaptive != nil && time.Since(checkedAt) >= time.Second {
				checkedAt = time.Now()
				report := c.session.Stats()
				if report.ReceiverReportAt.After(feedbackAt) && time.Since(report.ReceiverReportAt) < 15*time.Second && (feedbackAt.IsZero() || report.ReceiverReportAt.Sub(feedbackAt) >= 5*time.Second) {
					feedbackAt = report.ReceiverReportAt
					bitrate, err := adaptive.SetPacketLoss((int(report.RemoteFractionLost)*100 + 128) / 256)
					if err != nil {
						c.setError(err)
					}
					p.encoderBitrate.Store(int64(bitrate))
				}
			}
			payload, err := coder.Encode(encoded)
			if err != nil {
				c.setError(err)
				continue
			}
			if err = c.session.Send(format.PayloadType, payload, uint32(format.ClockRate/50), firstPacket); err != nil {
				c.setError(err)
			}
			firstPacket = false
		}
	}
}
func (c *Call) playback(p *mediaPipeline) {
	defer p.workers.Done()
	defer c.workers.Done()
	coder, format := p.coder, p.format
	isAudio := func(payloadType uint8) bool {
		return payloadType == format.PayloadType || payloadType == p.receivePayloadType
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	buffer := rtp.NewJitterBuffer(3)
	var source uint32
	var hasSource bool
	lastSamples := format.SampleRate / 50
	raw := make([]byte, p.deviceRate*2*120/1000)
	for {
		select {
		case <-p.ctx.Done():
			return
		case packet, ok := <-c.session.Packets():
			if !ok {
				return
			}
			if !hasSource || packet.SSRC != source {
				buffer = rtp.NewJitterBuffer(3)
				source = packet.SSRC
				hasSource = true
			}
			// Consume telephone-event sequence numbers too, without decoding their payload.
			c.mu.Lock()
			telephone := c.remote.telephone
			hasTelephone := c.remote.hasTelephone
			c.mu.Unlock()
			if isAudio(packet.PayloadType) || (hasTelephone && packet.PayloadType == telephone) {
				buffer.Push(packet)
			}
		case <-ticker.C:
			lost := buffer.Lost
			packet := buffer.Pop()
			if !c.receiveEnabled.Load() || c.held.Load() {
				continue
			}
			var pcm []int16
			var err error
			if packet == nil {
				if buffer.Lost == lost {
					continue
				}
				var nextPayload []byte
				if next := buffer.Peek(); next != nil && isAudio(next.PayloadType) {
					nextPayload = next.Payload
				}
				pcm, err = coder.Conceal(lastSamples, nextPayload)
			} else {
				if !isAudio(packet.PayloadType) {
					continue
				}
				pcm, err = coder.Decode(packet.Payload)
				if err == nil {
					lastSamples = len(pcm)
				}
			}
			if err != nil {
				c.setError(err)
				continue
			}
			if len(pcm) > format.SampleRate*120/1000 {
				c.setError(fmt.Errorf("decoded frame exceeds 120 ms"))
				continue
			}
			pcm, err = p.playbackConverter.Process(pcm)
			if err != nil {
				c.setError(err)
				continue
			}
			if len(pcm)*2 > len(raw) {
				c.setError(errors.New("resampled frame exceeds device buffer"))
				continue
			}
			if recording := c.recording.Load(); recording != nil {
				recording.Playback(pcm)
			}
			if leg := c.conference.Load(); leg != nil {
				if err := leg.forward(pcm); err != nil {
					c.setError(err)
				}
			}
			applyGain(pcm, math.Float64frombits(c.outputGain.Load()))
			for i, sample := range pcm {
				binary.LittleEndian.PutUint16(raw[i*2:], uint16(sample))
			}
			c.devices.Load().playback.Playback.Write(raw[:len(pcm)*2])
		}
	}
}
func (c *Call) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	transport := c.session.Stats()
	result := Stats{ICERestartPending: c.restart != nil, CleanupPending: c.closed && !c.cleanupComplete, ICEState: transport.ICEState, ICECandidateType: transport.ICECandidateType, RTCPMux: transport.RTCPMux, AudioRecoveryPending: c.nativeOpenPending.Load(), AudioRecovering: c.audioRecovering, AudioRecoveryAttempts: c.audioRecoveryAttempts, AudioRecoveryError: c.audioRecoveryError, DSPAvailable: audio.DSPAvailable(), EchoCancellation: c.connected && c.processor != nil && c.settings.EchoCancellation, NoiseSuppression: c.connected && c.processor != nil && c.settings.NoiseSuppression, EarlyMedia: c.early, Conference: c.conference.Load() != nil, InputGain: math.Float64frombits(c.inputGain.Load()), OutputGain: math.Float64frombits(c.outputGain.Load()), Codec: c.remote.format.Name, SampleRate: c.remote.format.SampleRate, DeviceSampleRate: c.deviceRate, ClockRate: c.remote.format.ClockRate, PacketizationMilliseconds: 20, Transport: "RTP/AVP", LocalAddress: transport.LocalAddress, RemoteAddress: transport.RemoteAddress, PacketsSent: transport.PacketsSent, PacketsReceived: transport.PacketsReceived, PacketsLost: transport.PacketsLost, PacketsDropped: transport.PacketsDropped, BytesSent: transport.BytesSent, BytesReceived: transport.BytesReceived, JitterMilliseconds: transport.JitterMilliseconds, RTTMilliseconds: transport.RTTMilliseconds, BufferMilliseconds: 60, Muted: c.muted.Load(), Held: c.held.Load(), LastError: c.lastError}
	if c.restart != nil {
		result.ICEState = "restarting"
	}
	if recording := c.recording.Load(); recording != nil {
		result.Recording = true
		result.RecordingPath = recording.Path
	}
	if transport.DTLSError != "" {
		result.LastError = transport.DTLSError
	}
	if c.secure {
		result.Encrypted = transport.Encrypted
		result.Transport = "RTP/SAVP"
		if c.dtls {
			result.Transport = c.dtlsProfile
		}
	}
	if c.stream != nil {
		stats := c.stream.Stats()
		result.AudioStopped = stats.Stopped
		if stats.Stopped && !c.closed && result.LastError == "" {
			result.LastError = "audio device stopped; reconnect or select an available device"
		}
		if c.captureStream != nil {
			input := c.captureStream.Stats()
			result.InputLevel = input.InputLevel
			result.CaptureOverruns = input.CaptureOverruns
			result.AudioStopped = result.AudioStopped || input.Stopped
		}
		result.OutputLevel = stats.OutputLevel
		result.PlaybackUnderruns = stats.PlaybackUnderruns
	}
	if result.AudioStopped || c.audioRecovering || c.closed {
		result.EchoCancellation = false
		result.NoiseSuppression = false
	}
	c.diagnoseMedia(&result, transport, time.Now())
	if c.pipeline != nil {
		result.EncoderBitrate = int(c.pipeline.encoderBitrate.Load())
	}
	if !transport.ReceiverReportAt.IsZero() {
		result.ReceiverReportAvailable = true
		result.RemotePacketLossPercent = float64(transport.RemoteFractionLost) * 100 / 256
		result.ReceiverReportAgeSeconds = time.Since(transport.ReceiverReportAt).Seconds()
	}
	return result
}

// Close bounds the caller's wait; native resources remain owned until cleanup completes.
func (c *Call) Close() error {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		conference := c.conference.Swap(nil)
		devices := &audioDevices{capture: c.captureStream, playback: c.stream}
		c.cancel()
		go c.cleanup(conference, devices)
	}
	done, timeout := c.closedDone, c.closeTimeout
	c.mu.Unlock()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		c.mu.Lock()
		err := c.closeErr
		c.mu.Unlock()
		return err
	case <-timer.C:
		return ErrCleanupPending
	}
}
func (c *Call) cleanup(conference *conferenceLeg, devices *audioDevices) {
	if conference != nil {
		conference.close()
	}
	c.session.Close()
	c.workers.Wait()
	c.operations.Wait()
	if c.pipeline != nil {
		c.pipeline.close()
	}
	if c.processor != nil {
		c.processor.Close()
	}
	err := errors.Join(c.StopRecording(), c.closeDevices(devices))
	c.mu.Lock()
	c.closeErr = errors.Join(c.closeErr, err)
	if c.closeErr != nil {
		c.lastError = c.closeErr.Error()
	}
	c.cleanupComplete = true
	close(c.closedDone)
	c.mu.Unlock()
}
func (c *Call) closeDevices(devices *audioDevices) error {
	var err error
	if devices.capture != nil && devices.capture != devices.playback {
		err = c.closeAudio(devices.capture)
	}
	if devices.playback != nil {
		err = errors.Join(err, c.closeAudio(devices.playback))
	}
	return err
}

// The caller already owns a registered operation or worker until disposal is queued.
func (c *Call) disposeDevices(devices *audioDevices) {
	c.operations.Add(1)
	go func() {
		defer c.operations.Done()
		if err := c.closeDevices(devices); err != nil {
			c.mu.Lock()
			c.closeErr = errors.Join(c.closeErr, err)
			c.mu.Unlock()
		}
	}()
}

func StartRinging(ctx context.Context, settings Settings) (func(), error) {
	output := settings.RingerDevice
	if output == "" {
		output = settings.OutputDevice
	}
	return audio.StartRinging(ctx, audio.Settings{Backend: settings.Backend, OutputDevice: output})
}
func TestAudio(ctx context.Context, settings Settings, mode string, duration time.Duration) (audio.TestResult, error) {
	return audio.Test(ctx, audio.Settings{Backend: settings.Backend, InputDevice: settings.InputDevice, OutputDevice: settings.OutputDevice}, mode, duration)
}

func (c *Call) SetGain(input, output float64) error {
	if math.IsNaN(input) || math.IsNaN(output) || input < 0 || output < 0 || input > 2 || output > 2 {
		return errors.New("audio gain must be between 0 and 2")
	}
	c.inputGain.Store(math.Float64bits(input))
	c.outputGain.Store(math.Float64bits(output))
	return nil
}
func applyGain(pcm []int16, gain float64) {
	if gain == 1 {
		return
	}
	for i, sample := range pcm {
		pcm[i] = int16(max(-32768, min(32767, float64(sample)*gain)))
	}
}

// LocalOffer starts a new offer independently of the peer's previous direction.
func (c *Call) LocalOffer(advertisedIP string, held bool) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.restart != nil {
		return nil
	}
	c.version++
	direction := "sendrecv"
	if held {
		direction = "sendonly"
	}
	data := c.sdp(advertisedIP, direction, true)
	c.localOffer = append([]byte(nil), data...)
	return data
}

func StartRingback(ctx context.Context, settings Settings) (func(), error) {
	return audio.StartRingback(ctx, audio.Settings{Backend: settings.Backend, OutputDevice: settings.OutputDevice})
}
