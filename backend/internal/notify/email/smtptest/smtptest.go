package smtptest

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type Received struct {
	From string
	To   []string
	Raw  string
}

func (r Received) parse() (*mail.Message, error) {
	return mail.ReadMessage(strings.NewReader(r.Raw))
}

func (r Received) Subject() string {
	m, err := r.parse()
	if err != nil {
		return ""
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
	if err != nil {
		return ""
	}
	return subject
}

func (r Received) Header(name string) string {
	m, err := r.parse()
	if err != nil {
		return ""
	}
	return m.Header.Get(name)
}

func (r Received) Text() string {
	m, err := r.parse()
	if err != nil {
		return ""
	}
	body, err := io.ReadAll(quotedprintable.NewReader(m.Body))
	if err != nil {
		return ""
	}
	return strings.ReplaceAll(string(body), "\r\n", "\n")
}

type Server struct {
	t        testing.TB
	ln       net.Listener
	cert     tls.Certificate
	roots    *x509.CertPool
	user     string
	password string

	mu            sync.Mutex
	starttls      bool
	messages      []Received
	plainAuthSeen int
	failStage     string
	failReply     string
	conns         map[net.Conn]struct{}
	wg            sync.WaitGroup
}

type Options struct {
	User       string
	Password   string
	NoSTARTTLS bool
	FailStage  string
	FailReply  string
}

func New(t testing.TB, opts Options) *Server {
	t.Helper()
	cert, roots := selfSigned(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("smtptest listen: %v", err)
	}
	s := &Server{
		t: t, ln: ln, cert: cert, roots: roots, user: opts.User, password: opts.Password,
		starttls: !opts.NoSTARTTLS, failStage: opts.FailStage, failReply: opts.FailReply,
		conns: map[net.Conn]struct{}{},
	}
	s.wg.Add(1)
	go s.accept()
	t.Cleanup(s.Close)
	return s
}

func selfSigned(t testing.TB) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("smtptest key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "smtptest"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("smtptest cert: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("smtptest parse cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func (s *Server) Host() string { return "127.0.0.1" }

func (s *Server) Port() int {
	return s.ln.Addr().(*net.TCPAddr).Port
}

func (s *Server) TLSConfig() *tls.Config {
	return &tls.Config{RootCAs: s.roots, MinVersion: tls.VersionTLS12}
}

func (s *Server) SetFailure(stage, reply string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStage, s.failReply = stage, reply
}

func (s *Server) Messages() []Received {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Received(nil), s.messages...)
}

func (s *Server) PlainAuthAttempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.plainAuthSeen
}

func (s *Server) Close() {
	_ = s.ln.Close()
	s.mu.Lock()
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() {
				_ = conn.Close()
				s.mu.Lock()
				delete(s.conns, conn)
				s.mu.Unlock()
			}()
			s.serve(conn)
		}()
	}
}

func (s *Server) failure(stage string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failStage == stage {
		return s.failReply
	}
	return ""
}

type session struct {
	conn net.Conn
	r    *bufio.Reader
	tls  bool
	from string
	to   []string
	auth bool
}

func (se *session) reply(format string, args ...any) {
	_, _ = fmt.Fprintf(se.conn, format+"\r\n", args...)
}

func (s *Server) serve(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	se := &session{conn: conn, r: bufio.NewReader(conn)}
	se.reply("220 smtptest ESMTP")
	for {
		line, err := se.r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO", "HELO":
			if s.starttls && !se.tls {
				se.reply("250-smtptest\r\n250-STARTTLS\r\n250 AUTH PLAIN")
			} else {
				se.reply("250-smtptest\r\n250 AUTH PLAIN")
			}
		case "STARTTLS":
			if !s.starttls || se.tls {
				se.reply("502 not available")
				continue
			}
			se.reply("220 ready")
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{s.cert}, MinVersion: tls.VersionTLS12})
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			se.conn, se.r, se.tls = tlsConn, bufio.NewReader(tlsConn), true
			conn = tlsConn
		case "AUTH":
			s.handleAuth(se, arg)
		case "MAIL":
			if !se.auth {
				se.reply("530 authentication required")
				continue
			}
			se.from = extractAddr(arg)
			se.to = nil
			se.reply("250 ok")
		case "RCPT":
			if reply := s.failure("RCPT"); reply != "" {
				se.reply("%s", reply)
				continue
			}
			se.to = append(se.to, extractAddr(arg))
			se.reply("250 ok")
		case "DATA":
			if reply := s.failure("DATA"); reply != "" {
				se.reply("%s", reply)
				continue
			}
			se.reply("354 go ahead")
			raw, err := readData(se.r)
			if err != nil {
				return
			}
			if reply := s.failure("END"); reply != "" {
				se.reply("%s", reply)
				continue
			}
			s.mu.Lock()
			s.messages = append(s.messages, Received{From: se.from, To: se.to, Raw: raw})
			s.mu.Unlock()
			se.reply("250 queued")
		case "RSET":
			se.from, se.to = "", nil
			se.reply("250 ok")
		case "NOOP":
			se.reply("250 ok")
		case "QUIT":
			se.reply("221 bye")
			return
		default:
			se.reply("502 unsupported")
		}
	}
}

func (s *Server) handleAuth(se *session, arg string) {
	mech, initial, _ := strings.Cut(arg, " ")
	if !se.tls {
		s.mu.Lock()
		s.plainAuthSeen++
		s.mu.Unlock()
	}
	if strings.ToUpper(mech) != "PLAIN" {
		se.reply("504 mechanism not supported")
		return
	}
	if initial == "" {
		se.reply("334 ")
		line, err := se.r.ReadString('\n')
		if err != nil {
			return
		}
		initial = strings.TrimRight(line, "\r\n")
	}
	if reply := s.failure("AUTH"); reply != "" {
		se.reply("%s", reply)
		return
	}
	decoded, err := base64.StdEncoding.DecodeString(initial)
	if err != nil {
		se.reply("501 bad encoding")
		return
	}
	parts := strings.Split(string(decoded), "\x00")
	if len(parts) != 3 || parts[1] != s.user || parts[2] != s.password {
		se.reply("535 5.7.8 credentials rejected")
		return
	}
	se.auth = true
	se.reply("235 ok")
}

func extractAddr(arg string) string {
	_, rest, _ := strings.Cut(arg, ":")
	rest = strings.TrimSpace(rest)
	if i := strings.Index(rest, ">"); i >= 0 {
		rest = rest[:i]
	}
	return strings.Trim(rest, "<> ")
}

func readData(r *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", err
		}
		if line == ".\r\n" {
			return b.String(), nil
		}
		if strings.HasPrefix(line, "..") {
			line = line[1:]
		}
		b.WriteString(line)
	}
}

func Addr(s *Server) string {
	return s.Host() + ":" + strconv.Itoa(s.Port())
}
