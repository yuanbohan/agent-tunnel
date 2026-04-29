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

var brokerWriteTimeout = 2 * time.Second

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
	conn            net.Conn
	encoder         *json.Encoder
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
	c.mirror.WriteOutput(data)
	select {
	case c.notify <- struct{}{}:
	default:
	}
	return nil
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
		close(c.done)
		err = c.writeFrame(BrokerFrame{
			Type:      brokerFrameSessionGone,
			SessionID: c.session.SessionID,
			UpdatedAt: protocol.UnixTimestamp(c.now().UTC()),
		})
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
	c.lastSentPreview = ""
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
			c.encoder = nil
		}
		c.mu.Unlock()
	}()

	session := c.session
	session.UpdatedAt = protocol.UnixTimestamp(c.now().UTC())
	if err := c.writeFrame(BrokerFrame{Type: brokerFrameRegisterSession, Session: &session}); err != nil {
		return err
	}

	readErr := make(chan error, 1)
	go func() {
		var frame BrokerFrame
		err := json.NewDecoder(conn).Decode(&frame)
		if err == nil {
			err = errors.New("broker connection closed")
		}
		readErr <- err
	}()

	var timer *time.Timer
	var timerC <-chan time.Time
	var pending string
	var lastSentAt time.Time
	sendNow := func(preview string) (bool, error) {
		c.mu.Lock()
		if preview == c.lastSentPreview {
			c.mu.Unlock()
			return false, nil
		}
		c.lastSentPreview = preview
		c.mu.Unlock()
		now := c.now().UTC()
		if err := c.writeFrame(BrokerFrame{
			Type:      brokerFramePreviewUpdate,
			SessionID: c.session.SessionID,
			Preview:   preview,
			UpdatedAt: protocol.UnixTimestamp(now),
		}); err != nil {
			return false, err
		}
		lastSentAt = now
		return true, nil
	}
	if preview := c.previewSnapshot(); preview != "" {
		if _, err := sendNow(preview); err != nil {
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
		if pending == "" || timerC != nil {
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
		case <-c.notify:
			preview := c.previewSnapshot()
			if c.throttle <= 0 {
				if _, err := sendNow(preview); err != nil {
					return err
				}
				continue
			}
			now := c.now().UTC()
			if lastSentAt.IsZero() || now.Sub(lastSentAt) >= c.throttle {
				stopTimer()
				pending = ""
				if _, err := sendNow(preview); err != nil {
					return err
				}
				continue
			}
			pending = preview
			schedulePending(now)
		case <-timerC:
			timerC = nil
			if _, err := sendNow(pending); err != nil {
				return err
			}
			pending = ""
		}
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
