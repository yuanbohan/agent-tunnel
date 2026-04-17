package device

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"yuanbohan/tunnel/internal/protocol"
)

var ErrDevicePeerInactive = errors.New("device peer inactive")

type DevicePeer interface {
	SendJSON(any) error
	Close() error
}

type deactivatableDevicePeer interface {
	Deactivate()
}

type DeviceOwner struct {
	UserID       int64
	AgentTokenID string
}

type LaunchResult struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"`
}

type Registry struct {
	mu       sync.RWMutex
	devices  map[string]*liveDevice
	requests map[string]*launchRequest
	pending  map[DevicePeer]DeviceOwner
	now      func() time.Time
}

type liveDevice struct {
	info        protocol.DeviceInfo
	owner       DeviceOwner
	peer        DevicePeer
	connectedAt time.Time
	inFlight    bool
}

type launchRequest struct {
	id       string
	deviceID string
	peer     DevicePeer
	resultCh chan LaunchResult
}

func NewRegistry() *Registry {
	return &Registry{
		devices:  make(map[string]*liveDevice),
		requests: make(map[string]*launchRequest),
		pending:  make(map[DevicePeer]DeviceOwner),
		now:      func() time.Time { return time.Now().UTC() },
	}
}

func (r *Registry) RegisterOwned(info protocol.DeviceInfo, owner DeviceOwner, peer DevicePeer) {
	r.mu.Lock()
	old := r.devices[info.DeviceID]
	if old != nil && old.peer != peer {
		deactivateDevicePeer(old.peer)
		r.completeDeviceRequestsLocked(info.DeviceID, LaunchResult{Accepted: false, Reason: "device_offline"})
	}
	r.devices[info.DeviceID] = &liveDevice{
		info:        info,
		owner:       owner,
		peer:        peer,
		connectedAt: r.now(),
	}
	r.mu.Unlock()

	if old != nil && old.peer != peer {
		_ = old.peer.Close()
	}
}

func (r *Registry) RegisterPending(owner DeviceOwner, peer DevicePeer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending[peer] = owner
}

func (r *Registry) DisconnectPending(peer DevicePeer) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pending[peer]; !ok {
		return false
	}
	delete(r.pending, peer)
	deactivateDevicePeer(peer)
	return true
}

func (r *Registry) ActivatePending(info protocol.DeviceInfo, owner DeviceOwner, peer DevicePeer) bool {
	r.mu.Lock()
	pendingOwner, ok := r.pending[peer]
	if !ok || pendingOwner != owner {
		r.mu.Unlock()
		return false
	}
	delete(r.pending, peer)
	old := r.devices[info.DeviceID]
	if old != nil && old.peer != peer {
		deactivateDevicePeer(old.peer)
		r.completeDeviceRequestsLocked(info.DeviceID, LaunchResult{Accepted: false, Reason: "device_offline"})
	}
	r.devices[info.DeviceID] = &liveDevice{
		info:        info,
		owner:       owner,
		peer:        peer,
		connectedAt: r.now(),
	}
	r.mu.Unlock()

	if old != nil && old.peer != peer {
		_ = old.peer.Close()
	}
	return true
}

func (r *Registry) DisconnectIfOwner(deviceID string, owner DevicePeer) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	live, ok := r.devices[deviceID]
	if !ok || owner == nil || live.peer != owner {
		return false
	}
	delete(r.devices, deviceID)
	deactivateDevicePeer(owner)
	r.completeDeviceRequestsLocked(deviceID, LaunchResult{Accepted: false, Reason: "device_offline"})
	_ = owner.Close()
	return true
}

func (r *Registry) DisconnectAgentTokenDevices(agentTokenID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	disconnected := 0
	for deviceID, live := range r.devices {
		if live.owner.AgentTokenID != agentTokenID {
			continue
		}
		delete(r.devices, deviceID)
		deactivateDevicePeer(live.peer)
		r.completeDeviceRequestsLocked(deviceID, LaunchResult{Accepted: false, Reason: "device_offline"})
		_ = live.peer.Close()
		disconnected++
	}
	for peer, owner := range r.pending {
		if owner.AgentTokenID != agentTokenID {
			continue
		}
		delete(r.pending, peer)
		deactivateDevicePeer(peer)
		_ = peer.Close()
		disconnected++
	}
	return disconnected
}

func (r *Registry) ListForUser(userID int64) []protocol.DeviceInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	liveDevices := make([]*liveDevice, 0, len(r.devices))
	for _, live := range r.devices {
		if live.owner.UserID != userID {
			continue
		}
		liveDevices = append(liveDevices, live)
	}
	sort.Slice(liveDevices, func(i, j int) bool {
		if liveDevices[i].connectedAt.Equal(liveDevices[j].connectedAt) {
			return liveDevices[i].info.DeviceID < liveDevices[j].info.DeviceID
		}
		return liveDevices[i].connectedAt.After(liveDevices[j].connectedAt)
	})
	out := make([]protocol.DeviceInfo, 0, len(liveDevices))
	for _, live := range liveDevices {
		out = append(out, live.info)
	}
	return out
}

func (r *Registry) Launch(ctx context.Context, deviceID string, userID int64, command string) LaunchResult {
	r.mu.Lock()
	live, ok := r.devices[deviceID]
	if !ok || live.owner.UserID != userID || live.peer == nil {
		r.mu.Unlock()
		return LaunchResult{Accepted: false, Reason: "device_offline"}
	}
	if live.inFlight {
		r.mu.Unlock()
		return LaunchResult{Accepted: false, Reason: "busy"}
	}

	requestID := deviceID + "-" + r.now().Format("150405.000000000")
	request := &launchRequest{
		id:       requestID,
		deviceID: deviceID,
		peer:     live.peer,
		resultCh: make(chan LaunchResult, 1),
	}
	live.inFlight = true
	r.requests[requestID] = request
	peer := live.peer
	r.mu.Unlock()

	if err := ctx.Err(); err != nil {
		r.clearRequest(requestID)
		return LaunchResult{Accepted: false, Reason: "device_offline"}
	}

	if err := peer.SendJSON(protocol.DeviceLaunchRequestFrame(requestID, command)); err != nil {
		r.clearRequest(requestID)
		return LaunchResult{Accepted: false, Reason: "device_offline"}
	}

	select {
	case result := <-request.resultCh:
		r.clearRequest(requestID)
		return result
	case <-ctx.Done():
		r.clearRequest(requestID)
		return LaunchResult{Accepted: false, Reason: "device_offline"}
	}
}

func (r *Registry) ResolveLaunchIfOwner(deviceID string, owner DevicePeer, requestID string, accepted bool, reason string) bool {
	r.mu.RLock()
	request, ok := r.requests[requestID]
	if !ok || request.deviceID != deviceID || request.peer != owner {
		r.mu.RUnlock()
		return false
	}
	resultCh := request.resultCh
	r.mu.RUnlock()

	select {
	case resultCh <- LaunchResult{Accepted: accepted, Reason: reason}:
	default:
	}
	return true
}

func (r *Registry) clearRequest(requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	request, ok := r.requests[requestID]
	if ok {
		if live := r.devices[request.deviceID]; live != nil {
			live.inFlight = false
		}
	}
	delete(r.requests, requestID)
}

func deactivateDevicePeer(peer DevicePeer) {
	if deactivatable, ok := peer.(deactivatableDevicePeer); ok {
		deactivatable.Deactivate()
	}
}

func completeLaunchRequest(request *launchRequest, result LaunchResult) {
	if request == nil {
		return
	}
	select {
	case request.resultCh <- result:
	default:
	}
}

func (r *Registry) completeDeviceRequestsLocked(deviceID string, result LaunchResult) {
	for requestID, request := range r.requests {
		if request.deviceID != deviceID {
			continue
		}
		if live := r.devices[deviceID]; live != nil {
			live.inFlight = false
		}
		completeLaunchRequest(request, result)
		delete(r.requests, requestID)
	}
}
