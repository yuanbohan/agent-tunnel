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
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	SessionID string `json:"session_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

const (
	LaunchStatusAccepted     = "accepted"
	LaunchStatusFailed       = "failed"
	LaunchStatusSessionReady = "session_ready"
)

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
	id        string
	deviceID  string
	owner     DeviceOwner
	peer      DevicePeer
	mu        sync.Mutex
	accepted  bool
	completed bool
	result    LaunchResult
	done      chan struct{}
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
		r.completeDeviceRequestsLocked(info.DeviceID, LaunchResult{Status: LaunchStatusFailed, Reason: "device_offline"})
	}
	r.devices[info.DeviceID] = &liveDevice{
		info:        info,
		owner:       owner,
		peer:        peer,
		connectedAt: r.now(),
		inFlight:    r.hasPendingRequestLocked(info.DeviceID),
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
	if old != nil && old.owner.UserID != owner.UserID {
		r.mu.Unlock()
		return false
	}
	if old != nil && old.peer != peer {
		deactivateDevicePeer(old.peer)
		r.completeDeviceRequestsLocked(info.DeviceID, LaunchResult{Status: LaunchStatusFailed, Reason: "device_offline"})
	}
	r.devices[info.DeviceID] = &liveDevice{
		info:        info,
		owner:       owner,
		peer:        peer,
		connectedAt: r.now(),
		inFlight:    r.hasPendingRequestLocked(info.DeviceID),
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
	r.completeDeviceRequestsLocked(deviceID, LaunchResult{Status: LaunchStatusFailed, Reason: "device_offline"})
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
		r.completeDeviceRequestsLocked(deviceID, LaunchResult{Status: LaunchStatusFailed, Reason: "device_offline"})
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

func (r *Registry) Launch(ctx context.Context, deviceID string, userID int64, command, cwd, label string) LaunchResult {
	requestID := deviceID + "-" + r.now().Format("150405.000000000")

	r.mu.Lock()
	live, ok := r.devices[deviceID]
	if !ok || live.owner.UserID != userID || live.peer == nil {
		r.mu.Unlock()
		return LaunchResult{RequestID: requestID, Status: LaunchStatusFailed, Reason: "device_offline"}
	}
	if live.inFlight {
		r.mu.Unlock()
		return LaunchResult{RequestID: requestID, Status: LaunchStatusFailed, Reason: "busy"}
	}

	request := &launchRequest{
		id:       requestID,
		deviceID: deviceID,
		owner:    live.owner,
		peer:     live.peer,
		done:     make(chan struct{}),
	}
	live.inFlight = true
	r.requests[requestID] = request
	peer := live.peer
	r.mu.Unlock()

	if err := ctx.Err(); err != nil {
		r.clearRequest(requestID)
		return LaunchResult{RequestID: requestID, Status: LaunchStatusFailed, Reason: "launch_timeout"}
	}

	if err := peer.SendJSON(protocol.DeviceLaunchRequestFrame(requestID, command, cwd, label)); err != nil {
		r.clearRequest(requestID)
		return LaunchResult{RequestID: requestID, Status: LaunchStatusFailed, Reason: "device_offline"}
	}

	select {
	case <-request.done:
		return request.snapshot()
	case <-ctx.Done():
		request.complete(LaunchResult{RequestID: requestID, Status: LaunchStatusFailed, Reason: "launch_timeout"})
		r.clearRequest(requestID)
		return request.snapshot()
	}
}

func (r *Registry) ResolveLaunchIfOwner(deviceID string, owner DevicePeer, requestID, status, reason string) bool {
	r.mu.Lock()
	request, ok := r.requests[requestID]
	if !ok || request.deviceID != deviceID || request.peer != owner {
		r.mu.Unlock()
		return false
	}
	switch status {
	case LaunchStatusAccepted:
		request.markAccepted()
		r.mu.Unlock()
		return true
	case LaunchStatusFailed:
		r.completeRequestLocked(requestID, LaunchResult{
			RequestID: requestID,
			Status:    LaunchStatusFailed,
			Reason:    reason,
		})
		r.mu.Unlock()
		return true
	default:
		r.mu.Unlock()
		return false
	}
}

func (r *Registry) CompleteLaunchIfOwner(requestID string, owner DeviceOwner, sessionID string) bool {
	r.mu.Lock()
	request, ok := r.requests[requestID]
	if !ok || request.owner != owner {
		r.mu.Unlock()
		return false
	}
	r.completeRequestLocked(requestID, LaunchResult{
		RequestID: requestID,
		Status:    LaunchStatusSessionReady,
		SessionID: sessionID,
	})
	r.mu.Unlock()
	return true
}

func (r *Registry) clearRequest(requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearRequestLocked(requestID)
}

func (r *Registry) clearRequestLocked(requestID string) *launchRequest {
	request, ok := r.requests[requestID]
	if ok {
		if live := r.devices[request.deviceID]; live != nil {
			live.inFlight = r.hasOtherPendingRequestLocked(request.deviceID, requestID)
		}
	}
	delete(r.requests, requestID)
	return request
}

func (r *Registry) completeRequestLocked(requestID string, result LaunchResult) {
	request := r.clearRequestLocked(requestID)
	completeLaunchRequest(request, result)
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
	request.complete(result)
}

func (r *Registry) completeDeviceRequestsLocked(deviceID string, result LaunchResult) {
	for requestID, request := range r.requests {
		if request.deviceID != deviceID {
			continue
		}
		if request.isAccepted() {
			if live := r.devices[deviceID]; live != nil {
				live.inFlight = true
			}
			continue
		}
		result.RequestID = request.id
		r.clearRequestLocked(requestID)
		completeLaunchRequest(request, result)
	}
}

func (r *Registry) hasPendingRequestLocked(deviceID string) bool {
	for _, request := range r.requests {
		if request.deviceID == deviceID {
			return true
		}
	}
	return false
}

func (r *Registry) hasOtherPendingRequestLocked(deviceID, skipRequestID string) bool {
	for requestID, request := range r.requests {
		if requestID == skipRequestID {
			continue
		}
		if request.deviceID == deviceID {
			return true
		}
	}
	return false
}

func (r *launchRequest) markAccepted() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.completed {
		return
	}
	r.accepted = true
}

func (r *launchRequest) isAccepted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.accepted && !r.completed
}

func (r *launchRequest) complete(result LaunchResult) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.completed {
		return false
	}
	r.completed = true
	r.result = result
	close(r.done)
	return true
}

func (r *launchRequest) snapshot() LaunchResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.result
}
