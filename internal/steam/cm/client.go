// Package cm implements the Steam Connection Manager TCP client:
// channel encrypt, anonymous logon, heartbeat, and raw send/recv.
package cm

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/proxy"
	"github.com/xywf221/steam-workshop-downloader/internal/stealth"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/crypto"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/protocol"
)

// EResult values we care about.
const (
	EResultOK                 = 1
	EResultFail               = 2
	EResultNoConnection       = 3
	EResultInvalidPassword    = 5
	EResultLoggedInElsewhere  = 6
	EResultInvalidProtocolVer = 7
	EResultInvalidParam       = 8
	EResultServiceUnavailable = 20
	EResultTimeout            = 16
	EResultTryAnotherCM       = 48
)

// AnonSteamID is SteamID(type=AnonUser, universe=Public) as used by ValvePython.
const AnonSteamID uint64 = 117093590311632896

// Protocol constants from ValvePython anonymous_login.
const (
	ClientProtocolVersion     = 65580
	ClientPackageVersion      = 1561159470
	UniversePublic            = 1
	ChannelEncryptProtoVersion = 1
)

// DialContextFunc dials a TCP address (optionally through a proxy).
type DialContextFunc func(ctx context.Context, network, address string) (net.Conn, error)

// msgConn is the transport between readLoop/sendRaw and the wire.
// TCP: one message = [VT01 len+magic][payload]. WebSocket: binary frame payload.
type msgConn interface {
	ReadMsg() ([]byte, error)
	WriteMsg(payload []byte) error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	Close() error
}

// tcpMsgConn wraps a net.Conn with VT01 framing.
type tcpMsgConn struct{ c net.Conn }

func (t *tcpMsgConn) ReadMsg() ([]byte, error)         { return protocol.ReadPacket(t.c) }
func (t *tcpMsgConn) WriteMsg(payload []byte) error     { return protocol.WritePacket(t.c, payload) }
func (t *tcpMsgConn) SetReadDeadline(d time.Time) error { return t.c.SetReadDeadline(d) }
func (t *tcpMsgConn) SetWriteDeadline(d time.Time) error {
	return t.c.SetWriteDeadline(d)
}
func (t *tcpMsgConn) Close() error { return t.c.Close() }

// Client is a single Steam CM TCP session.
type Client struct {
	Dial      DialContextFunc
	HTTP      *http.Client
	Servers   *ServerList
	Logf      func(format string, args ...any) // optional debug

	// Stealth omits the TLS SNI extension on WSS + HTTPS (mainland-China
	// direct-connection workaround; certificates are still verified).
	// Set automatically by NewDirect; ignored when a proxy dialer is set.
	Stealth bool

	mu            sync.Mutex
	conn          msgConn
	channelKey    []byte
	channelHMAC   []byte
	channelOK     bool
	steamID       uint64
	sessionID     int32
	cellID        uint32
	heartbeatEvery time.Duration
	stopHeartbeat  chan struct{}
	connected     bool
	loggedOn      bool

	// inbound demux
	recvErr    error
	waiters    map[protocol.EMsg]chan packet
	jobWaiters map[uint64]chan packet
	nextJob    uint64
}

type packet struct {
	raw  []byte
	emsg protocol.EMsg
	proto bool
	msg  *protocol.Msg
	mp   *protocol.MsgProto
}

// New creates a CM client. dial may be nil (direct). httpClient used for CM list bootstrap.
func New(dial DialContextFunc, httpClient *http.Client) *Client {
	if dial == nil {
		d := &net.Dialer{Timeout: 30 * time.Second}
		dial = d.DialContext
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		Dial:       dial,
		HTTP:       httpClient,
		Servers:    NewServerList(),
		waiters:    make(map[protocol.EMsg]chan packet),
		jobWaiters: make(map[uint64]chan packet),
		nextJob:    1,
	}
}

// NewDirect creates a CM client for a proxy-less connection. Its WebAPI/media
// HTTP client uses the stealth transport: TLS without SNI for hosts known to
// be SNI-blocked (store/community edges), normal TLS elsewhere; certificates
// are fully verified either way. CM websockets keep normal SNI — CM wss
// endpoints are IP-literal and reachable directly.
func NewDirect() *Client {
	c := New(nil, stealth.HTTPClient(30*time.Second))
	c.Stealth = true
	return c
}

// NewWithProxy builds a Client whose TCP + HTTP traffic use p.
func NewWithProxy(p *proxy.Parsed) (*Client, error) {
	var dial DialContextFunc
	var httpClient *http.Client
	if p != nil {
		pd, err := proxy.NewDialer(p)
		if err != nil {
			return nil, err
		}
		dial = pd.DialContext
		httpClient = &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				Proxy:       nil, // we dial ourselves
				DialContext: pd.DialContext,
			},
		}
	}
	return New(dial, httpClient), nil
}

func (c *Client) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

// Connected reports TCP+channel state loosely.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected && c.channelOK
}

// LoggedOn reports successful anonymous (or user) logon.
func (c *Client) LoggedOn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loggedOn
}

// SteamID returns the assigned steam id after logon.
func (c *Client) SteamID() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.steamID
}

// CellID returns the cell assigned at logon (0 = unknown / unmatched).
func (c *Client) CellID() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cellID
}

// ConnectAndLogin bootstraps CM list, connects, secures channel, anonymous logon.
// Retries across CMs up to attempts times on TryAnotherCM-class failures.
func (c *Client) ConnectAndLogin(ctx context.Context, attempts int) error {
	if attempts < 1 {
		attempts = 1
	}
	if c.Servers.Len() == 0 {
		if err := c.Servers.BootstrapWebAPI(ctx, c.HTTP); err != nil {
			c.logf("webapi bootstrap failed: %v; falling back to DNS", err)
			c.Servers.BootstrapDNS()
		}
	}
	var last error
	for i := 1; i <= attempts; i++ {
		srv, ok := c.Servers.Next()
		if !ok {
			return fmt.Errorf("no CM servers available")
		}
		c.logf("CM attempt %d/%d → %s", i, attempts, srv.Addr())
		err := c.connectOne(ctx, srv)
		if err == nil {
			err = c.anonymousLogon(ctx)
		}
		if err == nil {
			return nil
		}
		last = err
		c.logf("CM %s failed: %v", srv.Addr(), err)
		c.Servers.MarkBad(srv.Addr())
		c.Disconnect()
		// Only rotate on soft failures.
		if !isRetryableLogin(err) && i == attempts {
			break
		}
	}
	if last == nil {
		last = fmt.Errorf("login failed")
	}
	return last
}

func isRetryableLogin(err error) bool {
	if err == nil {
		return false
	}
	if le, ok := err.(*LoginError); ok {
		switch le.EResult {
		case EResultTryAnotherCM, EResultFail, EResultTimeout, EResultServiceUnavailable, EResultNoConnection:
			return true
		}
	}
	return true // connection errors etc.
}

// LoginError carries an EResult from the CM.
type LoginError struct {
	EResult int
	Msg     string
}

func (e *LoginError) Error() string {
	return fmt.Sprintf("%s (EResult %d)", e.Msg, e.EResult)
}

func (c *Client) connectOne(ctx context.Context, srv Server) error {
	c.Disconnect()
	conn, err := c.dialTransported(ctx, srv)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.channelOK = false
	c.channelKey = nil
	c.channelHMAC = nil
	c.loggedOn = false
	if srv.WSS {
		// WebSocket rides on TLS, so Steam skips the ChannelEncrypt
		// Request/Response/Result handshake entirely — we jump straight to logon.
		c.channelOK = true
	}
	c.mu.Unlock()

	go c.readLoop()

	// Wait for channel encrypt request then complete handshake.
	if !srv.WSS {
		if err := c.secureChannel(ctx); err != nil {
			c.Disconnect()
			return err
		}
	}
	return nil
}

// dialTransported opens the wire transport for a server: raw TCP+VT01 by default,
// TLS+WebSocket when the server entry came from serverlist_websockets.
func (c *Client) dialTransported(ctx context.Context, srv Server) (msgConn, error) {
	if !srv.WSS {
		raw, err := c.Dial(ctx, "tcp", srv.Addr())
		if err != nil {
			return nil, fmt.Errorf("dial %s: %w", srv.Addr(), err)
		}
		return &tcpMsgConn{c: raw}, nil
	}

	tcp, err := c.Dial(ctx, "tcp", srv.Addr())
	if err != nil {
		return nil, fmt.Errorf("wss dial %s: %w", srv.Addr(), err)
	}
	// WebSocket over TLS; host must be the SNI name from the endpoint list.
	tlsConn := tls.Client(tcp, &tls.Config{ServerName: srv.Host})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = tcp.Close()
		return nil, fmt.Errorf("wss tls %s: %w", srv.Host, err)
	}
	ws, err := newWSSConn(tlsConn, srv.Host, srv.Port)
	if err != nil {
		_ = tlsConn.Close()
		return nil, fmt.Errorf("wss handshake %s: %w", srv.Addr(), err)
	}
	c.logf("wss connected %s (%d)", srv.Host, srv.Port)
	return ws, nil
}

func (c *Client) secureChannel(ctx context.Context) error {
	pkt, err := c.waitEMsg(ctx, protocol.EMsgChannelEncryptRequest, 15*time.Second)
	if err != nil {
		return fmt.Errorf("waiting ChannelEncryptRequest: %w", err)
	}
	req, err := protocol.ParseChannelEncryptRequest(pkt.msg.Body)
	if err != nil {
		return err
	}
	if req.ProtocolVersion != ChannelEncryptProtoVersion {
		return fmt.Errorf("unsupported channel protocol %d", req.ProtocolVersion)
	}
	if req.Universe != 0 && req.Universe != UniversePublic {
		// 0 sometimes seen; Public=1
		c.logf("universe=%d (continuing)", req.Universe)
	}

	sessionKey, encKey, err := crypto.GenerateSessionKey(req.Challenge)
	if err != nil {
		return err
	}
	// RSA-OAEP output for 1024-bit key is 128 bytes — matches keySize=128.
	if len(encKey) > 128 {
		return fmt.Errorf("encrypted session key len %d > 128", len(encKey))
	}

	respBody := protocol.ChannelEncryptResponse{
		ProtocolVersion: ChannelEncryptProtoVersion,
		KeySize:         128,
		Key:             encKey,
		CRC:             crc32.ChecksumIEEE(encKey),
	}
	msg := &protocol.Msg{
		Header: protocol.MsgHdr{
			Msg:         protocol.EMsgChannelEncryptResponse,
			TargetJobID: protocol.JobIDNone,
			SourceJobID: protocol.JobIDNone,
		},
		Body: respBody.Serialize(),
	}
	// Job IDs in Python default to -1 which is all-bits-one on the wire.
	if err := c.sendRaw(msg.Serialize(), false); err != nil {
		return err
	}

	resultPkt, err := c.waitEMsg(ctx, protocol.EMsgChannelEncryptResult, 10*time.Second)
	if err != nil {
		return fmt.Errorf("waiting ChannelEncryptResult: %w", err)
	}
	result, err := protocol.ParseChannelEncryptResult(resultPkt.msg.Body)
	if err != nil {
		return err
	}
	if result.EResult != EResultOK {
		return &LoginError{EResult: int(result.EResult), Msg: "channel encrypt failed"}
	}

	c.mu.Lock()
	c.channelKey = sessionKey
	if len(req.Challenge) > 0 {
		c.channelHMAC = sessionKey[:16]
	}
	c.channelOK = true
	c.mu.Unlock()
	c.logf("channel secured (hmac=%v)", len(req.Challenge) > 0)
	return nil
}

func (c *Client) anonymousLogon(ctx context.Context) error {
	body := protocol.ClientLogonBody{
		ProtocolVersion:      ClientProtocolVersion,
		ClientPackageVersion: ClientPackageVersion,
	}.Serialize()
	mp := &protocol.MsgProto{
		Msg: protocol.EMsgClientLogon,
		Header: protocol.ProtoBufHeader{
			SteamID: AnonSteamID,
		},
		Body: body,
	}
	if err := c.SendMsgProto(mp); err != nil {
		return err
	}
	pkt, err := c.waitEMsg(ctx, protocol.EMsgClientLogOnResponse, 30*time.Second)
	if err != nil {
		return fmt.Errorf("waiting ClientLogOnResponse: %w", err)
	}
	if !pkt.proto || pkt.mp == nil {
		return fmt.Errorf("logon response not protobuf")
	}
	resp, err := protocol.ParseClientLogonResponse(pkt.mp.Body)
	if err != nil {
		return err
	}
	if resp.EResult != EResultOK {
		return &LoginError{EResult: int(resp.EResult), Msg: "anonymous login failed"}
	}

	c.mu.Lock()
	if pkt.mp.Header.SteamID != 0 {
		c.steamID = pkt.mp.Header.SteamID
	} else {
		c.steamID = AnonSteamID
	}
	c.sessionID = pkt.mp.Header.ClientSessionID
	c.cellID = resp.CellID
	c.loggedOn = true
	hb := time.Duration(resp.HeartbeatSeconds) * time.Second
	if hb <= 0 {
		hb = 5 * time.Second
	}
	c.heartbeatEvery = hb
	c.mu.Unlock()

	c.startHeartbeat()
	c.logf("logged on steamid=%d session=%d cell=%d hb=%s", c.SteamID(), c.sessionID, c.cellID, hb)
	return nil
}

func (c *Client) startHeartbeat() {
	c.mu.Lock()
	if c.stopHeartbeat != nil {
		close(c.stopHeartbeat)
	}
	stop := make(chan struct{})
	c.stopHeartbeat = stop
	every := c.heartbeatEvery
	c.mu.Unlock()
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				// CMsgClientHeartBeat is empty protobuf body.
				mp := &protocol.MsgProto{
					Msg: protocol.EMsgClientHeartBeat,
					Header: protocol.ProtoBufHeader{
						SteamID:         c.SteamID(),
						ClientSessionID: c.sessionID,
					},
					Body: nil,
				}
				_ = c.SendMsgProto(mp)
			}
		}
	}()
}

// Disconnect closes the CM connection.
func (c *Client) Disconnect() {
	c.mu.Lock()
	if c.stopHeartbeat != nil {
		close(c.stopHeartbeat)
		c.stopHeartbeat = nil
	}
	conn := c.conn
	c.conn = nil
	c.connected = false
	c.channelOK = false
	c.loggedOn = false
	c.channelKey = nil
	c.channelHMAC = nil
	// Unblock waiters
	for emsg, ch := range c.waiters {
		close(ch)
		delete(c.waiters, emsg)
	}
	for id, ch := range c.jobWaiters {
		close(ch)
		delete(c.jobWaiters, id)
	}
	c.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

// SendMsgProto sends an encrypted (if channel up) protobuf message.
func (c *Client) SendMsgProto(m *protocol.MsgProto) error {
	c.mu.Lock()
	if c.steamID != 0 && m.Header.SteamID == 0 {
		m.Header.SteamID = c.steamID
	}
	if c.sessionID != 0 && m.Header.ClientSessionID == 0 {
		m.Header.ClientSessionID = c.sessionID
	}
	c.mu.Unlock()
	return c.sendRaw(m.Serialize(), true)
}

// nextJobID allocates a source job id (1..9999 cycling, matching ValvePython).
func (c *Client) nextJobID() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextJob
	c.nextJob++
	if c.nextJob > 9999 || c.nextJob == 0 {
		c.nextJob = 1
	}
	return id
}

// SendJob sends a protobuf message as a job and returns the source job id.
func (c *Client) SendJob(m *protocol.MsgProto) (uint64, error) {
	jobID := c.nextJobID()
	m.Header.JobIDSource = jobID
	if err := c.SendMsgProto(m); err != nil {
		return 0, err
	}
	return jobID, nil
}

// WaitJob waits for a response whose header.jobid_target matches jobID.
func (c *Client) WaitJob(ctx context.Context, jobID uint64, timeout time.Duration) (*protocol.MsgProto, error) {
	ch := make(chan packet, 1)
	c.mu.Lock()
	c.jobWaiters[jobID] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.jobWaiters, jobID)
		c.mu.Unlock()
	}()

	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case <-tctx.Done():
		return nil, tctx.Err()
	case pkt, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("connection closed waiting for job %d", jobID)
		}
		if pkt.mp == nil {
			return nil, fmt.Errorf("job %d response not protobuf", jobID)
		}
		return pkt.mp, nil
	}
}

// SendJobAndWait sends a job and waits for the matching response.
func (c *Client) SendJobAndWait(ctx context.Context, m *protocol.MsgProto, timeout time.Duration) (*protocol.MsgProto, error) {
	jobID, err := c.SendJob(m)
	if err != nil {
		return nil, err
	}
	return c.WaitJob(ctx, jobID, timeout)
}

// SendUM sends a Unified Message (ServiceMethodCallFromClient) and waits for the response body.
// method is e.g. "PublishedFile.GetDetails#1". body is the already-serialized request protobuf.
// Returns (header EResult, response body bytes, error).
func (c *Client) SendUM(ctx context.Context, method string, body []byte, timeout time.Duration) (int32, []byte, error) {
	const umRetries = 3
	var last error
	for attempt := 1; attempt <= umRetries; attempt++ {
		if !c.LoggedOn() {
			if err := c.ConnectAndLogin(ctx, 3); err != nil {
				last = err
				continue
			}
		}
		mp := &protocol.MsgProto{
			Msg: protocol.EMsgServiceMethodCallFromClient,
			Header: protocol.ProtoBufHeader{
				TargetJobName: method,
			},
			Body: body,
		}
		resp, err := c.SendJobAndWait(ctx, mp, timeout)
		if err != nil {
			last = err
			c.logf("UM %s attempt %d/%d: %v", method, attempt, umRetries, err)
			continue
		}
		er := resp.Header.EResult
		if er != 0 && er != EResultOK {
			last = &LoginError{EResult: int(er), Msg: fmt.Sprintf("UM %s failed: %s", method, resp.Header.ErrorMessage)}
			// soft retry on timeout-ish
			if er == EResultFail || er == EResultTimeout || er == EResultServiceUnavailable || er == EResultTryAnotherCM {
				continue
			}
			return er, resp.Body, last
		}
		return EResultOK, resp.Body, nil
	}
	if last == nil {
		last = fmt.Errorf("UM %s failed", method)
	}
	return 0, nil, last
}

func (c *Client) sendRaw(payload []byte, encryptOK bool) error {
	c.mu.Lock()
	conn := c.conn
	key := c.channelKey
	hmacSecret := c.channelHMAC
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("not connected")
	}
	data := payload
	if encryptOK && key != nil {
		var err error
		if hmacSecret != nil {
			data, err = crypto.SymmetricEncryptHMAC(payload, key, hmacSecret)
		} else {
			data, err = crypto.SymmetricEncrypt(payload, key)
		}
		if err != nil {
			return err
		}
	}
	_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return conn.WriteMsg(data)
}

func (c *Client) waitEMsg(ctx context.Context, emsg protocol.EMsg, timeout time.Duration) (packet, error) {
	ch := make(chan packet, 1)
	c.mu.Lock()
	c.waiters[emsg] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.waiters, emsg)
		c.mu.Unlock()
	}()

	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case <-tctx.Done():
		return packet{}, tctx.Err()
	case pkt, ok := <-ch:
		if !ok {
			return packet{}, fmt.Errorf("connection closed while waiting for %d", emsg)
		}
		return pkt, nil
	}
}

func (c *Client) readLoop() {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return
	}
	for {
		_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		raw, err := conn.ReadMsg()
		if err != nil {
			if err != io.EOF {
				c.logf("read loop: %v", err)
			}
			c.mu.Lock()
			c.recvErr = err
			c.mu.Unlock()
			c.Disconnect()
			return
		}
		c.mu.Lock()
		key := c.channelKey
		hmacSecret := c.channelHMAC
		c.mu.Unlock()
		msg := raw
		if key != nil {
			var derr error
			if hmacSecret != nil {
				msg, derr = crypto.SymmetricDecryptHMAC(raw, key, hmacSecret)
			} else {
				msg, derr = crypto.SymmetricDecrypt(raw, key)
			}
			if derr != nil {
				c.logf("decrypt: %v", derr)
				c.Disconnect()
				return
			}
		}
		c.dispatch(msg)
	}
}

func (c *Client) dispatch(msg []byte) {
	if len(msg) < 4 {
		return
	}
	rawEMsg := binary.LittleEndian.Uint32(msg[0:4])
	emsg := protocol.ClearProtoBit(rawEMsg)
	proto := protocol.IsProto(rawEMsg)

	pkt := packet{raw: msg, emsg: emsg, proto: proto}
	if proto {
		mp, err := protocol.ParseMsgProto(msg)
		if err != nil {
			c.logf("parse msgproto %d: %v", emsg, err)
			return
		}
		pkt.mp = mp
	} else {
		m, err := protocol.ParseMsg(msg)
		if err != nil {
			c.logf("parse msg %d: %v", emsg, err)
			return
		}
		pkt.msg = m
	}

	if emsg == protocol.EMsgMulti && pkt.mp != nil {
		c.handleMulti(pkt.mp.Body)
		return
	}

	// Job responses: match jobid_target to a waiter.
	if pkt.mp != nil && pkt.mp.Header.JobIDTarget != 0 && pkt.mp.Header.JobIDTarget != protocol.JobIDNone {
		c.mu.Lock()
		jch := c.jobWaiters[pkt.mp.Header.JobIDTarget]
		c.mu.Unlock()
		if jch != nil {
			select {
			case jch <- pkt:
			default:
			}
			return
		}
	}

	c.mu.Lock()
	ch := c.waiters[emsg]
	c.mu.Unlock()
	if ch != nil {
		select {
		case ch <- pkt:
		default:
		}
	}
}

// handleMulti unpacks CMsgMulti: field 1 = size_unzipped (varint), field 2 = message_body bytes.
// Nested messages are length-prefixed uint32 LE. Body may be gzip-compressed when size_unzipped > 0.
func (c *Client) handleMulti(body []byte) {
	var payload []byte
	var sizeUnzipped uint64
	i := 0
	for i < len(body) {
		key, n := consumeVarintLocal(body[i:])
		if n == 0 {
			return
		}
		i += n
		field := int(key >> 3)
		wt := int(key & 7)
		switch wt {
		case 0:
			v, n := consumeVarintLocal(body[i:])
			if n == 0 {
				return
			}
			i += n
			if field == 1 {
				sizeUnzipped = v
			}
		case 2:
			l, n := consumeVarintLocal(body[i:])
			if n == 0 {
				return
			}
			i += n
			if i+int(l) > len(body) {
				return
			}
			val := body[i : i+int(l)]
			i += int(l)
			if field == 2 {
				payload = val
			}
		case 5:
			i += 4
		case 1:
			i += 8
		default:
			return
		}
	}
	if sizeUnzipped > 0 || (len(payload) >= 2 && payload[0] == 0x1f && payload[1] == 0x8b) {
		gr, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			c.logf("multi gzip open: %v", err)
			return
		}
		plain, err := io.ReadAll(gr)
		_ = gr.Close()
		if err != nil {
			c.logf("multi gzip read: %v", err)
			return
		}
		if sizeUnzipped > 0 && uint64(len(plain)) != sizeUnzipped {
			c.logf("multi gzip size mismatch got=%d want=%d", len(plain), sizeUnzipped)
		}
		payload = plain
	}
	for len(payload) >= 4 {
		n := binary.LittleEndian.Uint32(payload[:4])
		payload = payload[4:]
		if int(n) > len(payload) {
			return
		}
		c.dispatch(payload[:n])
		payload = payload[n:]
	}
}

func consumeVarintLocal(b []byte) (uint64, int) {
	var x uint64
	var s uint
	for i := 0; i < len(b) && i < 10; i++ {
		c := b[i]
		if c < 0x80 {
			return x | uint64(c)<<s, i + 1
		}
		x |= uint64(c&0x7f) << s
		s += 7
	}
	return 0, 0
}
