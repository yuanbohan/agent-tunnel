package connectivity

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"yuanbohan/tunnel/internal/protocol"
)

var (
	ErrConnectivityPeerInactive   = errors.New("connectivity peer inactive")
	ErrPairingCorrelationNotFound = errors.New("pairing correlation not found")
	ErrRelayTunnelUnavailable     = errors.New("relay tunnel unavailable")
	ErrRelayTunnelRateLimited     = errors.New("relay tunnel rate limited")
	ErrRelayTunnelTokenInvalid    = errors.New("relay tunnel token invalid")
)

const (
	TunnelActorAndroid = "android"
	TunnelActorDaemon  = "daemon"

	RelayTunnelRequestLimit               = 10
	RelayTunnelRequestWindow              = time.Minute
	RelayTunnelInFlightPerAppSessionLimit = 4
)

type AppPeer interface {
	SendJSON(any) error
	Close() error
}

type DaemonPeer interface {
	SendJSON(any) error
	Close() error
}

type AppOwner struct {
	UserID            int64
	AppSessionID      string
	DeviceFingerprint string
	SessionCreatedAt  time.Time
}

type DaemonOwner struct {
	UserID       int64
	AgentTokenID string
}

type Registry struct {
	mu             sync.RWMutex
	apps           map[AppPeer]liveApp
	daemons        map[string]*liveDaemon
	correlations   map[string]pairCorrelation
	attempts       map[string]*tunnelAttempt
	tokens         map[string]tunnelToken
	tunnelRequests map[int64][]time.Time
	revokedApps    map[string]struct{}
	revokedAgents  map[string]struct{}
	userAppCutoff  map[int64]time.Time
	nextCloserID   int64
	now            func() time.Time
}

type liveApp struct {
	owner       AppOwner
	peer        AppPeer
	connectedAt time.Time
}

type liveDaemon struct {
	info        protocol.ConnectivityDaemonInfo
	owner       DaemonOwner
	peer        DaemonPeer
	trusted     map[string]protocol.ConnectivityTrustedAndroid
	connectedAt time.Time
}

type pairCorrelation struct {
	id        string
	owner     DaemonOwner
	deviceID  string
	peer      DaemonPeer
	expiresAt time.Time
}

type tunnelAttempt struct {
	key               string
	id                string
	appOwner          AppOwner
	daemonOwner       DaemonOwner
	daemonDeviceID    string
	daemonFingerprint string
	daemonPeer        DaemonPeer
	appToken          string
	daemonToken       string
	appRedeemed       bool
	daemonRedeemed    bool
	active            bool
	expiresAt         time.Time
	closers           map[int64]func()
}

type tunnelToken struct {
	attemptID string
	actor     string
}

type TunnelRedemption struct {
	TunnelKey         string
	AttemptID         string
	Actor             string
	UserID            int64
	AppSessionID      string
	DeviceFingerprint string
	DaemonDeviceID    string
	DaemonFingerprint string
}

func NewRegistry() *Registry {
	return &Registry{
		apps:           make(map[AppPeer]liveApp),
		daemons:        make(map[string]*liveDaemon),
		correlations:   make(map[string]pairCorrelation),
		attempts:       make(map[string]*tunnelAttempt),
		tokens:         make(map[string]tunnelToken),
		tunnelRequests: make(map[int64][]time.Time),
		revokedApps:    make(map[string]struct{}),
		revokedAgents:  make(map[string]struct{}),
		userAppCutoff:  make(map[int64]time.Time),
		now:            func() time.Time { return time.Now().UTC() },
	}
}

func (r *Registry) RegisterApp(owner AppOwner, peer AppPeer) []protocol.ConnectivityDaemonInfo {
	snapshot, _ := r.RegisterAppIfValid(owner, peer, nil)
	return snapshot
}

func (r *Registry) RegisterAppIfValid(owner AppOwner, peer AppPeer, stillValid func() bool) ([]protocol.ConnectivityDaemonInfo, bool) {
	owner.DeviceFingerprint = normalizeFingerprint(owner.DeviceFingerprint)
	if stillValid != nil && !stillValid() {
		return nil, false
	}
	r.mu.Lock()
	if r.appOwnerRevokedLocked(owner) {
		r.mu.Unlock()
		return nil, false
	}
	r.apps[peer] = liveApp{owner: owner, peer: peer, connectedAt: r.now()}
	snapshot := r.visibleDaemonsLocked(owner)
	r.mu.Unlock()
	return snapshot, true
}

func (r *Registry) DisconnectApp(peer AppPeer) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.apps[peer]; !ok {
		return false
	}
	delete(r.apps, peer)
	_ = peer.Close()
	return true
}

func (r *Registry) RegisterDaemon(owner DaemonOwner, info protocol.ConnectivityDaemonInfo, trusted []protocol.ConnectivityTrustedAndroid, peer DaemonPeer) []AppPeer {
	appPeers, _ := r.RegisterDaemonIfValid(owner, info, trusted, peer, nil)
	return appPeers
}

func (r *Registry) RegisterDaemonIfValid(owner DaemonOwner, info protocol.ConnectivityDaemonInfo, trusted []protocol.ConnectivityTrustedAndroid, peer DaemonPeer, stillValid func() bool) ([]AppPeer, bool) {
	info.DeviceID = strings.TrimSpace(info.DeviceID)
	info.DaemonFingerprint = normalizeFingerprint(info.DaemonFingerprint)
	trustedMap := trustedMapFromSlice(trusted)
	key := daemonMapKey(owner.UserID, info.DeviceID)

	if stillValid != nil && !stillValid() {
		return nil, false
	}
	r.mu.Lock()
	if _, revoked := r.revokedAgents[owner.AgentTokenID]; revoked {
		r.mu.Unlock()
		return nil, false
	}
	old := r.daemons[key]
	var removed []AppPeer
	var oldInfo protocol.ConnectivityDaemonInfo
	var closers []func()
	if old != nil && old.peer != peer {
		delete(r.daemons, key)
		r.removeCorrelationsForDaemonLocked(key, old.peer)
		closers = r.removeTunnelAttemptsLocked(func(attempt *tunnelAttempt) bool {
			return attempt.daemonPeer == old.peer
		})
		oldInfo = old.info
		removed = r.lostAppPeersLocked(old.owner.UserID, old.trusted, trustedMap)
	}
	r.daemons[key] = &liveDaemon{
		info:        info,
		owner:       owner,
		peer:        peer,
		trusted:     trustedMap,
		connectedAt: r.now(),
	}
	appPeers := r.matchingAppPeersLocked(owner.UserID, trustedMap)
	r.mu.Unlock()

	closeTunnelClosers(closers)
	if old != nil && old.peer != peer {
		_ = old.peer.Close()
	}
	for _, appPeer := range removed {
		_ = appPeer.SendJSON(protocol.ConnectivityDaemonRemovedFrame(oldInfo.DeviceID, oldInfo.DaemonFingerprint))
	}
	for _, appPeer := range appPeers {
		_ = appPeer.SendJSON(protocol.ConnectivityDaemonUpsertFrame(info))
	}
	return appPeers, true
}

func (r *Registry) DisconnectDaemon(deviceID string, peer DaemonPeer) bool {
	r.mu.Lock()
	key, live, ok := r.daemonByDevicePeerLocked(deviceID, peer)
	if !ok {
		r.mu.Unlock()
		return false
	}
	delete(r.daemons, key)
	r.removeCorrelationsForDaemonLocked(key, peer)
	closers := r.removeTunnelAttemptsLocked(func(attempt *tunnelAttempt) bool {
		return attempt.daemonPeer == peer
	})
	appPeers := r.matchingAppPeersLocked(live.owner.UserID, live.trusted)
	info := live.info
	r.mu.Unlock()

	closeTunnelClosers(closers)
	for _, appPeer := range appPeers {
		_ = appPeer.SendJSON(protocol.ConnectivityDaemonRemovedFrame(info.DeviceID, info.DaemonFingerprint))
	}
	_ = peer.Close()
	return true
}

func (r *Registry) RevokeTrustedAndroid(deviceID string, peer DaemonPeer, fingerprint string) bool {
	fingerprint = normalizeFingerprint(fingerprint)
	r.mu.Lock()
	_, live, ok := r.daemonByDevicePeerLocked(deviceID, peer)
	if !ok {
		r.mu.Unlock()
		return false
	}
	delete(live.trusted, fingerprint)
	closers := r.removeTunnelAttemptsLocked(func(attempt *tunnelAttempt) bool {
		return attempt.daemonPeer == peer && attempt.appOwner.DeviceFingerprint == fingerprint
	})
	appPeers := r.matchingSingleAppPeerLocked(live.owner.UserID, fingerprint)
	info := live.info
	r.mu.Unlock()

	closeTunnelClosers(closers)
	for _, appPeer := range appPeers {
		_ = appPeer.SendJSON(protocol.ConnectivityPairedDeviceRevokedFrame(info.DeviceID, info.DaemonFingerprint))
	}
	return true
}

func (r *Registry) CompletePairing(deviceID string, peer DaemonPeer, trusted protocol.ConnectivityTrustedAndroid) bool {
	trusted.Fingerprint = normalizeFingerprint(trusted.Fingerprint)
	if trusted.Fingerprint == "" {
		return false
	}
	r.mu.Lock()
	_, live, ok := r.daemonByDevicePeerLocked(deviceID, peer)
	if !ok {
		r.mu.Unlock()
		return false
	}
	live.trusted[trusted.Fingerprint] = trusted
	appPeers := r.matchingSingleAppPeerLocked(live.owner.UserID, trusted.Fingerprint)
	info := live.info
	r.mu.Unlock()

	for _, appPeer := range appPeers {
		_ = appPeer.SendJSON(protocol.ConnectivityDaemonUpsertFrame(info))
	}
	return true
}

func (r *Registry) ReservePairing(deviceID string, owner DaemonOwner, peer DaemonPeer, correlationID string, ttl time.Duration) bool {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := daemonMapKey(owner.UserID, deviceID)
	live, ok := r.daemons[key]
	if !ok || live.owner != owner || live.peer != peer {
		return false
	}
	r.correlations[pairCorrelationKey(owner.UserID, correlationID)] = pairCorrelation{
		id:        correlationID,
		owner:     owner,
		deviceID:  key,
		peer:      peer,
		expiresAt: r.now().Add(ttl),
	}
	return true
}

func (r *Registry) ForwardPairingResponse(userID int64, correlationID string, response protocol.ConnectivityPairingResponse) error {
	return r.forwardPairingResponse(userID, nil, AppOwner{}, correlationID, response)
}

func (r *Registry) ForwardPairingResponseFromApp(owner AppOwner, peer AppPeer, correlationID string, response protocol.ConnectivityPairingResponse) error {
	owner.DeviceFingerprint = normalizeFingerprint(owner.DeviceFingerprint)
	return r.forwardPairingResponse(owner.UserID, peer, owner, correlationID, response)
}

func (r *Registry) forwardPairingResponse(userID int64, appPeer AppPeer, owner AppOwner, correlationID string, response protocol.ConnectivityPairingResponse) error {
	correlationID = strings.TrimSpace(correlationID)
	r.mu.Lock()
	if appPeer != nil && !r.appPeerActiveLocked(owner, appPeer) {
		r.mu.Unlock()
		return ErrPairingCorrelationNotFound
	}
	key := pairCorrelationKey(userID, correlationID)
	correlation, ok := r.correlations[key]
	if !ok || !correlation.expiresAt.After(r.now()) {
		if ok {
			delete(r.correlations, key)
		}
		r.mu.Unlock()
		return ErrPairingCorrelationNotFound
	}
	peer := correlation.peer
	r.mu.Unlock()
	if err := peer.SendJSON(protocol.ConnectivityPairResponseForwardFrame(response)); err != nil {
		return ErrConnectivityPeerInactive
	}
	r.mu.Lock()
	if current, ok := r.correlations[key]; ok && current.peer == peer && current.owner.UserID == userID {
		delete(r.correlations, key)
	}
	r.mu.Unlock()
	return nil
}

func (r *Registry) DisconnectAppSession(appSessionID string, reason string) int {
	r.mu.Lock()
	r.revokedApps[strings.TrimSpace(appSessionID)] = struct{}{}
	var peers []AppPeer
	for peer, app := range r.apps {
		if app.owner.AppSessionID != appSessionID {
			continue
		}
		delete(r.apps, peer)
		peers = append(peers, peer)
	}
	closers := r.removeTunnelAttemptsLocked(func(attempt *tunnelAttempt) bool {
		return attempt.appOwner.AppSessionID == appSessionID
	})
	r.mu.Unlock()
	closeTunnelClosers(closers)
	for _, peer := range peers {
		_ = peer.Close()
	}
	return len(peers)
}

func (r *Registry) DisconnectUser(userID int64, reason string) int {
	r.mu.Lock()
	r.userAppCutoff[userID] = r.now()
	var appPeers []AppPeer
	var daemonPeers []DaemonPeer
	for peer, app := range r.apps {
		if app.owner.UserID == userID {
			delete(r.apps, peer)
			r.revokedApps[app.owner.AppSessionID] = struct{}{}
			appPeers = append(appPeers, peer)
		}
	}
	for key, live := range r.daemons {
		if live.owner.UserID == userID {
			delete(r.daemons, key)
			r.removeCorrelationsForDaemonLocked(key, live.peer)
			daemonPeers = append(daemonPeers, live.peer)
		}
	}
	closers := r.removeTunnelAttemptsLocked(func(attempt *tunnelAttempt) bool {
		return attempt.appOwner.UserID == userID || attempt.daemonOwner.UserID == userID
	})
	r.mu.Unlock()
	closeTunnelClosers(closers)
	for _, peer := range appPeers {
		_ = peer.Close()
	}
	for _, peer := range daemonPeers {
		_ = peer.Close()
	}
	return len(appPeers) + len(daemonPeers)
}

func (r *Registry) DisconnectAgentToken(agentTokenID string) int {
	r.mu.Lock()
	r.revokedAgents[strings.TrimSpace(agentTokenID)] = struct{}{}
	var peers []DaemonPeer
	for key, live := range r.daemons {
		if live.owner.AgentTokenID == agentTokenID {
			delete(r.daemons, key)
			r.removeCorrelationsForDaemonLocked(key, live.peer)
			peers = append(peers, live.peer)
		}
	}
	closers := r.removeTunnelAttemptsLocked(func(attempt *tunnelAttempt) bool {
		return attempt.daemonOwner.AgentTokenID == agentTokenID
	})
	r.mu.Unlock()
	closeTunnelClosers(closers)
	for _, peer := range peers {
		_ = peer.Close()
	}
	return len(peers)
}

func (r *Registry) RequestRelayTunnel(owner AppOwner, daemonID, attemptID, requestID string, ttl time.Duration) (protocol.ConnectivityFrame, error) {
	return r.requestRelayTunnel(owner, nil, daemonID, attemptID, requestID, ttl)
}

func (r *Registry) RequestRelayTunnelFromApp(owner AppOwner, peer AppPeer, daemonID, attemptID, requestID string, ttl time.Duration) (protocol.ConnectivityFrame, error) {
	return r.requestRelayTunnel(owner, peer, daemonID, attemptID, requestID, ttl)
}

func (r *Registry) requestRelayTunnel(owner AppOwner, appPeer AppPeer, daemonID, attemptID, requestID string, ttl time.Duration) (protocol.ConnectivityFrame, error) {
	owner.DeviceFingerprint = normalizeFingerprint(owner.DeviceFingerprint)
	daemonID = strings.TrimSpace(daemonID)
	attemptID = strings.TrimSpace(attemptID)
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	if owner.DeviceFingerprint == "" || daemonID == "" || attemptID == "" {
		return protocol.ConnectivityFrame{}, ErrRelayTunnelUnavailable
	}
	r.mu.Lock()
	_, sweepClosers := r.sweepExpiredTunnelAttemptsLocked()
	now := r.now()
	if appPeer != nil && !r.appPeerActiveLocked(owner, appPeer) {
		r.mu.Unlock()
		closeTunnelClosers(sweepClosers)
		return protocol.ConnectivityFrame{}, ErrRelayTunnelUnavailable
	}
	key := daemonMapKey(owner.UserID, daemonID)
	live := r.daemons[key]
	if live == nil || live.trusted[owner.DeviceFingerprint].Fingerprint == "" {
		r.mu.Unlock()
		closeTunnelClosers(sweepClosers)
		return protocol.ConnectivityFrame{}, ErrRelayTunnelUnavailable
	}
	tunnelKey := tunnelAttemptKey(owner, daemonID, attemptID)
	if !r.allowRelayTunnelRequestLocked(owner.UserID, now) {
		r.mu.Unlock()
		closeTunnelClosers(sweepClosers)
		return protocol.ConnectivityFrame{}, ErrRelayTunnelRateLimited
	}
	if r.countRelayTunnelAttemptsLocked(owner, tunnelKey) >= RelayTunnelInFlightPerAppSessionLimit {
		r.mu.Unlock()
		closeTunnelClosers(sweepClosers)
		return protocol.ConnectivityFrame{}, ErrRelayTunnelRateLimited
	}
	appToken, err := randomToken()
	if err != nil {
		r.mu.Unlock()
		closeTunnelClosers(sweepClosers)
		return protocol.ConnectivityFrame{}, err
	}
	daemonToken, err := randomToken()
	if err != nil {
		r.mu.Unlock()
		closeTunnelClosers(sweepClosers)
		return protocol.ConnectivityFrame{}, err
	}
	r.recordRelayTunnelRequestLocked(owner.UserID, now)
	var closers []func()
	if old := r.attempts[tunnelKey]; old != nil {
		closers = append(closers, r.removeTunnelAttemptLocked(old)...)
	}
	attempt := &tunnelAttempt{
		key:               tunnelKey,
		id:                attemptID,
		appOwner:          owner,
		daemonOwner:       live.owner,
		daemonDeviceID:    live.info.DeviceID,
		daemonFingerprint: live.info.DaemonFingerprint,
		daemonPeer:        live.peer,
		appToken:          appToken,
		daemonToken:       daemonToken,
		expiresAt:         r.now().Add(ttl),
		closers:           make(map[int64]func()),
	}
	r.attempts[tunnelKey] = attempt
	r.tokens[appToken] = tunnelToken{attemptID: tunnelKey, actor: TunnelActorAndroid}
	r.tokens[daemonToken] = tunnelToken{attemptID: tunnelKey, actor: TunnelActorDaemon}
	daemonPeer := live.peer
	daemonFrame := protocol.ConnectivityRelayTunnelReadyFrame(requestID, attemptID, TunnelActorDaemon, live.info.DeviceID, owner.DeviceFingerprint, daemonToken)
	appFrame := protocol.ConnectivityRelayTunnelReadyFrame(requestID, attemptID, TunnelActorAndroid, live.info.DeviceID, owner.DeviceFingerprint, appToken)
	r.mu.Unlock()

	closeTunnelClosers(sweepClosers)
	closeTunnelClosers(closers)
	r.expireUnpairedRelayTunnelLater(tunnelKey, ttl)
	if err := daemonPeer.SendJSON(daemonFrame); err != nil {
		r.mu.Lock()
		var attemptClosers []func()
		if current := r.attempts[tunnelKey]; current == attempt {
			attemptClosers = r.removeTunnelAttemptLocked(attempt)
		}
		r.mu.Unlock()
		closeTunnelClosers(attemptClosers)
		return protocol.ConnectivityFrame{}, ErrConnectivityPeerInactive
	}
	return appFrame, nil
}

func (r *Registry) RedeemRelayTunnelToken(token string) (TunnelRedemption, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return TunnelRedemption{}, ErrRelayTunnelTokenInvalid
	}
	r.mu.Lock()
	_, sweepClosers := r.sweepExpiredTunnelAttemptsLocked()
	stored, ok := r.tokens[token]
	if !ok {
		r.mu.Unlock()
		closeTunnelClosers(sweepClosers)
		return TunnelRedemption{}, ErrRelayTunnelTokenInvalid
	}
	attempt := r.attempts[stored.attemptID]
	if attempt == nil || !attempt.expiresAt.After(r.now()) {
		var closers []func()
		if attempt != nil {
			closers = r.removeTunnelAttemptLocked(attempt)
		} else {
			delete(r.tokens, token)
		}
		r.mu.Unlock()
		closeTunnelClosers(sweepClosers)
		closeTunnelClosers(closers)
		return TunnelRedemption{}, ErrRelayTunnelTokenInvalid
	}
	switch stored.actor {
	case TunnelActorAndroid:
		if attempt.appRedeemed {
			r.mu.Unlock()
			closeTunnelClosers(sweepClosers)
			return TunnelRedemption{}, ErrRelayTunnelTokenInvalid
		}
		attempt.appRedeemed = true
	case TunnelActorDaemon:
		if attempt.daemonRedeemed {
			r.mu.Unlock()
			closeTunnelClosers(sweepClosers)
			return TunnelRedemption{}, ErrRelayTunnelTokenInvalid
		}
		attempt.daemonRedeemed = true
	default:
		r.mu.Unlock()
		closeTunnelClosers(sweepClosers)
		return TunnelRedemption{}, ErrRelayTunnelTokenInvalid
	}
	delete(r.tokens, token)
	redemption := TunnelRedemption{
		TunnelKey:         attempt.key,
		AttemptID:         attempt.id,
		Actor:             stored.actor,
		UserID:            attempt.appOwner.UserID,
		AppSessionID:      attempt.appOwner.AppSessionID,
		DeviceFingerprint: attempt.appOwner.DeviceFingerprint,
		DaemonDeviceID:    attempt.daemonDeviceID,
		DaemonFingerprint: attempt.daemonFingerprint,
	}
	r.mu.Unlock()
	closeTunnelClosers(sweepClosers)
	return redemption, nil
}

func (r *Registry) MarkRelayTunnelActive(tunnelKey string) bool {
	tunnelKey = strings.TrimSpace(tunnelKey)
	if tunnelKey == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	attempt := r.attempts[tunnelKey]
	if attempt == nil {
		return false
	}
	attempt.active = true
	return true
}

func (r *Registry) CloseRelayTunnel(tunnelKey string) bool {
	tunnelKey = strings.TrimSpace(tunnelKey)
	if tunnelKey == "" {
		return false
	}
	r.mu.Lock()
	attempt := r.attempts[tunnelKey]
	closers := r.removeTunnelAttemptLocked(attempt)
	r.mu.Unlock()
	closeTunnelClosers(closers)
	return attempt != nil
}

func (r *Registry) SweepExpiredRelayTunnelAttempts() int {
	r.mu.Lock()
	removed, closers := r.sweepExpiredTunnelAttemptsLocked()
	r.mu.Unlock()
	closeTunnelClosers(closers)
	return removed
}

func (r *Registry) AddTunnelCloser(tunnelKey string, close func()) func() {
	if close == nil {
		return func() {}
	}
	tunnelKey = strings.TrimSpace(tunnelKey)
	r.mu.Lock()
	attempt := r.attempts[tunnelKey]
	if attempt == nil {
		r.mu.Unlock()
		close()
		return func() {}
	}
	r.nextCloserID++
	id := r.nextCloserID
	attempt.closers[id] = close
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		if current := r.attempts[tunnelKey]; current != nil {
			delete(current.closers, id)
		}
		r.mu.Unlock()
	}
}

func (r *Registry) visibleDaemonsLocked(owner AppOwner) []protocol.ConnectivityDaemonInfo {
	daemons := make([]*liveDaemon, 0, len(r.daemons))
	for _, live := range r.daemons {
		if live.owner.UserID != owner.UserID {
			continue
		}
		if _, ok := live.trusted[owner.DeviceFingerprint]; !ok {
			continue
		}
		daemons = append(daemons, live)
	}
	sort.Slice(daemons, func(i, j int) bool {
		if daemons[i].connectedAt.Equal(daemons[j].connectedAt) {
			return daemons[i].info.DeviceID < daemons[j].info.DeviceID
		}
		return daemons[i].connectedAt.After(daemons[j].connectedAt)
	})
	out := make([]protocol.ConnectivityDaemonInfo, 0, len(daemons))
	for _, live := range daemons {
		out = append(out, live.info)
	}
	return out
}

func (r *Registry) matchingAppPeersLocked(userID int64, trusted map[string]protocol.ConnectivityTrustedAndroid) []AppPeer {
	var peers []AppPeer
	for _, app := range r.apps {
		if app.owner.UserID != userID {
			continue
		}
		if _, ok := trusted[app.owner.DeviceFingerprint]; !ok {
			continue
		}
		peers = append(peers, app.peer)
	}
	return peers
}

func (r *Registry) matchingSingleAppPeerLocked(userID int64, fingerprint string) []AppPeer {
	var peers []AppPeer
	for _, app := range r.apps {
		if app.owner.UserID == userID && app.owner.DeviceFingerprint == fingerprint {
			peers = append(peers, app.peer)
		}
	}
	return peers
}

func (r *Registry) removeCorrelationsForDaemonLocked(deviceID string, peer DaemonPeer) {
	for id, correlation := range r.correlations {
		if correlation.deviceID == deviceID && correlation.peer == peer {
			delete(r.correlations, id)
		}
	}
}

func (r *Registry) removeTunnelAttemptsLocked(match func(*tunnelAttempt) bool) []func() {
	var closers []func()
	for _, attempt := range r.attempts {
		if !match(attempt) {
			continue
		}
		closers = append(closers, r.removeTunnelAttemptLocked(attempt)...)
	}
	return closers
}

func (r *Registry) removeTunnelAttemptLocked(attempt *tunnelAttempt) []func() {
	if attempt == nil {
		return nil
	}
	delete(r.attempts, attempt.key)
	delete(r.tokens, attempt.appToken)
	delete(r.tokens, attempt.daemonToken)
	closers := make([]func(), 0, len(attempt.closers))
	for _, closer := range attempt.closers {
		closers = append(closers, closer)
	}
	attempt.closers = nil
	return closers
}

func (r *Registry) sweepExpiredTunnelAttemptsLocked() (int, []func()) {
	var closers []func()
	removed := 0
	now := r.now()
	for _, attempt := range r.attempts {
		if attempt.active || attempt.expiresAt.After(now) {
			continue
		}
		removed++
		closers = append(closers, r.removeTunnelAttemptLocked(attempt)...)
	}
	return removed, closers
}

func (r *Registry) expireUnpairedRelayTunnelLater(tunnelKey string, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	time.AfterFunc(ttl, func() {
		r.mu.Lock()
		attempt := r.attempts[tunnelKey]
		var closers []func()
		if attempt != nil && !attempt.active && !attempt.expiresAt.After(r.now()) {
			closers = r.removeTunnelAttemptLocked(attempt)
		}
		r.mu.Unlock()
		closeTunnelClosers(closers)
	})
}

func (r *Registry) daemonByDevicePeerLocked(deviceID string, peer DaemonPeer) (string, *liveDaemon, bool) {
	for key, live := range r.daemons {
		if live.info.DeviceID == deviceID && live.peer == peer {
			return key, live, true
		}
	}
	return "", nil, false
}

func (r *Registry) appOwnerRevokedLocked(owner AppOwner) bool {
	if _, revoked := r.revokedApps[strings.TrimSpace(owner.AppSessionID)]; revoked {
		return true
	}
	cutoff, ok := r.userAppCutoff[owner.UserID]
	if !ok {
		return false
	}
	return owner.SessionCreatedAt.IsZero() || !owner.SessionCreatedAt.After(cutoff)
}

func (r *Registry) appPeerActiveLocked(owner AppOwner, peer AppPeer) bool {
	app, ok := r.apps[peer]
	if !ok {
		return false
	}
	return app.owner.UserID == owner.UserID &&
		app.owner.AppSessionID == owner.AppSessionID &&
		app.owner.DeviceFingerprint == normalizeFingerprint(owner.DeviceFingerprint) &&
		!r.appOwnerRevokedLocked(app.owner)
}

func (r *Registry) lostAppPeersLocked(userID int64, oldTrusted, newTrusted map[string]protocol.ConnectivityTrustedAndroid) []AppPeer {
	var peers []AppPeer
	for fingerprint := range oldTrusted {
		if _, stillTrusted := newTrusted[fingerprint]; stillTrusted {
			continue
		}
		peers = append(peers, r.matchingSingleAppPeerLocked(userID, fingerprint)...)
	}
	return peers
}

func daemonMapKey(userID int64, deviceID string) string {
	return fmt.Sprintf("%d:%s", userID, strings.TrimSpace(deviceID))
}

func pairCorrelationKey(userID int64, correlationID string) string {
	return fmt.Sprintf("%d:%s", userID, strings.TrimSpace(correlationID))
}

func tunnelAttemptKey(owner AppOwner, daemonID, attemptID string) string {
	return fmt.Sprintf("%d:%s:%s:%s:%s",
		owner.UserID,
		strings.TrimSpace(owner.AppSessionID),
		normalizeFingerprint(owner.DeviceFingerprint),
		strings.TrimSpace(daemonID),
		strings.TrimSpace(attemptID),
	)
}

func (r *Registry) allowRelayTunnelRequestLocked(userID int64, now time.Time) bool {
	return len(r.recentRelayTunnelRequestsLocked(userID, now)) < RelayTunnelRequestLimit
}

func (r *Registry) recordRelayTunnelRequestLocked(userID int64, now time.Time) {
	requests := r.recentRelayTunnelRequestsLocked(userID, now)
	requests = append(requests, now)
	r.tunnelRequests[userID] = requests
}

func (r *Registry) recentRelayTunnelRequestsLocked(userID int64, now time.Time) []time.Time {
	requests := r.tunnelRequests[userID]
	if len(requests) == 0 {
		return nil
	}
	cutoff := now.Add(-RelayTunnelRequestWindow)
	kept := requests[:0]
	for _, requestedAt := range requests {
		if requestedAt.After(cutoff) {
			kept = append(kept, requestedAt)
		}
	}
	if len(kept) == 0 {
		delete(r.tunnelRequests, userID)
		return nil
	}
	r.tunnelRequests[userID] = kept
	return kept
}

func (r *Registry) countRelayTunnelAttemptsLocked(owner AppOwner, excludingTunnelKey string) int {
	count := 0
	for key, attempt := range r.attempts {
		if key == excludingTunnelKey {
			continue
		}
		if attempt.appOwner.UserID == owner.UserID &&
			attempt.appOwner.AppSessionID == owner.AppSessionID &&
			attempt.appOwner.DeviceFingerprint == owner.DeviceFingerprint {
			count++
		}
	}
	return count
}

func trustedMapFromSlice(trusted []protocol.ConnectivityTrustedAndroid) map[string]protocol.ConnectivityTrustedAndroid {
	out := make(map[string]protocol.ConnectivityTrustedAndroid, len(trusted))
	for _, device := range trusted {
		device.Fingerprint = normalizeFingerprint(device.Fingerprint)
		if device.Fingerprint == "" {
			continue
		}
		out[device.Fingerprint] = device
	}
	return out
}

func normalizeFingerprint(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func randomToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func closeTunnelClosers(closers []func()) {
	for _, closer := range closers {
		closer()
	}
}
