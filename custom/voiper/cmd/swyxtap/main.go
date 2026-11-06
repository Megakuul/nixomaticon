// Command swyxtap is a development-only man-in-the-middle for reverse
// engineering the SwyxWare client protocol. Point SwyxIt! at the host running
// this tool instead of the real SwyxServer; every configured port is forwarded
// to the real server, and the WCF channel (net.tcp, TCP 9094) is intercepted:
// the tool parses the .NET Message Framing (MC-NMF) preamble, relays the
// "application/ssl-tls" upgrade, terminates the TLS toward the client, opens a
// fresh TLS session to the real server, and logs the decrypted framing records
// (the binary SOAP envelopes) in both directions.
//
// It is a plaintext recorder, not part of the shipped application. It only
// works while the client does not validate the server certificate (SwyxWare
// ships a self-signed "IpPbx" certificate, which SwyxIt! accepts), and while
// the server does not reject the session over a mismatched WS-Addressing "To"
// header. If either does not hold, see the notes in cmd/swyxtap/README.md.
//
//	go run ./cmd/swyxtap -listen 192.168.124.1 -upstream 10.1.1.17 -log ./swyxtap-logs
//
// SwyxWare's self-signed certificate uses a negative serial number, which Go
// rejects since 1.23; re-allow it so the tap can connect to the real server.
//
//go:debug x509negativeserial=1
package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	listen := flag.String("listen", "0.0.0.0", "address to bind the forwarded ports on (the address SwyxIt! connects to)")
	upstream := flag.String("upstream", "10.1.1.17", "the real SwyxServer to forward to")
	ports := flag.String("ports", "9094,5060,9100,9090", "comma-separated TCP ports to forward")
	tlsPorts := flag.String("tls-ports", "9094", "comma-separated ports to intercept as WCF net.tcp (TLS upgrade)")
	logDir := flag.String("log", "swyxtap-logs", "directory for decrypted traffic logs")
	sni := flag.String("sni", "IpPbx", "TLS SNI/server name to send to the real server")
	certCN := flag.String("cert-cn", "IpPbx", "common name of the certificate presented to the client")
	flag.Parse()

	if err := os.MkdirAll(*logDir, 0o755); err != nil {
		log.Fatalf("log dir: %v", err)
	}
	cert, err := selfSigned(*certCN)
	if err != nil {
		log.Fatalf("certificate: %v", err)
	}
	tlsSet := map[int]bool{}
	for _, p := range splitPorts(*tlsPorts) {
		tlsSet[p] = true
	}

	p := &proxy{
		upstream:  *upstream,
		logDir:    *logDir,
		sni:       *sni,
		clientTLS: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
	}

	var wg sync.WaitGroup
	for _, port := range splitPorts(*ports) {
		addr := net.JoinHostPort(*listen, strconv.Itoa(port))
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			log.Fatalf("listen %s: %v", addr, err)
		}
		mode := "passthrough"
		if tlsSet[port] {
			mode = "WCF/TLS intercept"
		}
		log.Printf("listening on %s -> %s:%d (%s)", addr, *upstream, port, mode)
		wg.Add(1)
		go func(ln net.Listener, port int, intercept bool) {
			defer wg.Done()
			for {
				conn, err := ln.Accept()
				if err != nil {
					log.Printf("accept %d: %v", port, err)
					return
				}
				go p.handle(conn, port, intercept)
			}
		}(ln, port, tlsSet[port])
	}
	log.Printf("point the SwyxIt! server address at %s; logs in %s", *listen, *logDir)
	wg.Wait()
}

type proxy struct {
	upstream  string
	logDir    string
	sni       string
	clientTLS *tls.Config
	conns     atomic.Int64
}

func (p *proxy) handle(client net.Conn, port int, intercept bool) {
	defer client.Close()
	id := p.conns.Add(1)
	server, err := net.DialTimeout("tcp", net.JoinHostPort(p.upstream, strconv.Itoa(port)), 10*time.Second)
	if err != nil {
		log.Printf("[%d] dial upstream :%d: %v", id, port, err)
		return
	}
	defer server.Close()

	if !intercept {
		pipe(client, server)
		return
	}
	if err := p.interceptWCF(id, client, server); err != nil && !errors.Is(err, io.EOF) {
		log.Printf("[%d] wcf: %v", id, err)
	}
}

// interceptWCF relays the MC-NMF preamble (rewriting the Via host to the real
// server), performs the TLS upgrade on both sides, and then logs the decrypted
// framing records flowing through.
func (p *proxy) interceptWCF(id int64, client, server net.Conn) error {
	cr := bufio.NewReader(client)
	sr := bufio.NewReader(server)
	via := ""
	for {
		rec, err := readPreambleRecord(cr)
		if err != nil {
			return fmt.Errorf("read preamble: %w", err)
		}
		switch rec.kind {
		case recVia:
			via = rec.text
			rewritten := p.rewriteVia(rec.text)
			if _, err := server.Write(encodeString(recVia, rewritten)); err != nil {
				return err
			}
		case recUpgradeRequest:
			if _, err := server.Write(rec.raw); err != nil {
				return err
			}
			if rec.text != "application/ssl-tls" {
				// Some configurations negotiate SPNEGO first; we cannot
				// decrypt that, so fall back to a transparent relay and let
				// the operator know the channel is not TLS-only.
				log.Printf("[%d] unexpected upgrade %q; relaying raw", id, rec.text)
				pipe(drain(cr, client), drain(sr, server))
				return nil
			}
			// Await the server's upgrade response, then both sides are TLS.
			resp, err := readPreambleRecord(sr)
			if err != nil {
				return fmt.Errorf("read upgrade response: %w", err)
			}
			if _, err := client.Write(resp.raw); err != nil {
				return err
			}
			return p.spliceTLS(id, via, drain(cr, client), drain(sr, server))
		default:
			if _, err := server.Write(rec.raw); err != nil {
				return err
			}
		}
	}
}

func (p *proxy) spliceTLS(id int64, via string, client, server net.Conn) error {
	clientTLS := tls.Server(client, p.clientTLS)
	if err := clientTLS.Handshake(); err != nil {
		return fmt.Errorf("client handshake (does SwyxIt! trust our certificate?): %w", err)
	}
	serverTLS := tls.Client(server, &tls.Config{InsecureSkipVerify: true, ServerName: p.sni, MinVersion: tls.VersionTLS12})
	if err := serverTLS.Handshake(); err != nil {
		return fmt.Errorf("server handshake: %w", err)
	}
	facade := facadeName(via)
	log.Printf("[%d] TLS established, facade %s", id, facade)

	base := filepath.Join(p.logDir, fmt.Sprintf("conn-%03d-%s", id, facade))
	rec, err := newRecorder(base, via)
	if err != nil {
		return err
	}
	defer rec.close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); rec.relay("C->S", clientTLS, serverTLS); serverTLS.CloseWrite() }()
	go func() { defer wg.Done(); rec.relay("S->C", serverTLS, clientTLS); clientTLS.CloseWrite() }()
	wg.Wait()
	return nil
}

func (p *proxy) rewriteVia(via string) string {
	u, err := url.Parse(via)
	if err != nil {
		return via
	}
	host := p.upstream
	if u.Port() != "" {
		host = net.JoinHostPort(p.upstream, u.Port())
	}
	u.Host = host
	return u.String()
}

func facadeName(via string) string {
	if u, err := url.Parse(via); err == nil {
		seg := strings.Split(strings.Trim(u.Path, "/"), "/")
		name := seg[len(seg)-1]
		name = strings.TrimSuffix(name, ".jwt2")
		if name != "" {
			return sanitize(name)
		}
	}
	return "unknown"
}

// --- MC-NMF framing (the plaintext preamble, [MS-NMF]) ---

const (
	recVersion        = 0x00
	recMode           = 0x01
	recVia            = 0x02
	recKnownEncoding  = 0x03
	recUpgradeRequest = 0x09
	recUpgradeResp    = 0x0A
	recPreambleAck    = 0x0B
	recPreambleEnd    = 0x0C
)

type preambleRecord struct {
	kind byte
	text string // for Via and UpgradeRequest
	raw  []byte // the exact bytes as read, for verbatim forwarding
}

func readPreambleRecord(r *bufio.Reader) (preambleRecord, error) {
	kind, err := r.ReadByte()
	if err != nil {
		return preambleRecord{}, err
	}
	raw := []byte{kind}
	switch kind {
	case recVersion:
		b, err := readN(r, 2)
		return preambleRecord{kind: kind, raw: append(raw, b...)}, err
	case recMode, recKnownEncoding:
		b, err := r.ReadByte()
		return preambleRecord{kind: kind, raw: append(raw, b)}, err
	case recUpgradeResp, recPreambleAck, recPreambleEnd:
		return preambleRecord{kind: kind, raw: raw}, nil
	case recVia, recUpgradeRequest:
		n, lenBytes, err := readVarint(r)
		if err != nil {
			return preambleRecord{}, err
		}
		text, err := readN(r, n)
		if err != nil {
			return preambleRecord{}, err
		}
		raw = append(append(raw, lenBytes...), text...)
		return preambleRecord{kind: kind, text: string(text), raw: raw}, nil
	default:
		return preambleRecord{}, fmt.Errorf("unexpected preamble record 0x%02x", kind)
	}
}

func encodeString(kind byte, s string) []byte {
	out := []byte{kind}
	out = append(out, putVarint(len(s))...)
	return append(out, s...)
}

// readVarint decodes a .NET 7-bit encoded int and returns the raw bytes too.
func readVarint(r *bufio.Reader) (int, []byte, error) {
	var value, shift int
	var raw []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		raw = append(raw, b)
		value |= int(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, raw, nil
		}
		shift += 7
		if shift > 35 {
			return 0, nil, errors.New("varint too long")
		}
	}
}

func putVarint(n int) []byte {
	var out []byte
	for {
		b := byte(n & 0x7f)
		n >>= 7
		if n != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if n == 0 {
			return out
		}
	}
}

func readN(r *bufio.Reader, n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

// --- decrypted record logging ---

// recorder writes the decrypted inner MC-NMF stream of one connection. Bytes
// are forwarded verbatim; a per-direction parser extracts sized-envelope
// payloads (the binary SOAP) for the human-readable log and a raw dump.
type recorder struct {
	mu   sync.Mutex
	text *os.File
}

func newRecorder(base, via string) (*recorder, error) {
	f, err := os.Create(base + ".log")
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(f, "# %s\n# via: %s\n\n", time.Now().Format(time.RFC3339Nano), via)
	return &recorder{text: f}, nil
}

func (r *recorder) close() { r.text.Close() }

func (r *recorder) relay(dir string, src io.Reader, dst io.Writer) {
	parser := &envelopeParser{}
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
			for _, env := range parser.feed(buf[:n]) {
				r.logEnvelope(dir, env)
			}
		}
		if err != nil {
			return
		}
	}
}

func (r *recorder) logEnvelope(dir string, env []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(r.text, "=== %s  %s  %d bytes ===\n", dir, time.Now().Format("15:04:05.000"), len(env))
	fmt.Fprintln(r.text, printable(env))
	fmt.Fprint(r.text, hexdump(env))
	fmt.Fprintln(r.text)
	r.text.Sync()
}

// envelopeParser extracts sized-envelope (0x06) payloads from the decrypted
// stream. Other framing records (preamble end/ack, end, fault) are skipped.
type envelopeParser struct {
	buf []byte
}

func (p *envelopeParser) feed(b []byte) [][]byte {
	p.buf = append(p.buf, b...)
	var out [][]byte
	for {
		if len(p.buf) == 0 {
			return out
		}
		switch p.buf[0] {
		case recPreambleEnd, recPreambleAck, 0x07: // end-of-record markers, no body
			p.buf = p.buf[1:]
		case 0x06: // sized envelope: 0x06, varint length, payload
			size, hdr, ok := peekVarint(p.buf[1:])
			if !ok {
				return out // need more bytes for the length
			}
			start := 1 + hdr
			if len(p.buf) < start+size {
				return out // wait for the full payload
			}
			out = append(out, append([]byte(nil), p.buf[start:start+size]...))
			p.buf = p.buf[start+size:]
		default:
			// Unknown byte: drop it so we resync rather than stall.
			p.buf = p.buf[1:]
		}
	}
}

func peekVarint(b []byte) (value, n int, ok bool) {
	var shift int
	for i, c := range b {
		value |= int(c&0x7f) << shift
		if c&0x80 == 0 {
			return value, i + 1, true
		}
		shift += 7
		if shift > 35 {
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// --- plain forwarding and TLS splice helpers ---

func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); io.Copy(a, b) }()
	go func() { defer wg.Done(); io.Copy(b, a) }()
	wg.Wait()
}

// prefixConn prepends already-buffered bytes to a connection's read stream.
type prefixConn struct {
	pre []byte
	net.Conn
}

func (c prefixConn) Read(b []byte) (int, error) {
	if len(c.pre) > 0 {
		n := copy(b, c.pre)
		c.pre = c.pre[n:]
		return n, nil
	}
	return c.Conn.Read(b)
}

// drain wraps a connection with any bytes the bufio.Reader already consumed.
func drain(r *bufio.Reader, c net.Conn) net.Conn {
	n := r.Buffered()
	if n == 0 {
		return c
	}
	pre, _ := r.Peek(n)
	cp := make([]byte, n)
	copy(cp, pre)
	return prefixConn{pre: cp, Conn: c}
}

// --- misc ---

func splitPorts(s string) []int {
	var out []int
	for f := range strings.SplitSeq(s, ",") {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		if n, err := strconv.Atoi(f); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
}

// printable extracts runs of at least four printable characters, which in the
// binary SOAP are element names, namespaces and string values.
func printable(b []byte) string {
	var sb strings.Builder
	run := make([]byte, 0, 64)
	flush := func() {
		if len(run) >= 4 {
			sb.WriteString("  ")
			sb.Write(run)
			sb.WriteByte('\n')
		}
		run = run[:0]
	}
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			run = append(run, c)
		} else {
			flush()
		}
	}
	flush()
	return sb.String()
}

func hexdump(b []byte) string {
	var sb strings.Builder
	for i := 0; i < len(b); i += 16 {
		end := min(i+16, len(b))
		fmt.Fprintf(&sb, "%08x  ", i)
		for j := i; j < i+16; j++ {
			if j < end {
				fmt.Fprintf(&sb, "%02x ", b[j])
			} else {
				sb.WriteString("   ")
			}
		}
		sb.WriteString(" ")
		for j := i; j < end; j++ {
			if b[j] >= 0x20 && b[j] < 0x7f {
				sb.WriteByte(b[j])
			} else {
				sb.WriteByte('.')
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func selfSigned(cn string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{cn},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
