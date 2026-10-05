package live

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/logging"
	"github.com/pion/webrtc/v4"
	"github.com/termbacktime/termbacktime/internal/config"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/terminal"
)

type APIError struct {
	Status        int
	Code, Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("live API %s (HTTP %d): %s", e.Code, e.Status, e.Message)
}

type API struct {
	Origin, Room, Capability string
	HTTP                     *http.Client
}

// Call bounds every control response and keeps capabilities out of request URLs
func (a *API) Call(ctx context.Context, action string, body any, out any) error {
	if body == nil {
		body = struct{}{}
	}
	path := "/api/v1/rooms"
	if a.Room != "" {
		path += "/" + a.Room + "/" + action
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", a.Origin+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.Capability != "" {
		req.Header.Set("Authorization", "Bearer "+a.Capability)
	}
	res, err := a.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("live API unavailable")
	}
	defer res.Body.Close()
	data, err := recording.ReadBounded(res.Body, 131072)
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var problem struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		_ = json.Unmarshal(data, &problem)
		return &APIError{res.StatusCode, problem.Code, problem.Error}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

type channelResult struct {
	ID        *uint16 `json:"id"`
	ErrorCode string  `json:"errorCode"`
}
type negotiation struct {
	Description webrtc.SessionDescription `json:"sessionDescription"`
	Renegotiate bool                      `json:"requiresImmediateRenegotiation"`
	Channel     channelResult             `json:"dataChannel"`
	Lower       channelResult             `json:"datachannel"`
	Channels    []channelResult           `json:"dataChannels"`
}

// answerOffer gathers local candidates before acknowledging an SFU negotiation
func answerOffer(ctx context.Context, api API, pc *webrtc.PeerConnection, offer webrtc.SessionDescription) error {
	if offer.Type != webrtc.SDPTypeOffer {
		return fmt.Errorf("SFU negotiation did not include an offer")
	}
	if err := pc.SetRemoteDescription(offer); err != nil {
		return err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return err
	}
	gather := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(answer); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-gather:
	}
	return api.Call(ctx, "answer", map[string]any{"sessionDescription": pc.LocalDescription()}, nil)
}

type ControlState struct {
	Paused   bool `json:"paused"`
	Revision int  `json:"revision"`
}

type connection struct {
	controls  chan ControlState
	ended     atomic.Bool
	closeOnce sync.Once
	pc        *webrtc.PeerConnection
	dc        *webrtc.DataChannel
	ws        *websocket.Conn
	api       API
	fail      chan struct{}
	snapshot  atomic.Bool
	done      chan struct{}
}

func (c *connection) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		if c.ws != nil {
			c.ws.Close()
		}
		c.pc.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.api.Call(ctx, "close", nil, nil)
	})
}

// connect completes SFU transport negotiation before exposing the publisher channel
func connect(ctx context.Context, host API, generation string) (result *connection, err error) {
	return connectRole(ctx, host, generation, false, false)
}

func connectRole(ctx context.Context, host API, generation string, viewer, relayOnly bool) (result *connection, err error) {
	var joined struct {
		Capability string `json:"capability"`
		ControlState
	}
	if err = host.Call(ctx, "join", map[string]string{"generation": generation}, &joined); err != nil {
		return nil, err
	}
	api := host
	api.Capability = joined.Capability
	cleanupAdmission := true
	defer func() {
		if err != nil && cleanupAdmission {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = api.Call(cleanup, "close", nil, nil)
		}
	}()
	var ice struct {
		Servers []webrtc.ICEServer `json:"iceServers"`
	}
	if err = api.Call(ctx, "ice", nil, &ice); err != nil {
		return nil, err
	}
	configuration := webrtc.Configuration{ICEServers: ice.Servers}
	if relayOnly {
		configuration.ICETransportPolicy = webrtc.ICETransportPolicyRelay
	}
	// Report sanitized connection stages ourselves; transport debug logs can
	// contain candidate addresses and must not bypass the diagnostic policy.
	settings := webrtc.SettingEngine{LoggerFactory: &logging.DefaultLoggerFactory{
		Writer: io.Discard, DefaultLogLevel: logging.LogLevelDisabled,
	}}
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(settings)).NewPeerConnection(configuration)
	if err != nil {
		return nil, err
	}
	c := &connection{pc: pc, api: api, fail: make(chan struct{}, 1), done: make(chan struct{}), controls: make(chan ControlState, 1)}
	cleanupAdmission = false
	c.controls <- joined.ControlState
	defer func() {
		if err != nil {
			c.close()
		}
	}()
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateDisconnected {
			select {
			case c.fail <- struct{}{}:
			default:
			}
		}
	})
	var transport negotiation
	if err = api.Call(ctx, "establish", nil, &transport); err != nil {
		return nil, err
	}
	id := transport.Channel.ID
	if id == nil {
		id = transport.Lower.ID
	}
	if id == nil || *id == 65535 || transport.Description.Type != webrtc.SDPTypeOffer {
		return nil, fmt.Errorf("invalid SFU transport")
	}
	// The SFU opens this channel in-band so Pion must receive and acknowledge its DCEP Open
	transportReady := make(chan struct{}, 1)
	pc.OnDataChannel(func(channel *webrtc.DataChannel) {
		if channel.ID() == nil || *channel.ID() != *id || channel.Label() != "server-events" {
			_ = channel.Close()
			return
		}
		channel.OnOpen(func() {
			select {
			case transportReady <- struct{}{}:
			default:
			}
		})
	})
	if err = answerOffer(ctx, api, pc, transport.Description); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.fail:
		return nil, fmt.Errorf("ICE_TURN: SFU transport failed (ICE %s); retry or run doctor --live --relay-only", pc.ICEConnectionState())
	case <-transportReady:
	}
	var channels negotiation
	if err = api.Call(ctx, "channel", nil, &channels); err != nil {
		return nil, err
	}
	if channels.Renegotiate || channels.Description.Type == webrtc.SDPTypeOffer {
		if err = answerOffer(ctx, api, pc, channels.Description); err != nil {
			return nil, err
		}
	}
	if len(channels.Channels) != 1 || channels.Channels[0].ID == nil || channels.Channels[0].ErrorCode != "" {
		return nil, fmt.Errorf("invalid SFU channel")
	}
	if *channels.Channels[0].ID == *id || *channels.Channels[0].ID == 65535 {
		return nil, fmt.Errorf("invalid SFU channel ID")
	}
	yes := true
	c.dc, err = pc.CreateDataChannel("terminal-v1", &webrtc.DataChannelInit{Negotiated: &yes, Ordered: &yes, ID: channels.Channels[0].ID})
	if err != nil {
		return nil, err
	}
	opened := make(chan struct{})
	c.dc.OnOpen(func() { close(opened) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-opened:
	}
	if viewer {
		if err := c.dc.SendText("ack"); err != nil {
			return nil, err
		}
	}
	var ticket struct {
		Ticket string `json:"ticket"`
	}
	if err = api.Call(ctx, "ticket", nil, &ticket); err != nil {
		return nil, err
	}
	u, _ := url.Parse(host.Origin)
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = "/api/v1/rooms/" + host.Room + "/control"
	dial := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Subprotocols: []string{"tbt", ticket.Ticket}}
	c.ws, _, err = dial.DialContext(ctx, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("control connection failed")
	}
	c.ws.SetReadLimit(4096)
	go func() {
		for {
			_ = c.ws.SetReadDeadline(time.Now().Add(60 * time.Second))
			_, data, e := c.ws.ReadMessage()
			if e != nil {
				select {
				case c.fail <- struct{}{}:
				default:
				}
				return
			}
			var message struct {
				Type string `json:"type"`
				ControlState
			}
			if json.Unmarshal(data, &message) == nil {
				if message.Type == "settings" || message.Type == "status" {
					select {
					case c.controls <- message.ControlState:
					default:
						select {
						case <-c.controls:
						default:
						}
						select {
						case c.controls <- message.ControlState:
						default:
						}
					}
				}
				if message.Type == "snapshot" {
					c.snapshot.Store(true)
				}
				if message.Type == "ended" {
					c.ended.Store(true)
					select {
					case c.fail <- struct{}{}:
					default:
					}
					return
				}
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(20000 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-c.done:
				return
			case <-ticker.C:
				_ = c.ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if c.ws.WriteMessage(websocket.TextMessage, []byte("heartbeat")) != nil {
					return
				}
			}
		}
	}()
	if err = api.Call(ctx, "ready", nil, nil); err != nil {
		return nil, err
	}
	c.snapshot.Store(true)
	return c, nil
}

type Options struct {
	OnDiagnostics   func(Diagnostics)
	Endpoint, Shell string
	Title           string
	Started         int64
	Args            []string
	OnReady         func(string)
	OnHostReady     func(string)
	OnStatus        func(string)
	OnEvent         func(terminal.Event) error
	TTL             time.Duration
	RelayOnly       bool
	Presentation    terminal.Presentation
	Cols, Rows      int
	OnRoomStatus    func(RoomStatus, error)
}

func Run(parent context.Context, o Options) error {
	origin, err := config.ValidateSiteURL(o.Endpoint)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	diagnostics := monitorDiagnostics(ctx, o.OnDiagnostics)
	host := API{Origin: origin, HTTP: &http.Client{Timeout: 20 * time.Second}}
	var room struct {
		ID         string `json:"roomId"`
		Host       string `json:"hostCapability"`
		Viewer     string `json:"viewerCapability"`
		ShareURL   string `json:"shareUrl"`
		Management string `json:"managementCapability"`
	}
	if err = host.Call(ctx, "", map[string]any{"ttlSeconds": int64(o.TTL / time.Second)}, &room); err != nil {
		return err
	}
	share, err := url.Parse(room.ShareURL)
	if err != nil || share == nil || share.RawQuery != "" || share.Fragment != "" {
		return fmt.Errorf("invalid sharing URL from Worker")
	}
	if _, err = config.ValidateSiteURL(share.Scheme + "://" + share.Host); err != nil {
		return fmt.Errorf("invalid sharing origin from Worker: %w", err)
	}
	host.Room = room.ID
	host.Capability = room.Host
	defer func() {
		endCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = host.Call(endCtx, "end", nil, nil)
	}()
	key := make([]byte, 32)
	defer clear(key)
	if _, err = rand.Read(key); err != nil {
		return err
	}
	cols, rows := terminal.Size(os.Stdout)
	if o.Cols > 0 && o.Rows > 0 {
		cols, rows = o.Cols, o.Rows
	}
	screen := NewScreen(cols, rows)
	if o.Started == 0 {
		o.Started = time.Now().Unix()
	}
	screen.SetSession(o.Title, o.Started, recording.CurrentInfo())
	defer screen.Close()
	sealer, err := NewSealer(key, room.ID)
	if err != nil {
		return err
	}
	setup, stop := context.WithTimeout(ctx, 30*time.Second)
	conn, err := connectRole(setup, host, sealer.Generation(), false, o.RelayOnly)
	stop()
	if err != nil {
		return err
	}
	diagnostics.set(conn, "connected", false)
	defer func() {
		if conn != nil {
			conn.close()
		}
	}()
	if o.OnReady != nil {
		o.OnReady(room.ShareURL + "#v=" + room.Viewer + "&k=" + base64.RawURLEncoding.EncodeToString(key))
	}
	if o.OnHostReady != nil && room.Management != "" {
		o.OnHostReady(origin + "/host/" + room.ID + "#m=" + room.Management)
	}
	if o.OnRoomStatus != nil && room.Management != "" {
		management := host
		management.Capability = room.Management
		done := make(chan struct{})
		go func() { defer close(done); pollRoom(ctx, management, o.OnRoomStatus) }()
		defer func() { cancel(); <-done }()
	}
	status := func(message string) {
		if o.OnStatus != nil {
			o.OnStatus(message)
		}
	}
	terminalDone := make(chan error, 1)
	terminalFinished := false
	// Stop PTY callbacks before closing their terminal emulator
	defer func() {
		cancel()
		if !terminalFinished {
			<-terminalDone
		}
	}()
	go func() {
		terminalDone <- terminal.Run(ctx, terminal.Options{Shell: o.Shell, Args: o.Args, Cols: cols, Rows: rows, Presentation: o.Presentation, OnEvent: func(e terminal.Event) error {
			if o.OnEvent != nil {
				if err := o.OnEvent(e); err != nil {
					status("Recording failed; journal retained: " + err.Error())
				}
			}
			if e.Cols > 0 {
				screen.Resize(e.Cols, e.Rows)
				return nil
			}
			return screen.Write(e.Text)
		}})
	}()
	fps := 30
	ticker := time.NewTicker(time.Second / time.Duration(fps))
	adapt := func(next int) {
		next = max(5, min(30, next))
		if next != fps {
			fps = next
			ticker.Reset(time.Second / time.Duration(fps))
		}
	}
	defer ticker.Stop()
	lastSnapshot := time.Time{}
	paused := false
	pendingRevision := 0
	nextAck := time.Time{}
	for {
		var failed <-chan struct{}
		var controls <-chan ControlState
		if conn != nil {
			failed = conn.fail
			controls = conn.controls
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-terminalDone:
			terminalFinished = true
			return err
		case desired := <-controls:
			paused = desired.Paused
			state := "connected"
			if paused {
				state = "paused"
			}
			diagnostics.set(conn, state, false)
			pendingRevision = desired.Revision
			nextAck = time.Time{}
			if !paused && conn != nil {
				conn.snapshot.Store(true)
			}

		case <-failed:
			diagnostics.set(nil, "reconnecting", false)
			conn.close()
			if conn.ended.Load() {
				status("Sharing ended; your shell and any local recording continue")
				conn = nil
				diagnostics.set(nil, "ended", false)
				continue
			}
			deadline := time.Now().Add(55 * time.Second)
			status("Reconnecting sharing; shell and recording continue")
			for {
				sealer, err = NewSealer(key, room.ID)
				if err != nil {
					return err
				}
				diagnostics.set(nil, "reconnecting", true)
				attempt, end := context.WithTimeout(ctx, 20*time.Second)
				next, e := connectRole(attempt, host, sealer.Generation(), false, o.RelayOnly)
				end()
				if e == nil {
					conn = next
					state := "connected"
					if paused {
						state = "paused"
					}
					diagnostics.set(conn, state, false)
					status("Sharing reconnected")
					break
				}
				var apiErr *APIError
				if errors.As(e, &apiErr) && apiErr.Status == http.StatusGone {
					status("Sharing ended; your shell and any local recording continue")
					conn = nil
					diagnostics.set(nil, "ended", false)
					break
				}
				if time.Now().After(deadline) {
					status("Sharing stopped after reconnect timeout; your shell and any local recording continue")
					conn = nil
					diagnostics.set(nil, "ended", false)
					break
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case err := <-terminalDone:
					terminalFinished = true
					return err
				case <-time.After(time.Second):
				}
			}
		case <-ticker.C:
			if conn != nil && pendingRevision > 0 && time.Now().After(nextAck) {
				ackCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				e := conn.api.Call(ackCtx, "ack", map[string]any{"revision": pendingRevision}, nil)
				stop()
				if e == nil {
					pendingRevision = 0
				} else {
					nextAck = time.Now().Add(2 * time.Second)
				}
			}
			if conn == nil || paused {
				continue
			}
			// Send only the newest screen when network backpressure clears
			if conn.dc.BufferedAmount() > 1<<20 {
				adapt(fps / 2)
				continue
			}
			force := conn.snapshot.Swap(false) || time.Since(lastSnapshot) > 2*time.Second
			frame, changed := screen.Frame(force)
			if !changed {
				adapt(fps - 5)
				continue
			}
			if !force {
				adapt(fps + 5)
			}
			payload, e := json.Marshal(frame)
			if e != nil {
				return e
			}
			chunks, e := sealer.Seal(payload)
			if e != nil {
				return e
			}
			for _, chunk := range chunks {
				if e = conn.dc.Send(chunk); e != nil {
					select {
					case conn.fail <- struct{}{}:
					default:
					}
					break
				}
			}
			if force {
				lastSnapshot = time.Now()
			}
		}
	}
}

// RoomStatus uses the management capability; polling never admits a viewer.
type RoomStatus struct {
	Viewers      int   `json:"viewerCount"`
	Expires      int64 `json:"expiresAt"`
	Paused       bool  `json:"paused"`
	Locked       bool  `json:"locked"`
	Revision     int   `json:"revision"`
	Acknowledged int   `json:"acknowledged"`
}

func pollRoom(ctx context.Context, api API, notify func(RoomStatus, error)) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		call, stop := context.WithTimeout(ctx, 3*time.Second)
		var state RoomStatus
		err := api.Call(call, "status", nil, &state)
		stop()
		if ctx.Err() != nil {
			return
		}
		notify(state, err)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
