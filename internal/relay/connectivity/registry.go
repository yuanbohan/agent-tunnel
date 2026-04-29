package connectivity

import (
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
}

type DaemonOwner struct {
	UserID       int64
	AgentTokenID string
}

type Registry struct {
	mu           sync.RWMutex
	apps         map[AppPeer]liveApp
	daemons      map[string]*liveDaemon
	correlations map[string]pairCorrelation
	now          func() time.Time
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

func NewRegistry() *Registry {
	return &Registry{
		apps:         make(map[AppPeer]liveApp),
		daemons:      make(map[string]*liveDaemon),
		correlations: make(map[string]pairCorrelation),
		now:          func() time.Time { return time.Now().UTC() },
	}
}

func (r *Registry) RegisterApp(owner AppOwner, peer AppPeer) []protocol.ConnectivityDaemonInfo {
	owner.DeviceFingerprint = normalizeFingerprint(owner.DeviceFingerprint)
	r.mu.Lock()
	r.apps[peer] = liveApp{owner: owner, peer: peer, connectedAt: r.now()}
	snapshot := r.visibleDaemonsLocked(owner)
	r.mu.Unlock()
	return snapshot
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
	info.DeviceID = strings.TrimSpace(info.DeviceID)
	info.DaemonFingerprint = normalizeFingerprint(info.DaemonFingerprint)
	trustedMap := trustedMapFromSlice(trusted)
	key := daemonMapKey(owner.UserID, info.DeviceID)

	r.mu.Lock()
	old := r.daemons[key]
	var removed []AppPeer
	var oldInfo protocol.ConnectivityDaemonInfo
	if old != nil && old.peer != peer {
		delete(r.daemons, key)
		r.removeCorrelationsForDaemonLocked(key, old.peer)
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

	if old != nil && old.peer != peer {
		_ = old.peer.Close()
	}
	for _, appPeer := range removed {
		_ = appPeer.SendJSON(protocol.ConnectivityDaemonRemovedFrame(oldInfo.DeviceID, oldInfo.DaemonFingerprint))
	}
	for _, appPeer := range appPeers {
		_ = appPeer.SendJSON(protocol.ConnectivityDaemonUpsertFrame(info))
	}
	return appPeers
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
	appPeers := r.matchingAppPeersLocked(live.owner.UserID, live.trusted)
	info := live.info
	r.mu.Unlock()

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
	appPeers := r.matchingSingleAppPeerLocked(live.owner.UserID, fingerprint)
	info := live.info
	r.mu.Unlock()

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
	r.correlations[correlationID] = pairCorrelation{
		id:        correlationID,
		owner:     owner,
		deviceID:  key,
		peer:      peer,
		expiresAt: r.now().Add(ttl),
	}
	return true
}

func (r *Registry) ForwardPairingResponse(userID int64, correlationID string, response protocol.ConnectivityPairingResponse) error {
	correlationID = strings.TrimSpace(correlationID)
	r.mu.Lock()
	correlation, ok := r.correlations[correlationID]
	if !ok || correlation.owner.UserID != userID || !correlation.expiresAt.After(r.now()) {
		if ok {
			delete(r.correlations, correlationID)
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
	if current, ok := r.correlations[correlationID]; ok && current.peer == peer && current.owner.UserID == userID {
		delete(r.correlations, correlationID)
	}
	r.mu.Unlock()
	return nil
}

func (r *Registry) DisconnectAppSession(appSessionID string, reason string) int {
	r.mu.Lock()
	var peers []AppPeer
	for peer, app := range r.apps {
		if app.owner.AppSessionID != appSessionID {
			continue
		}
		delete(r.apps, peer)
		peers = append(peers, peer)
	}
	r.mu.Unlock()
	for _, peer := range peers {
		_ = peer.Close()
	}
	return len(peers)
}

func (r *Registry) DisconnectUser(userID int64, reason string) int {
	r.mu.Lock()
	var appPeers []AppPeer
	var daemonPeers []DaemonPeer
	for peer, app := range r.apps {
		if app.owner.UserID == userID {
			delete(r.apps, peer)
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
	r.mu.Unlock()
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
	var peers []DaemonPeer
	for key, live := range r.daemons {
		if live.owner.AgentTokenID == agentTokenID {
			delete(r.daemons, key)
			r.removeCorrelationsForDaemonLocked(key, live.peer)
			peers = append(peers, live.peer)
		}
	}
	r.mu.Unlock()
	for _, peer := range peers {
		_ = peer.Close()
	}
	return len(peers)
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

func (r *Registry) daemonByDevicePeerLocked(deviceID string, peer DaemonPeer) (string, *liveDaemon, bool) {
	for key, live := range r.daemons {
		if live.info.DeviceID == deviceID && live.peer == peer {
			return key, live, true
		}
	}
	return "", nil, false
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
