package connectivity

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
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
	ErrRendezvousUnavailable      = errors.New("rendezvous unavailable")
	ErrRendezvousRateLimited      = errors.New("rendezvous rate limited")
)

const (
	TunnelActorClient = "client"
	TunnelActorDaemon = "daemon"

	RelayTunnelRequestLimit               = 10
	RelayTunnelRequestWindow              = time.Minute
	RelayTunnelInFlightPerAppSessionLimit = 4
	RendezvousCandidateLimit              = 8
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
	activeDirect   map[string]*directSession
	rendezvous     map[string]*rendezvousAttempt
	tokens         map[string]tunnelToken
	tunnelRequests map[int64][]time.Time
	rendezvousReqs map[int64][]time.Time
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
	fallbackReason    string
	directLatencyMS   int
	relayLatencyMS    int
	appRedeemed       bool
	daemonRedeemed    bool
	active            bool
	expiresAt         time.Time
	closers           map[int64]func()
}

type rendezvousAttempt struct {
	key               string
	id                string
	appOwner          AppOwner
	appPeer           AppPeer
	daemonOwner       DaemonOwner
	daemonDeviceID    string
	daemonFingerprint string
	daemonPeer        DaemonPeer
	expiresAt         time.Time
}

type directSession struct {
	key                string
	id                 string
	appOwner           AppOwner
	daemonOwner        DaemonOwner
	daemonDeviceID     string
	daemonFingerprint  string
	daemonPeer         DaemonPeer
	androidFingerprint string
}

type jsonPeer interface {
	SendJSON(any) error
	Close() error
}

type rendezvousCloseNotification struct {
	peer           jsonPeer
	frame          protocol.ConnectivityFrame
	closeOnFailure bool
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
		activeDirect:   make(map[string]*directSession),
		rendezvous:     make(map[string]*rendezvousAttempt),
		tokens:         make(map[string]tunnelToken),
		tunnelRequests: make(map[int64][]time.Time),
		rendezvousReqs: make(map[int64][]time.Time),
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
	app, ok := r.apps[peer]
	if !ok {
		r.mu.Unlock()
		return false
	}
	delete(r.apps, peer)
	notifications := r.removeRendezvousLocked(func(attempt *rendezvousAttempt) bool {
		return attempt.appPeer == peer ||
			(attempt.appOwner.UserID == app.owner.UserID &&
				attempt.appOwner.AppSessionID == app.owner.AppSessionID &&
				attempt.appOwner.DeviceFingerprint == app.owner.DeviceFingerprint)
	})
	r.mu.Unlock()

	sendRendezvousCloseNotifications(notifications)
	_ = peer.Close()
	return true
}

func (r *Registry) RegisterDaemon(owner DaemonOwner, info protocol.ConnectivityDaemonInfo, trusted []protocol.ConnectivityTrustedAndroid, peer DaemonPeer) []AppPeer {
	appPeers, _ := r.RegisterDaemonIfValid(owner, info, trusted, peer, nil)
	return appPeers
}

func (r *Registry) RegisterDaemonIfValid(owner DaemonOwner, info protocol.ConnectivityDaemonInfo, trusted []protocol.ConnectivityTrustedAndroid, peer DaemonPeer, stillValid func() bool) ([]AppPeer, bool) {
	return r.RegisterDaemonIfValidWithDirectSessions(owner, info, trusted, nil, peer, stillValid)
}

func (r *Registry) RegisterDaemonIfValidWithDirectSessions(owner DaemonOwner, info protocol.ConnectivityDaemonInfo, trusted []protocol.ConnectivityTrustedAndroid, directSessions []protocol.ConnectivityDirectSession, peer DaemonPeer, stillValid func() bool) ([]AppPeer, bool) {
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
	var notifications []rendezvousCloseNotification
	if old != nil && old.peer != peer {
		delete(r.daemons, key)
		r.removeCorrelationsForDaemonLocked(key, old.peer)
		notifications = r.removeRendezvousLocked(func(attempt *rendezvousAttempt) bool {
			return attempt.daemonPeer == old.peer
		})
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
	notifications = append(notifications, r.reconcileDirectSessionsLocked(owner, info, trustedMap, directSessions, peer)...)
	r.mu.Unlock()

	sendRendezvousCloseNotifications(notifications)
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
	notifications := r.removeRendezvousLocked(func(attempt *rendezvousAttempt) bool {
		return attempt.daemonPeer == peer
	})
	closers := r.removeTunnelAttemptsLocked(func(attempt *tunnelAttempt) bool {
		return attempt.daemonPeer == peer
	})
	appPeers := r.matchingAppPeersLocked(live.owner.UserID, live.trusted)
	info := live.info
	r.mu.Unlock()

	sendRendezvousCloseNotifications(notifications)
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
	notifications := r.removeRendezvousLocked(func(attempt *rendezvousAttempt) bool {
		return attempt.daemonPeer == peer && attempt.appOwner.DeviceFingerprint == fingerprint
	})
	notifications = append(notifications, r.removeActiveDirectLocked(func(session *directSession) bool {
		return session.daemonPeer == peer && session.androidFingerprint == fingerprint
	})...)
	closers := r.removeTunnelAttemptsLocked(func(attempt *tunnelAttempt) bool {
		return attempt.daemonPeer == peer && attempt.appOwner.DeviceFingerprint == fingerprint
	})
	appPeers := r.matchingSingleAppPeerLocked(live.owner.UserID, fingerprint)
	info := live.info
	r.mu.Unlock()

	sendRendezvousCloseNotifications(notifications)
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
	r.sweepExpiredPairCorrelationsLocked()
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
	if owner.AppSessionID != "" && r.appOwnerRevokedLocked(owner) {
		r.mu.Unlock()
		return ErrPairingCorrelationNotFound
	}
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

func (r *Registry) DisconnectAppSession(appSessionID string) int {
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
	notifications := r.removeRendezvousLocked(func(attempt *rendezvousAttempt) bool {
		return attempt.appOwner.AppSessionID == appSessionID
	})
	notifications = append(notifications, r.removeActiveDirectLocked(func(session *directSession) bool {
		return session.appOwner.AppSessionID == appSessionID
	})...)
	closers := r.removeTunnelAttemptsLocked(func(attempt *tunnelAttempt) bool {
		return attempt.appOwner.AppSessionID == appSessionID
	})
	r.mu.Unlock()
	sendRendezvousCloseNotifications(notifications)
	closeTunnelClosers(closers)
	for _, peer := range peers {
		_ = peer.Close()
	}
	return len(peers)
}

func (r *Registry) DisconnectUser(userID int64) int {
	r.mu.Lock()
	r.userAppCutoff[userID] = r.now()
	var appPeers []AppPeer
	var daemonPeers []DaemonPeer
	var notifications []rendezvousCloseNotification
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
			notifications = append(notifications, r.removeRendezvousLocked(func(attempt *rendezvousAttempt) bool {
				return attempt.daemonPeer == live.peer
			})...)
			notifications = append(notifications, r.removeActiveDirectLocked(func(session *directSession) bool {
				return session.daemonPeer == live.peer
			})...)
			daemonPeers = append(daemonPeers, live.peer)
		}
	}
	notifications = append(notifications, r.removeRendezvousLocked(func(attempt *rendezvousAttempt) bool {
		return attempt.appOwner.UserID == userID || attempt.daemonOwner.UserID == userID
	})...)
	notifications = append(notifications, r.removeActiveDirectLocked(func(session *directSession) bool {
		return session.appOwner.UserID == userID || session.daemonOwner.UserID == userID
	})...)
	closers := r.removeTunnelAttemptsLocked(func(attempt *tunnelAttempt) bool {
		return attempt.appOwner.UserID == userID || attempt.daemonOwner.UserID == userID
	})
	r.mu.Unlock()
	sendRendezvousCloseNotifications(notifications)
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
	var notifications []rendezvousCloseNotification
	for key, live := range r.daemons {
		if live.owner.AgentTokenID == agentTokenID {
			delete(r.daemons, key)
			r.removeCorrelationsForDaemonLocked(key, live.peer)
			notifications = append(notifications, r.removeRendezvousLocked(func(attempt *rendezvousAttempt) bool {
				return attempt.daemonPeer == live.peer
			})...)
			notifications = append(notifications, r.removeActiveDirectLocked(func(session *directSession) bool {
				return session.daemonPeer == live.peer
			})...)
			peers = append(peers, live.peer)
		}
	}
	notifications = append(notifications, r.removeRendezvousLocked(func(attempt *rendezvousAttempt) bool {
		return attempt.daemonOwner.AgentTokenID == agentTokenID
	})...)
	notifications = append(notifications, r.removeActiveDirectLocked(func(session *directSession) bool {
		return session.daemonOwner.AgentTokenID == agentTokenID
	})...)
	closers := r.removeTunnelAttemptsLocked(func(attempt *tunnelAttempt) bool {
		return attempt.daemonOwner.AgentTokenID == agentTokenID
	})
	r.mu.Unlock()
	sendRendezvousCloseNotifications(notifications)
	closeTunnelClosers(closers)
	for _, peer := range peers {
		_ = peer.Close()
	}
	return len(peers)
}

func (r *Registry) OpenRendezvousFromApp(owner AppOwner, peer AppPeer, daemonID, attemptID, requestID, publicUDPAddr string, privateUDPAddrs []string, ttl time.Duration) (protocol.ConnectivityFrame, error) {
	owner.DeviceFingerprint = normalizeFingerprint(owner.DeviceFingerprint)
	daemonID = strings.TrimSpace(daemonID)
	attemptID = strings.TrimSpace(attemptID)
	publicUDPAddr = strings.TrimSpace(publicUDPAddr)
	privateUDPAddrs = normalizeCandidateList(privateUDPAddrs)
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	if owner.DeviceFingerprint == "" || daemonID == "" || attemptID == "" || !validCandidatePayload(publicUDPAddr, privateUDPAddrs) {
		return protocol.ConnectivityFrame{}, ErrRendezvousUnavailable
	}

	r.mu.Lock()
	r.sweepExpiredRendezvousLocked()
	now := r.now()
	if !r.appPeerActiveLocked(owner, peer) {
		r.mu.Unlock()
		return protocol.ConnectivityFrame{}, ErrRendezvousUnavailable
	}
	key := daemonMapKey(owner.UserID, daemonID)
	live := r.daemons[key]
	if live == nil || live.trusted[owner.DeviceFingerprint].Fingerprint == "" {
		r.mu.Unlock()
		return protocol.ConnectivityFrame{}, ErrRendezvousUnavailable
	}
	if !r.allowRendezvousRequestLocked(owner.UserID, now) {
		r.mu.Unlock()
		return protocol.ConnectivityFrame{}, ErrRendezvousRateLimited
	}
	r.recordRendezvousRequestLocked(owner.UserID, now)
	attemptKey := tunnelAttemptKey(owner, daemonID, attemptID)
	notifications := r.removeRendezvousLocked(func(attempt *rendezvousAttempt) bool {
		return attempt.appOwner.UserID == owner.UserID &&
			attempt.appOwner.AppSessionID == owner.AppSessionID &&
			attempt.appOwner.DeviceFingerprint == owner.DeviceFingerprint &&
			attempt.daemonDeviceID == daemonID
	})
	expiresAt := now.Add(ttl)
	attempt := &rendezvousAttempt{
		key:               attemptKey,
		id:                attemptID,
		appOwner:          owner,
		appPeer:           peer,
		daemonOwner:       live.owner,
		daemonDeviceID:    live.info.DeviceID,
		daemonFingerprint: live.info.DaemonFingerprint,
		daemonPeer:        live.peer,
		expiresAt:         expiresAt,
	}
	r.rendezvous[attemptKey] = attempt
	daemonPeer := live.peer
	daemonFrame := protocol.ConnectivityRendezvousHintFrame(requestID, attemptID, TunnelActorClient, live.info.DeviceID, owner.DeviceFingerprint, publicUDPAddr, privateUDPAddrs, expiresAt.Unix())
	r.mu.Unlock()

	sendRendezvousCloseNotifications(notifications)
	r.expireRendezvousLater(attemptKey, ttl)
	if err := daemonPeer.SendJSON(daemonFrame); err != nil {
		r.mu.Lock()
		if current := r.rendezvous[attemptKey]; current == attempt {
			delete(r.rendezvous, attemptKey)
		}
		r.mu.Unlock()
		return protocol.ConnectivityFrame{}, ErrConnectivityPeerInactive
	}
	return daemonFrame, nil
}

func (r *Registry) ForwardRendezvousHintFromDaemon(owner DaemonOwner, deviceID string, peer DaemonPeer, attemptID, requestID, androidFingerprint, publicUDPAddr string, privateUDPAddrs []string) error {
	deviceID = strings.TrimSpace(deviceID)
	attemptID = strings.TrimSpace(attemptID)
	androidFingerprint = normalizeFingerprint(androidFingerprint)
	publicUDPAddr = strings.TrimSpace(publicUDPAddr)
	privateUDPAddrs = normalizeCandidateList(privateUDPAddrs)
	if deviceID == "" || attemptID == "" || androidFingerprint == "" || !validCandidatePayload(publicUDPAddr, privateUDPAddrs) {
		return ErrRendezvousUnavailable
	}

	r.mu.Lock()
	r.sweepExpiredRendezvousLocked()
	_, live, ok := r.daemonByDevicePeerLocked(deviceID, peer)
	if !ok || live.owner != owner {
		r.mu.Unlock()
		return ErrRendezvousUnavailable
	}
	var attempt *rendezvousAttempt
	for _, candidate := range r.rendezvous {
		if candidate.id == attemptID &&
			candidate.daemonPeer == peer &&
			candidate.daemonOwner == owner &&
			candidate.daemonDeviceID == deviceID &&
			candidate.appOwner.DeviceFingerprint == androidFingerprint {
			attempt = candidate
			break
		}
	}
	if attempt == nil || !attempt.expiresAt.After(r.now()) {
		r.mu.Unlock()
		return ErrRendezvousUnavailable
	}
	appPeer := attempt.appPeer
	appFrame := protocol.ConnectivityRendezvousHintFrame(requestID, attemptID, TunnelActorDaemon, attempt.daemonDeviceID, attempt.appOwner.DeviceFingerprint, publicUDPAddr, privateUDPAddrs, attempt.expiresAt.Unix())
	r.mu.Unlock()

	if err := appPeer.SendJSON(appFrame); err != nil {
		return ErrConnectivityPeerInactive
	}
	return nil
}

func (r *Registry) CloseRendezvousFromApp(owner AppOwner, peer AppPeer, daemonID, attemptID string) bool {
	owner.DeviceFingerprint = normalizeFingerprint(owner.DeviceFingerprint)
	daemonID = strings.TrimSpace(daemonID)
	attemptID = strings.TrimSpace(attemptID)
	r.mu.Lock()
	if !r.appPeerActiveLocked(owner, peer) {
		r.mu.Unlock()
		return false
	}
	notifications := r.removeRendezvousLocked(func(attempt *rendezvousAttempt) bool {
		if attempt.appPeer != peer ||
			attempt.appOwner.UserID != owner.UserID ||
			attempt.appOwner.AppSessionID != owner.AppSessionID ||
			attempt.appOwner.DeviceFingerprint != owner.DeviceFingerprint ||
			attempt.id != attemptID {
			return false
		}
		return daemonID == "" || attempt.daemonDeviceID == daemonID
	})
	r.mu.Unlock()
	if len(notifications) == 0 {
		return false
	}
	sendRendezvousCloseNotifications(notifications)
	return true
}

func (r *Registry) CloseRendezvousFromDaemon(owner DaemonOwner, deviceID string, peer DaemonPeer, attemptID, androidFingerprint string) bool {
	deviceID = strings.TrimSpace(deviceID)
	attemptID = strings.TrimSpace(attemptID)
	androidFingerprint = normalizeFingerprint(androidFingerprint)
	r.mu.Lock()
	if _, live, ok := r.daemonByDevicePeerLocked(deviceID, peer); !ok || live.owner != owner {
		r.mu.Unlock()
		return false
	}
	var notifications []rendezvousCloseNotification
	for key, attempt := range r.rendezvous {
		if attempt.id == attemptID &&
			attempt.daemonPeer == peer &&
			attempt.daemonOwner == owner &&
			attempt.daemonDeviceID == deviceID &&
			attempt.appOwner.DeviceFingerprint == androidFingerprint {
			delete(r.rendezvous, key)
			if attempt.appPeer != nil {
				notifications = append(notifications, rendezvousCloseNotification{
					peer: attempt.appPeer,
					frame: protocol.ConnectivityRendezvousCloseFrame(
						"",
						attempt.id,
						TunnelActorDaemon,
						attempt.daemonDeviceID,
						attempt.appOwner.DeviceFingerprint,
					),
				})
			}
			r.mu.Unlock()
			sendRendezvousCloseNotifications(notifications)
			return true
		}
	}
	r.mu.Unlock()
	return false
}

func (r *Registry) OpenDirectSessionFromDaemon(owner DaemonOwner, deviceID string, peer DaemonPeer, attemptID, requestID, androidFingerprint string) bool {
	deviceID = strings.TrimSpace(deviceID)
	attemptID = strings.TrimSpace(attemptID)
	androidFingerprint = normalizeFingerprint(androidFingerprint)
	if deviceID == "" || attemptID == "" || androidFingerprint == "" {
		return false
	}
	r.mu.Lock()
	r.sweepExpiredRendezvousLocked()
	_, live, ok := r.daemonByDevicePeerLocked(deviceID, peer)
	if !ok || live.owner != owner {
		r.mu.Unlock()
		return false
	}
	for key, attempt := range r.rendezvous {
		if attempt.id != attemptID ||
			attempt.daemonPeer != peer ||
			attempt.daemonOwner != owner ||
			attempt.daemonDeviceID != deviceID ||
			attempt.appOwner.DeviceFingerprint != androidFingerprint {
			continue
		}
		tunnelKey := tunnelAttemptKey(attempt.appOwner, deviceID, attemptID)
		if r.attempts[tunnelKey] != nil || r.activeDirect[tunnelKey] != nil || r.appOwnerRevokedLocked(attempt.appOwner) {
			r.mu.Unlock()
			return false
		}
		delete(r.rendezvous, key)
		r.activeDirect[tunnelKey] = &directSession{
			key:                tunnelKey,
			id:                 attemptID,
			appOwner:           attempt.appOwner,
			daemonOwner:        attempt.daemonOwner,
			daemonDeviceID:     attempt.daemonDeviceID,
			daemonFingerprint:  attempt.daemonFingerprint,
			daemonPeer:         peer,
			androidFingerprint: attempt.appOwner.DeviceFingerprint,
		}
		r.mu.Unlock()
		return true
	}
	r.mu.Unlock()
	return false
}

func (r *Registry) CloseDirectSessionFromDaemon(owner DaemonOwner, deviceID string, peer DaemonPeer, attemptID, androidFingerprint string) bool {
	deviceID = strings.TrimSpace(deviceID)
	attemptID = strings.TrimSpace(attemptID)
	androidFingerprint = normalizeFingerprint(androidFingerprint)
	r.mu.Lock()
	for key, session := range r.activeDirect {
		if session.id == attemptID &&
			session.daemonPeer == peer &&
			session.daemonOwner == owner &&
			session.daemonDeviceID == deviceID &&
			session.androidFingerprint == androidFingerprint {
			delete(r.activeDirect, key)
			r.mu.Unlock()
			return true
		}
	}
	r.mu.Unlock()
	return false
}

func (r *Registry) RequestRelayTunnel(owner AppOwner, daemonID, attemptID, requestID string, ttl time.Duration) (protocol.ConnectivityFrame, error) {
	return r.requestRelayTunnel(owner, nil, daemonID, attemptID, requestID, "", 0, 0, ttl)
}

func (r *Registry) RequestRelayTunnelFromApp(owner AppOwner, peer AppPeer, daemonID, attemptID, requestID string, ttl time.Duration) (protocol.ConnectivityFrame, error) {
	return r.RequestRelayTunnelFromAppWithDiagnostics(owner, peer, daemonID, attemptID, requestID, "", 0, 0, ttl)
}

func (r *Registry) RequestRelayTunnelFromAppWithDiagnostics(owner AppOwner, peer AppPeer, daemonID, attemptID, requestID, fallbackReason string, directLatencyMS, relayLatencyMS int, ttl time.Duration) (protocol.ConnectivityFrame, error) {
	return r.requestRelayTunnel(owner, peer, daemonID, attemptID, requestID, fallbackReason, directLatencyMS, relayLatencyMS, ttl)
}

func (r *Registry) requestRelayTunnel(owner AppOwner, appPeer AppPeer, daemonID, attemptID, requestID, fallbackReason string, directLatencyMS, relayLatencyMS int, ttl time.Duration) (protocol.ConnectivityFrame, error) {
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
	if (appPeer != nil && !r.appPeerActiveLocked(owner, appPeer)) || (appPeer == nil && !r.appOwnerActiveLocked(owner)) {
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
	if r.activeDirect[tunnelKey] != nil {
		r.mu.Unlock()
		closeTunnelClosers(sweepClosers)
		return protocol.ConnectivityFrame{}, ErrRelayTunnelUnavailable
	}
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
	rendezvousNotifications := r.removeRendezvousLocked(func(attempt *rendezvousAttempt) bool {
		return attempt.appOwner.UserID == owner.UserID &&
			attempt.appOwner.AppSessionID == owner.AppSessionID &&
			attempt.appOwner.DeviceFingerprint == owner.DeviceFingerprint &&
			attempt.daemonDeviceID == daemonID &&
			attempt.id == attemptID
	})
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
		fallbackReason:    strings.TrimSpace(fallbackReason),
		directLatencyMS:   nonNegativeInt(directLatencyMS),
		relayLatencyMS:    nonNegativeInt(relayLatencyMS),
		expiresAt:         r.now().Add(ttl),
		closers:           make(map[int64]func()),
	}
	r.attempts[tunnelKey] = attempt
	r.tokens[appToken] = tunnelToken{attemptID: tunnelKey, actor: TunnelActorClient}
	r.tokens[daemonToken] = tunnelToken{attemptID: tunnelKey, actor: TunnelActorDaemon}
	daemonPeer := live.peer
	daemonFrame := protocol.ConnectivityRelayTunnelReadyFrameWithDiagnostics(requestID, attemptID, TunnelActorDaemon, live.info.DeviceID, owner.DeviceFingerprint, daemonToken, attempt.fallbackReason, attempt.directLatencyMS, attempt.relayLatencyMS)
	appFrame := protocol.ConnectivityRelayTunnelReadyFrameWithDiagnostics(requestID, attemptID, TunnelActorClient, live.info.DeviceID, owner.DeviceFingerprint, appToken, attempt.fallbackReason, attempt.directLatencyMS, attempt.relayLatencyMS)
	r.mu.Unlock()

	sendRendezvousCloseNotifications(rendezvousNotifications)
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
	case TunnelActorClient:
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

func (r *Registry) removeRendezvousLocked(match func(*rendezvousAttempt) bool) []rendezvousCloseNotification {
	var notifications []rendezvousCloseNotification
	for key, attempt := range r.rendezvous {
		if match(attempt) {
			delete(r.rendezvous, key)
			if attempt.daemonPeer != nil {
				notifications = append(notifications, rendezvousCloseNotification{
					peer: attempt.daemonPeer,
					frame: protocol.ConnectivityRendezvousCloseFrame(
						"",
						attempt.id,
						TunnelActorClient,
						attempt.daemonDeviceID,
						attempt.appOwner.DeviceFingerprint,
					),
				})
			}
		}
	}
	return notifications
}

func (r *Registry) removeActiveDirectLocked(match func(*directSession) bool) []rendezvousCloseNotification {
	var notifications []rendezvousCloseNotification
	for key, session := range r.activeDirect {
		if match(session) {
			delete(r.activeDirect, key)
			if session.daemonPeer != nil {
				notifications = append(notifications, rendezvousCloseNotification{
					peer:           session.daemonPeer,
					closeOnFailure: true,
					frame: protocol.ConnectivityDirectSessionCloseFrame(
						"",
						session.id,
						session.daemonDeviceID,
						session.androidFingerprint,
					),
				})
			}
		}
	}
	return notifications
}

func (r *Registry) reconcileDirectSessionsLocked(owner DaemonOwner, info protocol.ConnectivityDaemonInfo, trusted map[string]protocol.ConnectivityTrustedAndroid, reported []protocol.ConnectivityDirectSession, peer DaemonPeer) []rendezvousCloseNotification {
	reportedKeys := make(map[string]struct{}, len(reported))
	var notifications []rendezvousCloseNotification
	for _, session := range reported {
		attemptID := strings.TrimSpace(session.AttemptID)
		androidFingerprint := normalizeFingerprint(session.AndroidFingerprint)
		if attemptID == "" || androidFingerprint == "" {
			continue
		}
		reportedKeys[directSessionReportKey(attemptID, androidFingerprint)] = struct{}{}
		var active *directSession
		for _, candidate := range r.activeDirect {
			if candidate.id == attemptID &&
				candidate.daemonOwner == owner &&
				candidate.daemonDeviceID == info.DeviceID &&
				candidate.androidFingerprint == androidFingerprint {
				active = candidate
				break
			}
		}
		if active == nil || r.appOwnerRevokedLocked(active.appOwner) || trusted[androidFingerprint].Fingerprint == "" {
			notifications = append(notifications, rendezvousCloseNotification{
				peer:  peer,
				frame: protocol.ConnectivityDirectSessionCloseFrame("", attemptID, info.DeviceID, androidFingerprint),
			})
			continue
		}
		active.daemonPeer = peer
		active.daemonFingerprint = info.DaemonFingerprint
	}
	for key, session := range r.activeDirect {
		if session.daemonOwner != owner || session.daemonDeviceID != info.DeviceID {
			continue
		}
		if _, ok := reportedKeys[directSessionReportKey(session.id, session.androidFingerprint)]; ok {
			continue
		}
		delete(r.activeDirect, key)
	}
	return notifications
}

func directSessionReportKey(attemptID, androidFingerprint string) string {
	return strings.TrimSpace(attemptID) + "\x00" + normalizeFingerprint(androidFingerprint)
}

func sendRendezvousCloseNotifications(notifications []rendezvousCloseNotification) {
	for _, notification := range notifications {
		if notification.peer != nil {
			if err := notification.peer.SendJSON(notification.frame); err != nil && notification.closeOnFailure {
				_ = notification.peer.Close()
			}
		}
	}
}

func (r *Registry) sweepExpiredRendezvousLocked() int {
	removed := 0
	now := r.now()
	for key, attempt := range r.rendezvous {
		if attempt.expiresAt.After(now) {
			continue
		}
		delete(r.rendezvous, key)
		removed++
	}
	return removed
}

func (r *Registry) sweepExpiredPairCorrelationsLocked() int {
	removed := 0
	now := r.now()
	for key, correlation := range r.correlations {
		if correlation.expiresAt.After(now) {
			continue
		}
		delete(r.correlations, key)
		removed++
	}
	return removed
}

func (r *Registry) expireRendezvousLater(key string, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	time.AfterFunc(ttl, func() {
		r.mu.Lock()
		attempt := r.rendezvous[key]
		if attempt != nil && !attempt.expiresAt.After(r.now()) {
			delete(r.rendezvous, key)
		}
		r.mu.Unlock()
	})
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

func (r *Registry) appOwnerActiveLocked(owner AppOwner) bool {
	if r.appOwnerRevokedLocked(owner) {
		return false
	}
	owner.DeviceFingerprint = normalizeFingerprint(owner.DeviceFingerprint)
	for _, app := range r.apps {
		if app.owner.UserID == owner.UserID &&
			app.owner.AppSessionID == owner.AppSessionID &&
			app.owner.DeviceFingerprint == owner.DeviceFingerprint {
			return true
		}
	}
	return false
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

func (r *Registry) allowRendezvousRequestLocked(userID int64, now time.Time) bool {
	return len(r.recentRendezvousRequestsLocked(userID, now)) < RelayTunnelRequestLimit
}

func (r *Registry) recordRendezvousRequestLocked(userID int64, now time.Time) {
	requests := r.recentRendezvousRequestsLocked(userID, now)
	requests = append(requests, now)
	r.rendezvousReqs[userID] = requests
}

func (r *Registry) recentRendezvousRequestsLocked(userID int64, now time.Time) []time.Time {
	requests := r.rendezvousReqs[userID]
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
		delete(r.rendezvousReqs, userID)
		return nil
	}
	r.rendezvousReqs[userID] = kept
	return kept
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

func normalizeCandidateList(addrs []string) []string {
	seen := make(map[string]struct{})
	for _, raw := range addrs {
		addr := strings.TrimSpace(raw)
		if addr == "" {
			continue
		}
		seen[addr] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for addr := range seen {
		out = append(out, addr)
	}
	sort.Strings(out)
	if len(out) > RendezvousCandidateLimit {
		out = out[:RendezvousCandidateLimit]
	}
	return out
}

func validCandidatePayload(publicUDPAddr string, privateUDPAddrs []string) bool {
	if !validUDPAddr(publicUDPAddr) {
		return false
	}
	for _, addr := range privateUDPAddrs {
		if !validUDPAddr(addr) {
			return false
		}
	}
	return true
}

func validUDPAddr(raw string) bool {
	host, port, err := net.SplitHostPort(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	portNum, err := strconv.Atoi(port)
	return err == nil && portNum > 0 && portNum <= 65535
}

func nonNegativeInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
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
