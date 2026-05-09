package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"yuanbohan/tunnel/internal/protocol"
	tunnelsession "yuanbohan/tunnel/internal/tunnel/session"
)

const defaultPreviewThrottle = 250 * time.Millisecond
const defaultBrokerSubmitEnterGap = 120 * time.Millisecond

var brokerWriteTimeout = 2 * time.Second

const defaultPendingBrokerCommandLimit = 128
const defaultPendingBrokerWriteLimit = 256

type SessionRegistrationClient struct {
	paths   Paths
	session BrokerSession
	mirror  *tunnelsession.TerminalMirror

	dialer                         func(context.Context, string) (net.Conn, error)
	daemonStatus                   func(context.Context, Paths) (StatusInfo, error)
	sleep                          func(context.Context, time.Duration) bool
	now                            func() time.Time
	throttle                       time.Duration
	expectedBaseURL                string
	expectedAuthContextFingerprint string

	notify    chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	writeMu   sync.Mutex

	mu              sync.Mutex
	lastSentPreview string
	pendingSnapshot bool
	conn            net.Conn
	encoder         *json.Encoder
	brokerWrites    chan BrokerFrame

	hubMu           sync.Mutex
	hub             *tunnelsession.Hub
	pendingCommands []BrokerFrame
}

func NewSessionRegistrationClient(paths Paths, info protocol.SessionInfo) *SessionRegistrationClient {
	return &SessionRegistrationClient{
		paths:        paths,
		session:      BrokerSessionFromSessionInfo(info),
		mirror:       tunnelsession.NewTerminalMirror(0, 0),
		throttle:     defaultPreviewThrottle,
		notify:       make(chan struct{}, 1),
		done:         make(chan struct{}),
		now:          time.Now,
		daemonStatus: Status,
		dialer: func(ctx context.Context, socketPath string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socketPath)
		},
		sleep: func(ctx context.Context, d time.Duration) bool {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return false
			case <-timer.C:
				return true
			}
		},
	}
}

func (c *SessionRegistrationClient) SetExpectedBaseURL(baseURL string) {
	if c == nil {
		return
	}
	c.expectedBaseURL = strings.TrimSpace(baseURL)
}

func (c *SessionRegistrationClient) SetExpectedDaemonContext(baseURL, authToken string) {
	if c == nil {
		return
	}
	c.expectedBaseURL = strings.TrimSpace(baseURL)
	c.expectedAuthContextFingerprint = AuthContextFingerprint(authToken)
}

func BrokerSessionFromSessionInfo(info protocol.SessionInfo) BrokerSession {
	updatedAt := protocol.UnixTimestamp(time.Now().UTC())
	if info.StartedAt > updatedAt {
		updatedAt = info.StartedAt
	}
	return BrokerSession{
		SessionID:      info.SessionID,
		DeviceID:       info.DeviceID,
		Launcher:       info.Launcher,
		Label:          info.Label,
		CWD:            info.CWD,
		CommandPreview: info.CommandPreview,
		GitBranch:      info.GitBranch,
		StartedAt:      info.StartedAt,
		UpdatedAt:      updatedAt,
		Online:         true,
		PlatformFamily: info.PlatformFamily,
		PlatformID:     info.PlatformID,
		ComputerName:   info.ComputerName,
		LaunchSource:   info.LaunchSource,
	}
}

func (c *SessionRegistrationClient) WriteOutput(data []byte) error {
	if c == nil || len(data) == 0 {
		return nil
	}
	select {
	case <-c.done:
		return nil
	default:
	}
	dataCopy := append([]byte(nil), data...)
	c.mirror.WriteOutput(dataCopy)
	c.mu.Lock()
	c.pendingSnapshot = true
	c.mu.Unlock()
	c.enqueueBrokerWrite(BrokerFrame{
		Type:      brokerFrameOutputBytes,
		SessionID: c.session.SessionID,
		Output:    dataCopy,
	})
	select {
	case c.notify <- struct{}{}:
	default:
	}
	return nil
}

func (c *SessionRegistrationClient) BindHub(hub *tunnelsession.Hub) {
	if c == nil || hub == nil {
		return
	}
	if cols, rows := hub.CurrentSize(); cols > 0 && rows > 0 {
		c.applyResize(cols, rows, false)
	}
	hub.AddResizeListener("session_registration", func(cols, rows int) {
		c.applyResize(cols, rows, true)
	})
	c.hubMu.Lock()
	c.hub = hub
	pending := append([]BrokerFrame(nil), c.pendingCommands...)
	c.pendingCommands = nil
	c.hubMu.Unlock()
	for _, frame := range pending {
		c.deliverBrokerCommand(hub, frame)
	}
}

func (c *SessionRegistrationClient) Run(ctx context.Context) {
	if c == nil {
		return
	}
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		default:
		}

		err := c.runOnce(ctx)
		if err == nil {
			backoff = time.Second
			continue
		}
		if !c.sleepWithDone(ctx, backoff) {
			return
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

func (c *SessionRegistrationClient) Close() error {
	if c == nil {
		return nil
	}
	var err error
	c.closeOnce.Do(func() {
		err = c.writeFrame(BrokerFrame{
			Type:      brokerFrameSessionGone,
			SessionID: c.session.SessionID,
			UpdatedAt: protocol.UnixTimestamp(c.now().UTC()),
		})
		close(c.done)
		c.mu.Lock()
		if c.conn != nil {
			_ = c.conn.Close()
		}
		c.mu.Unlock()
	})
	return err
}

func (c *SessionRegistrationClient) runOnce(ctx context.Context) error {
	if err := verifyBrokerSocket(c.paths.BrokerSocketPath); err != nil {
		return err
	}
	if err := c.verifyDaemonContext(ctx); err != nil {
		return err
	}
	conn, err := c.dialer(ctx, c.paths.BrokerSocketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	c.mu.Lock()
	c.conn = conn
	c.encoder = json.NewEncoder(conn)
	c.brokerWrites = make(chan BrokerFrame, defaultPendingBrokerWriteLimit)
	c.lastSentPreview = ""
	brokerWrites := c.brokerWrites
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
			c.encoder = nil
			c.brokerWrites = nil
		}
		c.mu.Unlock()
	}()

	session := c.session
	session.UpdatedAt = protocol.UnixTimestamp(c.now().UTC())
	if err := c.writeFrame(BrokerFrame{Type: brokerFrameRegisterSession, Session: &session}); err != nil {
		return err
	}
	if err := c.writeFrame(c.snapshotFrame()); err != nil {
		return err
	}

	readErr := make(chan error, 1)
	go func() {
		decoder := json.NewDecoder(conn)
		for {
			var frame BrokerFrame
			if err := decoder.Decode(&frame); err != nil {
				readErr <- err
				return
			}
			c.handleBrokerFrame(frame)
		}
	}()

	var timer *time.Timer
	var timerC <-chan time.Time
	var pending string
	var pendingSnapshot bool
	var lastSentAt time.Time
	sendNow := func(preview string, snapshot bool) (bool, error) {
		changed := false
		c.mu.Lock()
		if preview != c.lastSentPreview {
			c.lastSentPreview = preview
			changed = true
		}
		c.mu.Unlock()
		now := c.now().UTC()
		if changed {
			if err := c.writeFrame(BrokerFrame{
				Type:      brokerFramePreviewUpdate,
				SessionID: c.session.SessionID,
				Preview:   preview,
				UpdatedAt: protocol.UnixTimestamp(now),
				}); err != nil {
				return false, err
			}
		}
		if snapshot {
			if err := c.writeFrame(c.snapshotFrame()); err != nil {
				return false, err
			}
		}
		lastSentAt = now
		return changed, nil
	}
	if preview := c.previewSnapshot(); preview != "" {
		if _, err := sendNow(preview, false); err != nil {
			return err
		}
	}
	resetTimer := func(d time.Duration) {
		if timer == nil {
			timer = time.NewTimer(d)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(d)
		}
		timerC = timer.C
	}
	schedulePending := func(now time.Time) {
		if timerC != nil {
			return
		}
		if lastSentAt.IsZero() {
			resetTimer(0)
			return
		}
		delay := lastSentAt.Add(c.throttle).Sub(now)
		if delay < 0 {
			delay = 0
		}
		resetTimer(delay)
	}
	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timerC = nil
	}
	defer stopTimer()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-c.done:
			return nil
		case err := <-readErr:
			return err
		case frame := <-brokerWrites:
			if err := c.writeFrame(frame); err != nil {
				return err
			}
		case <-c.notify:
			preview := c.previewSnapshot()
			c.mu.Lock()
			snapshot := c.pendingSnapshot
			if snapshot {
				c.pendingSnapshot = false
			}
			c.mu.Unlock()
			if c.throttle <= 0 {
				if _, err := sendNow(preview, snapshot); err != nil {
					return err
				}
				continue
			}
			now := c.now().UTC()
			if lastSentAt.IsZero() || now.Sub(lastSentAt) >= c.throttle {
				stopTimer()
				pending = ""
				pendingSnapshot = false
				if _, err := sendNow(preview, snapshot); err != nil {
					return err
				}
				continue
			}
			pending = preview
				pendingSnapshot = pendingSnapshot || snapshot
			schedulePending(now)
		case <-timerC:
			timerC = nil
			if _, err := sendNow(pending, pendingSnapshot); err != nil {
				return err
			}
			pending = ""
			pendingSnapshot = false
		}
	}
}

func (c *SessionRegistrationClient) handleBrokerFrame(frame BrokerFrame) {
	switch frame.Type {
	case brokerFrameInputText, brokerFrameInputKey, brokerFrameResize:
		c.routeBrokerCommand(frame)
	default:
	}
}

func (c *SessionRegistrationClient) routeBrokerCommand(frame BrokerFrame) {
	c.hubMu.Lock()
	hub := c.hub
	if hub == nil {
		if len(c.pendingCommands) >= defaultPendingBrokerCommandLimit {
			c.pendingCommands = c.pendingCommands[1:]
		}
		c.pendingCommands = append(c.pendingCommands, frame)
		c.hubMu.Unlock()
		return
	}
	c.hubMu.Unlock()
	c.deliverBrokerCommand(hub, frame)
}

func (c *SessionRegistrationClient) deliverBrokerCommand(hub *tunnelsession.Hub, frame BrokerFrame) {
	if hub == nil {
		return
	}
	switch frame.Type {
	case brokerFrameInputText:
		if frame.Submit {
			_ = hub.WriteInputSequenceWithGap(defaultBrokerSubmitEnterGap, tunnelsession.EncodeRemoteSubmitInput(frame.Text)...)
			return
		}
		_ = hub.WriteInput(tunnelsession.EncodeRemoteTextInput(frame.Text))
	case brokerFrameInputKey:
		if data, ok := tunnelsession.EncodeRemoteKeyInput(frame.Key); ok {
			_ = hub.WriteInput(data)
		}
	case brokerFrameResize:
		_ = hub.Resize(frame.Cols, frame.Rows)
	}
}

func (c *SessionRegistrationClient) verifyDaemonContext(ctx context.Context) error {
	expectedBaseURL := strings.TrimSpace(c.expectedBaseURL)
	if expectedBaseURL == "" {
		return nil
	}
	statusFn := c.daemonStatus
	if statusFn == nil {
		statusFn = Status
	}
	status, err := statusFn(ctx, c.paths)
	if err != nil {
		return err
	}
	if !status.Running {
		return ErrNotRunning
	}
	runningBaseURL := strings.TrimSpace(status.BaseURL)
	if runningBaseURL == "" {
		return errors.New("daemon base URL unavailable")
	}
	if runningBaseURL != expectedBaseURL {
		return errors.New("daemon base URL does not match tunnel run")
	}
	if c.expectedAuthContextFingerprint != "" && strings.TrimSpace(status.AuthContextFingerprint) != c.expectedAuthContextFingerprint {
		return errors.New("daemon auth context does not match tunnel run")
	}
	return nil
}

func (c *SessionRegistrationClient) previewSnapshot() string {
	return c.mirror.PreviewText(tunnelsession.DefaultPreviewMaxChars)
}

func (c *SessionRegistrationClient) snapshotFrame() BrokerFrame {
	snapshot, cols, rows := c.mirror.Snapshot()
	return BrokerFrame{
		Type:         brokerFrameSnapshotUpdate,
		SessionID:    c.session.SessionID,
		Snapshot:     snapshot,
		SnapshotCols: cols,
		SnapshotRows: rows,
	}
}

func (c *SessionRegistrationClient) applyResize(cols, rows int, publish bool) {
	if c == nil || cols <= 0 || rows <= 0 {
		return
	}
	c.mirror.Resize(cols, rows)
	if publish {
		c.mu.Lock()
		c.pendingSnapshot = true
		c.mu.Unlock()
		select {
		case c.notify <- struct{}{}:
		default:
		}
	}
}

func (c *SessionRegistrationClient) enqueueBrokerWrite(frame BrokerFrame) {
	c.mu.Lock()
	ch := c.brokerWrites
	conn := c.conn
	c.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- frame:
	default:
		if conn != nil {
			_ = conn.Close()
		}
	}
}

func (c *SessionRegistrationClient) writeFrame(frame BrokerFrame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.Lock()
	encoder := c.encoder
	conn := c.conn
	c.mu.Unlock()
	if encoder == nil || conn == nil {
		return nil
	}
	if brokerWriteTimeout > 0 {
		_ = conn.SetWriteDeadline(c.now().Add(brokerWriteTimeout))
		defer conn.SetWriteDeadline(time.Time{})
	}
	return encoder.Encode(frame)
}

func (c *SessionRegistrationClient) sleepWithDone(ctx context.Context, d time.Duration) bool {
	sleep := c.sleep
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) bool {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return false
			case <-timer.C:
				return true
			}
		}
	}
	sleepCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-c.done:
			cancel()
		case <-sleepCtx.Done():
		}
	}()
	return sleep(sleepCtx, d)
}

func verifyBrokerSocket(socketPath string) error {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return errors.New("broker socket path is empty")
	}
	info, err := os.Lstat(socketPath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("broker socket path is not a unix socket")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("broker socket is not owner-only")
	}
	return verifyBrokerSocketOwner(socketPath, info)
}
