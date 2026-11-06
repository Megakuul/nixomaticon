package media

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/megakuul/voiper/pkg/codec"
	"github.com/megakuul/voiper/pkg/rtp"
	"github.com/pion/sdp/v3"
)

type description struct {
	fingerprint, setup, dtlsProfile string
	tlsID, fingerprintSet           string
	payloads                        map[uint8]bool
	mappings                        map[uint8]string
	ice                             rtp.ICEDescription
	mux                             bool
	events                          uint16
	secure                          bool
	key                             []byte
	cryptoTag                       string
	format                          codec.Format
	address, control                *net.UDPAddr
	telephone                       uint8
	hasTelephone                    bool
	send, receive                   bool
}

func negotiate(data []byte, formats []codec.Format) (description, error) {
	result := description{send: true, receive: true, events: 0xffff}
	if len(data) == 0 || len(data) > 65536 {
		return result, errors.New("missing or oversized SDP")
	}
	var session sdp.SessionDescription
	if err := session.Unmarshal(data); err != nil {
		return result, errors.New("invalid SDP session description")
	}
	if len(session.MediaDescriptions) != 1 {
		return result, errors.New("SDP must contain exactly one audio media section")
	}
	var media *sdp.MediaDescription
	for _, candidate := range session.MediaDescriptions {
		if candidate.MediaName.Media == "audio" && candidate.MediaName.Port.Value != 0 {
			if media != nil {
				return result, errors.New("multiple active audio streams are not supported")
			}
			media = candidate
		} else if candidate.MediaName.Port.Value != 0 {
			return result, errors.New("only audio media is supported")
		}
	}
	if media == nil {
		return result, errors.New("remote SDP has no active audio stream")
	}
	if media.MediaName.Port.Range != nil && *media.MediaName.Port.Range != 1 {
		return result, errors.New("multiple RTP ports are unsupported")
	}
	profile := strings.Join(media.MediaName.Protos, "/")
	dtlsProfile := profile == "UDP/TLS/RTP/SAVP" || profile == "UDP/TLS/RTP/SAVPF"
	if profile != "RTP/AVP" && profile != "RTP/SAVP" && !dtlsProfile {
		return result, errors.New("unsupported media transport profile")
	}
	if profile == "RTP/SAVP" {
		result.secure = true
		for _, attribute := range media.Attributes {
			if attribute.Key != "crypto" {
				continue
			}
			fields := strings.Fields(attribute.Value)
			if len(fields) != 3 || fields[1] != "AES_CM_128_HMAC_SHA1_80" || !strings.HasPrefix(fields[2], "inline:") {
				continue
			}
			if _, err := strconv.ParseUint(fields[0], 10, 31); err != nil {
				continue
			}
			key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(fields[2], "inline:"))
			if err != nil || len(key) != 30 {
				continue
			}
			result.key = key
			result.cryptoTag = fields[0]
			break
		}
		if len(result.key) == 0 {
			return result, errors.New("no supported SDES-SRTP key exchange")
		}
	}
	conn := media.ConnectionInformation
	if conn == nil {
		conn = session.ConnectionInformation
	}
	if conn == nil || conn.Address == nil {
		return result, errors.New("SDP has no media connection address")
	}
	ip := net.ParseIP(conn.Address.Address)
	if ip == nil || ip.IsMulticast() {
		return result, errors.New("SDP media address must be a unicast IP")
	}
	port := media.MediaName.Port.Value
	if port < 1 || port > 65534 {
		return result, errors.New("invalid SDP RTP port")
	}
	result.address = &net.UDPAddr{IP: ip, Port: port}
	result.control = &net.UDPAddr{IP: ip, Port: port + 1}
	if value, ok := media.Attribute("rtcp"); ok {
		fields := strings.Fields(value)
		if len(fields) != 1 && len(fields) != 4 {
			return result, errors.New("invalid RTCP attribute")
		}
		p, err := strconv.Atoi(fields[0])
		if err != nil || p < 1 || p > 65535 {
			return result, errors.New("invalid RTCP port")
		}
		result.control.Port = p
		if len(fields) == 4 {
			rtcpIP := net.ParseIP(fields[3])
			if rtcpIP == nil || rtcpIP.IsMulticast() {
				return result, errors.New("invalid RTCP address")
			}
			result.control.IP = rtcpIP
		}
	}
	result.mux = false
	if _, ok := media.Attribute("rtcp-mux"); ok {
		result.mux = true
	}
	attribute := func(key string) string {
		if value, ok := media.Attribute(key); ok {
			return value
		}
		value, _ := session.Attribute(key)
		return value
	}
	if dtlsProfile {
		fingerprints := session.Attributes
		for _, attr := range media.Attributes {
			if attr.Key == "fingerprint" {
				fingerprints = media.Attributes
				break
			}
		}
		var fingerprintSet []string
		for _, attr := range fingerprints {
			if attr.Key != "fingerprint" {
				continue
			}
			fields := strings.Fields(attr.Value)
			if len(fields) != 2 {
				return result, errors.New("invalid DTLS fingerprint attribute")
			}
			fingerprintSet = append(fingerprintSet, strings.ToLower(fields[0])+" "+strings.ToUpper(fields[1]))
			if !strings.EqualFold(fields[0], "sha-256") {
				continue
			}
			if result.fingerprint != "" {
				return result, errors.New("ambiguous SHA-256 DTLS fingerprints")
			}
			var err error
			result.fingerprint, err = rtp.NormalizeDTLSFingerprint(attr.Value)
			if err != nil {
				return result, err
			}
		}
		var err error
		result.fingerprint, err = rtp.NormalizeDTLSFingerprint(result.fingerprint)
		if err != nil {
			return result, err
		}
		sort.Strings(fingerprintSet)
		result.fingerprintSet = strings.Join(fingerprintSet, "\n")
		result.tlsID, _ = media.Attribute("tls-id")
		if result.tlsID != "" {
			if len(result.tlsID) < 20 || len(result.tlsID) > 255 {
				return result, errors.New("invalid DTLS tls-id length")
			}
			for _, value := range result.tlsID {
				if !((value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') || (value >= '0' && value <= '9') || strings.ContainsRune("+/-_", value)) {
					return result, errors.New("invalid DTLS tls-id character")
				}
			}
		}
		setupAttributes := session.Attributes
		for _, attr := range media.Attributes {
			if attr.Key == "setup" {
				setupAttributes = media.Attributes
				break
			}
		}
		setupCount := 0
		for _, attr := range setupAttributes {
			if attr.Key == "setup" {
				setupCount++
			}
		}
		tlsIDCount := 0
		for _, attr := range media.Attributes {
			if attr.Key == "tls-id" {
				tlsIDCount++
			}
		}
		if setupCount != 1 || tlsIDCount > 1 {
			return result, errors.New("ambiguous DTLS setup or association identity")
		}
		result.setup = attribute("setup")
		if result.setup != "actpass" && result.setup != "active" && result.setup != "passive" {
			return result, errors.New("DTLS requires active, passive or actpass setup")
		}
		if !result.mux {
			return result, errors.New("DTLS-SRTP requires RTCP multiplexing")
		}
		for _, attr := range media.Attributes {
			if attr.Key == "crypto" {
				return result, errors.New("DTLS-SRTP cannot include SDES keys")
			}
		}
		result.secure = true
		result.dtlsProfile = profile
	}
	_, result.ice.Lite = session.Attribute("ice-lite")
	result.ice.Username = attribute("ice-ufrag")
	result.ice.Password = attribute("ice-pwd")
	for _, attr := range media.Attributes {
		if attr.Key == "candidate" {
			result.ice.Candidates = append(result.ice.Candidates, attr.Value)
		}
	}
	if result.ice.Lite || result.ice.Username != "" || result.ice.Password != "" || len(result.ice.Candidates) > 0 {
		if len(result.ice.Username) < 4 || len(result.ice.Username) > 256 || len(result.ice.Password) < 22 || len(result.ice.Password) > 256 || len(result.ice.Candidates) == 0 || len(result.ice.Candidates) > 64 {
			return result, errors.New("incomplete or oversized ICE description")
		}
	}
	directions := session.Attributes
	hasMediaDirection := false
	for _, attribute := range media.Attributes {
		switch attribute.Key {
		case "sendrecv", "sendonly", "recvonly", "inactive":
			hasMediaDirection = true
		}
	}
	if hasMediaDirection {
		directions = media.Attributes
	}
	for _, attribute := range directions {
		switch attribute.Key {
		case "sendonly":
			result.send = false
		case "recvonly":
			result.receive = false
		case "inactive":
			result.send = false
			result.receive = false
		}
	}
	if ip.IsUnspecified() {
		result.send = false
		result.receive = false
	}
	if value, ok := media.Attribute("maxptime"); ok {
		maximum, err := strconv.ParseFloat(value, 64)
		if err != nil || maximum < 20 {
			return result, errors.New("peer cannot accept 20 ms audio frames")
		}
	}
	parameters := make(map[uint8]string)
	for _, attribute := range media.Attributes {
		if attribute.Key == "fmtp" {
			parts := strings.SplitN(attribute.Value, " ", 2)
			if len(parts) == 2 {
				pt, err := strconv.ParseUint(parts[0], 10, 7)
				if err == nil {
					parameters[uint8(pt)] = parts[1]
				}
			}
		}
	}
	mapping := map[uint8]string{0: "PCMU/8000", 8: "PCMA/8000", 9: "G722/8000"}
	for _, attribute := range media.Attributes {
		if attribute.Key != "rtpmap" {
			continue
		}
		fields := strings.Fields(attribute.Value)
		if len(fields) != 2 {
			return result, errors.New("invalid rtpmap")
		}
		pt, err := strconv.ParseUint(fields[0], 10, 7)
		if err != nil {
			return result, errors.New("invalid RTP payload type")
		}
		// Some endpoints describe G.722 by its 16 kHz sample rate instead of
		// the 8000 RTP clock rate that RFC 3551 assigns for compatibility.
		if pt == 9 && strings.EqualFold(fields[1], "G722/16000") {
			fields[1] = "G722/8000"
		}
		if pt == 0 || pt == 8 || pt == 9 {
			expected := mapping[uint8(pt)]
			if !strings.EqualFold(fields[1], expected) && !strings.EqualFold(fields[1], expected+"/1") {
				return result, errors.New("SDP remaps a static audio payload type")
			}
		}
		mapping[uint8(pt)] = fields[1]
	}
	offered := map[uint8]bool{}
	for _, value := range media.MediaName.Formats {
		pt, err := strconv.ParseUint(value, 10, 7)
		if err != nil {
			return result, errors.New("invalid SDP payload type")
		}
		if result.mux && pt >= 64 && pt <= 95 {
			return result, errors.New("RTP payload type conflicts with RTCP multiplexing")
		}
		offered[uint8(pt)] = true
	}
	result.payloads = offered
	result.mappings = mapping
	for _, format := range formats {
		for _, value := range media.MediaName.Formats {
			pt, _ := strconv.ParseUint(value, 10, 7)
			parts := strings.Split(mapping[uint8(pt)], "/")
			if len(parts) < 2 || !strings.EqualFold(parts[0], format.Name) {
				continue
			}
			rate, err := strconv.Atoi(parts[1])
			if err != nil || rate != format.ClockRate {
				continue
			}
			channels := 1
			if len(parts) > 2 {
				channels, _ = strconv.Atoi(parts[2])
			}
			if channels != format.Channels {
				continue
			}
			format.PayloadType = uint8(pt)
			if strings.EqualFold(format.Name, "opus") {
				unsupported := false
				for _, parameter := range strings.Split(parameters[uint8(pt)], ";") {
					pair := strings.SplitN(strings.TrimSpace(parameter), "=", 2)
					if len(pair) != 2 {
						continue
					}
					value, _ := strconv.Atoi(pair[1])
					switch pair[0] {
					case "maxaveragebitrate":
						if value >= 6000 && value <= 510000 {
							format.Bitrate = value
						}
					case "maxplaybackrate":
						if value >= 8000 && value <= 48000 {
							format.MaxPlaybackRate = value
						}
					case "useinbandfec":
						format.UseFEC = value == 1
					case "minptime":
						if value > 20 {
							unsupported = true
						}
					case "cbr":
						if value == 1 {
							unsupported = true
						}
					}
				}
				if unsupported {
					continue
				}
			}
			for eventPT, rtpmap := range mapping {
				if offered[eventPT] && strings.EqualFold(rtpmap, fmt.Sprintf("telephone-event/%d", format.ClockRate)) {
					result.telephone = eventPT
					result.hasTelephone = true
					if events, ok := parameters[eventPT]; ok {
						result.events = parseEvents(events)
						if result.events == 0 {
							result.hasTelephone = false
						}
					}
					break
				}
			}
			result.format = format
			return result, nil
		}
	}
	return result, errors.New("no mutually supported audio codec")
}

func localSDP(id uint64, version uint64, ip string, port, control int, formats []codec.Format, telephone uint8, hasTelephone bool, telephoneClock int, direction string, key []byte, cryptoTag string) []byte {
	address := net.ParseIP(ip)
	if address == nil || address.IsUnspecified() || address.IsMulticast() {
		return nil
	}
	family := "IP4"
	if address.To4() == nil {
		family = "IP6"
	}
	var out strings.Builder
	profile := "RTP/AVP"
	if len(key) > 0 {
		profile = "RTP/SAVP"
	}
	fmt.Fprintf(&out, "v=0\r\no=- %d %d IN %s %s\r\ns=Voiper\r\nc=IN %s %s\r\nt=0 0\r\nm=audio %d %s", id, version, family, address.String(), family, address.String(), port, profile)
	for _, format := range formats {
		fmt.Fprintf(&out, " %d", format.PayloadType)
	}
	extraTelephone := false
	if hasTelephone {
		fmt.Fprintf(&out, " %d", telephone)
		for _, format := range formats {
			if telephoneClock == 8000 && format.ClockRate == 48000 {
				extraTelephone = true
			}
		}
		if extraTelephone {
			out.WriteString(" 102")
		}
	}
	fmt.Fprintf(&out, "\r\na=rtcp:%d IN %s %s\r\n", control, family, address.String())
	if len(key) > 0 {
		fmt.Fprintf(&out, "a=crypto:%s AES_CM_128_HMAC_SHA1_80 inline:%s\r\n", cryptoTag, base64.StdEncoding.EncodeToString(key))
	}
	for _, format := range formats {
		fmt.Fprintf(&out, "a=rtpmap:%d %s/%d", format.PayloadType, format.Name, format.ClockRate)
		if format.Channels > 1 {
			fmt.Fprintf(&out, "/%d", format.Channels)
		}
		out.WriteString("\r\n")
		if strings.EqualFold(format.Name, "opus") {
			fmt.Fprintf(&out, "a=fmtp:%d minptime=10;useinbandfec=1;stereo=0;sprop-stereo=0\r\n", format.PayloadType)
		}
	}
	if hasTelephone {
		fmt.Fprintf(&out, "a=rtpmap:%d telephone-event/%d\r\na=fmtp:%d 0-15\r\n", telephone, telephoneClock, telephone)
		if extraTelephone {
			out.WriteString("a=rtpmap:102 telephone-event/48000\r\na=fmtp:102 0-15\r\n")
		}
	}
	fmt.Fprintf(&out, "a=ptime:20\r\na=%s\r\n", direction)
	return []byte(out.String())
}

func parseEvents(value string) uint16 {
	var mask uint16
	for _, entry := range strings.Split(value, ",") {
		bounds := strings.SplitN(strings.TrimSpace(entry), "-", 2)
		low, err := strconv.Atoi(bounds[0])
		if err != nil || low < 0 || low > 16 {
			continue
		}
		high := low
		if len(bounds) == 2 {
			high, err = strconv.Atoi(bounds[1])
			if err != nil || high < low || high > 255 {
				continue
			}
		}
		for event := low; event <= min(high, 15); event++ {
			mask |= 1 << event
		}
	}
	return mask
}

func iceAttributes(description rtp.ICEDescription) []byte {
	var out strings.Builder
	fmt.Fprintf(&out, "a=ice-ufrag:%s\r\na=ice-pwd:%s\r\n", description.Username, description.Password)
	for _, candidate := range description.Candidates {
		fmt.Fprintf(&out, "a=candidate:%s\r\n", candidate)
	}
	out.WriteString("a=end-of-candidates\r\n")
	return []byte(out.String())
}
