package device

import (
	"context"
	"errors"
	"sort"
	"strings"
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

type TerminateResult struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
}

type TerminateTarget struct {
	DeviceID         string
	WorkspaceSession string
}

type LaunchCompletion struct {
	SessionID string
	Target    TerminateTarget
}

const (
	LaunchStatusAccepted     = "accepted"
	LaunchStatusFailed       = "failed"
	LaunchStatusSessionReady = "session_ready"

	TerminateStatusTerminated = "terminated"
	TerminateStatusFailed     = "failed"
)

type Registry struct {
	mu                sync.RWMutex
	devices           map[string]*liveDevice
	requests          map[string]*launchRequest
	completedLaunches map[string]completedLaunch
	terminateRequests map[string]*terminateRequest
	pending           map[DevicePeer]DeviceOwner
	now               func() time.Time
}

type liveDevice struct {
	info        protocol.DeviceInfo
	owner       DeviceOwner
	peer        DevicePeer
	connectedAt time.Time
	inFlight    bool
}

type launchRequest struct {
	id               string
	deviceID         string
	owner            DeviceOwner
	peer             DevicePeer
	mu               sync.Mutex
	accepted         bool
	completed        bool
	sessionID        string
	workspaceSession string
	result           LaunchResult
	done             chan struct{}
}

type completedLaunch struct {
	owner  DeviceOwner
	target TerminateTarget
}

type terminateRequest struct {
	id        string
	deviceID  string
	owner     DeviceOwner
	peer      DevicePeer
	target    TerminateTarget
	mu        sync.Mutex
	completed bool
	result    TerminateResult
	done      chan struct{}
}

func NewRegistry() *Registry {
	return &Registry{
		devices:           make(map[string]*liveDevice),
		requests:          make(map[string]*launchRequest),
		completedLaunches: make(map[string]completedLaunch),
		terminateRequests: make(map[string]*terminateRequest),
		pending:           make(map[DevicePeer]DeviceOwner),
		now:               func() time.Time { return time.Now().UTC() },
	}
}

func (r *Registry) RegisterOwned(info protocol.DeviceInfo, owner DeviceOwner, peer DevicePeer) {
	r.mu.Lock()
	old := r.devices[info.DeviceID]
	if old != nil && old.peer != peer {
		deactivateDevicePeer(old.peer)
		r.completeDeviceRequestsLocked(info.DeviceID, LaunchResult{Status: LaunchStatusFailed, Reason: "device_offline"})
		r.completeDeviceTerminateRequestsLocked(info.DeviceID, TerminateResult{Status: TerminateStatusFailed, Reason: "device_offline"})
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
		r.completeDeviceTerminateRequestsLocked(info.DeviceID, TerminateResult{Status: TerminateStatusFailed, Reason: "device_offline"})
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

func (r *Registry) UpdateIfOwner(deviceID string, owner DevicePeer, info protocol.DeviceInfo) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	live, ok := r.devices[deviceID]
	if !ok || live.peer != owner {
		return false
	}
	live.info = info
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
	r.completeDeviceTerminateRequestsLocked(deviceID, TerminateResult{Status: TerminateStatusFailed, Reason: "device_offline"})
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
		r.completeDeviceTerminateRequestsLocked(deviceID, TerminateResult{Status: TerminateStatusFailed, Reason: "device_offline"})
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

func (r *Registry) ResolveLaunchIfOwner(deviceID string, owner DevicePeer, requestID, status, reason, workspaceSession string) (LaunchCompletion, bool) {
	r.mu.Lock()
	request, ok := r.requests[requestID]
	if !ok || request.deviceID != deviceID || request.peer != owner {
		r.mu.Unlock()
		return LaunchCompletion{}, false
	}
	switch status {
	case LaunchStatusAccepted:
		request.markAccepted(workspaceSession)
		sessionID := request.registeredSessionID()
		if sessionID == "" {
			r.mu.Unlock()
			return LaunchCompletion{}, true
		}
		completion, _ := r.completeReadyLaunchLocked(requestID, request, sessionID)
		r.mu.Unlock()
		return completion, true
	case LaunchStatusFailed:
		failureReason := strings.TrimSpace(reason)
		if failureReason == "" {
			failureReason = "unknown_reason"
		}
		r.completeRequestLocked(requestID, LaunchResult{
			RequestID: requestID,
			Status:    LaunchStatusFailed,
			Reason:    failureReason,
		})
		r.mu.Unlock()
		return LaunchCompletion{}, true
	default:
		r.mu.Unlock()
		return LaunchCompletion{}, false
	}
}

func (r *Registry) CompleteLaunchIfOwner(requestID string, owner DeviceOwner, sessionID string) (TerminateTarget, bool) {
	r.mu.Lock()
	sessionID = strings.TrimSpace(sessionID)
	request, ok := r.requests[requestID]
	if !ok || request.owner != owner || sessionID == "" {
		completed, completedOK := r.completedLaunches[requestID]
		if !completedOK || completed.owner != owner {
			r.mu.Unlock()
			return TerminateTarget{}, false
		}
		r.mu.Unlock()
		return completed.target, true
	}
	request.markSessionReady(sessionID)
	if !request.isAccepted() {
		r.mu.Unlock()
		return TerminateTarget{}, false
	}
	completion, _ := r.completeReadyLaunchLocked(requestID, request, sessionID)
	r.mu.Unlock()
	return completion.Target, true
}

func (r *Registry) Terminate(ctx context.Context, userID int64, sessionID string, target TerminateTarget) TerminateResult {
	requestID := target.DeviceID + "-terminate-" + r.now().Format("150405.000000000")
	workspaceSession := strings.TrimSpace(target.WorkspaceSession)
	if workspaceSession == "" {
		return TerminateResult{RequestID: requestID, Status: TerminateStatusFailed, Reason: "session_not_found"}
	}

	r.mu.Lock()
	live, ok := r.devices[target.DeviceID]
	if !ok || live.owner.UserID != userID || live.peer == nil {
		r.mu.Unlock()
		return TerminateResult{RequestID: requestID, Status: TerminateStatusFailed, Reason: "device_offline"}
	}
	request := &terminateRequest{
		id:       requestID,
		deviceID: target.DeviceID,
		owner:    live.owner,
		peer:     live.peer,
		target:   target,
		done:     make(chan struct{}),
	}
	r.terminateRequests[requestID] = request
	peer := live.peer
	r.mu.Unlock()

	if err := ctx.Err(); err != nil {
		r.clearTerminateRequest(requestID)
		return TerminateResult{RequestID: requestID, Status: TerminateStatusFailed, Reason: "terminate_timeout"}
	}

	if err := peer.SendJSON(protocol.DeviceTerminateRequestFrame(requestID, sessionID, workspaceSession)); err != nil {
		r.clearTerminateRequest(requestID)
		return TerminateResult{RequestID: requestID, Status: TerminateStatusFailed, Reason: "device_offline"}
	}

	select {
	case <-request.done:
		return request.snapshot()
	case <-ctx.Done():
		request.complete(TerminateResult{RequestID: requestID, Status: TerminateStatusFailed, Reason: "terminate_timeout"})
		r.clearTerminateRequest(requestID)
		return request.snapshot()
	}
}

func (r *Registry) ResolveTerminateIfOwner(deviceID string, owner DevicePeer, requestID, status, reason string) bool {
	r.mu.Lock()
	request, ok := r.terminateRequests[requestID]
	if !ok || request.deviceID != deviceID || request.peer != owner {
		r.mu.Unlock()
		return false
	}
	switch status {
	case TerminateStatusTerminated:
		r.deleteCompletedLaunchTargetLocked(request.target)
		r.completeTerminateRequestLocked(requestID, TerminateResult{
			RequestID: requestID,
			Status:    TerminateStatusTerminated,
		})
		r.mu.Unlock()
		return true
	case TerminateStatusFailed:
		failureReason := strings.TrimSpace(reason)
		if failureReason == "" {
			failureReason = "unknown_reason"
		}
		r.completeTerminateRequestLocked(requestID, TerminateResult{
			RequestID: requestID,
			Status:    TerminateStatusFailed,
			Reason:    failureReason,
		})
		r.mu.Unlock()
		return true
	default:
		r.mu.Unlock()
		return false
	}
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

func (r *Registry) completeReadyLaunchLocked(requestID string, request *launchRequest, sessionID string) (LaunchCompletion, bool) {
	target := TerminateTarget{
		DeviceID:         request.deviceID,
		WorkspaceSession: request.acceptedWorkspaceSession(),
	}
	r.completeRequestLocked(requestID, LaunchResult{
		RequestID: requestID,
		Status:    LaunchStatusSessionReady,
		SessionID: sessionID,
	})
	completion := LaunchCompletion{
		SessionID: sessionID,
		Target:    target,
	}
	if hasTerminateTarget(target) {
		r.completedLaunches[requestID] = completedLaunch{
			owner:  request.owner,
			target: target,
		}
		return completion, true
	}
	return completion, false
}

func (r *Registry) clearTerminateRequest(requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearTerminateRequestLocked(requestID)
}

func (r *Registry) clearTerminateRequestLocked(requestID string) *terminateRequest {
	request, ok := r.terminateRequests[requestID]
	if !ok {
		return nil
	}
	delete(r.terminateRequests, requestID)
	return request
}

func (r *Registry) completeTerminateRequestLocked(requestID string, result TerminateResult) {
	request := r.clearTerminateRequestLocked(requestID)
	completeTerminateRequest(request, result)
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

func completeTerminateRequest(request *terminateRequest, result TerminateResult) {
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

func (r *Registry) deleteCompletedLaunchTargetLocked(target TerminateTarget) {
	target = normalizedTerminateTarget(target)
	if !hasTerminateTarget(target) {
		return
	}
	for requestID, completed := range r.completedLaunches {
		if completed.target == target {
			delete(r.completedLaunches, requestID)
		}
	}
}

func (r *Registry) completeDeviceTerminateRequestsLocked(deviceID string, result TerminateResult) {
	for requestID, request := range r.terminateRequests {
		if request.deviceID != deviceID {
			continue
		}
		result.RequestID = request.id
		r.clearTerminateRequestLocked(requestID)
		completeTerminateRequest(request, result)
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

func (r *launchRequest) markAccepted(workspaceSession string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.completed {
		return
	}
	r.accepted = true
	r.workspaceSession = strings.TrimSpace(workspaceSession)
}

func (r *launchRequest) markSessionReady(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.completed {
		return
	}
	r.sessionID = strings.TrimSpace(sessionID)
}

func (r *launchRequest) isAccepted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.accepted && !r.completed
}

func (r *launchRequest) registeredSessionID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.completed {
		return ""
	}
	return strings.TrimSpace(r.sessionID)
}

func (r *launchRequest) acceptedWorkspaceSession() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.completed {
		return ""
	}
	return strings.TrimSpace(r.workspaceSession)
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

func (r *terminateRequest) complete(result TerminateResult) bool {
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

func (r *terminateRequest) snapshot() TerminateResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.result
}

func normalizedTerminateTarget(target TerminateTarget) TerminateTarget {
	return TerminateTarget{
		DeviceID:         strings.TrimSpace(target.DeviceID),
		WorkspaceSession: strings.TrimSpace(target.WorkspaceSession),
	}
}

func hasTerminateTarget(target TerminateTarget) bool {
	target = normalizedTerminateTarget(target)
	return target.DeviceID != "" && target.WorkspaceSession != ""
}
