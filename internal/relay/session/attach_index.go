package session

import "sync"

type AttachSessionRef struct {
	UserID       int64
	AppSessionID string
	SessionID    string
	ClientID     string
}

type AttachSessionIndex struct {
	mu           sync.Mutex
	byAppSession map[string]map[string]AttachSessionRef
	byUser       map[int64]map[string]AttachSessionRef
}

func NewAttachSessionIndex() *AttachSessionIndex {
	return &AttachSessionIndex{
		byAppSession: make(map[string]map[string]AttachSessionRef),
		byUser:       make(map[int64]map[string]AttachSessionRef),
	}
}

func (i *AttachSessionIndex) Add(ref AttachSessionRef) {
	if i == nil || ref.UserID == 0 || ref.AppSessionID == "" || ref.SessionID == "" || ref.ClientID == "" {
		return
	}

	key := ref.indexKey()
	i.mu.Lock()
	defer i.mu.Unlock()

	appRefs := i.byAppSession[ref.AppSessionID]
	if appRefs == nil {
		appRefs = make(map[string]AttachSessionRef)
		i.byAppSession[ref.AppSessionID] = appRefs
	}
	appRefs[key] = ref

	userRefs := i.byUser[ref.UserID]
	if userRefs == nil {
		userRefs = make(map[string]AttachSessionRef)
		i.byUser[ref.UserID] = userRefs
	}
	userRefs[key] = ref
}

func (i *AttachSessionIndex) Remove(ref AttachSessionRef) {
	if i == nil || ref.UserID == 0 || ref.AppSessionID == "" || ref.SessionID == "" || ref.ClientID == "" {
		return
	}

	key := ref.indexKey()
	i.mu.Lock()
	defer i.mu.Unlock()

	if appRefs := i.byAppSession[ref.AppSessionID]; appRefs != nil {
		delete(appRefs, key)
		if len(appRefs) == 0 {
			delete(i.byAppSession, ref.AppSessionID)
		}
	}
	if userRefs := i.byUser[ref.UserID]; userRefs != nil {
		delete(userRefs, key)
		if len(userRefs) == 0 {
			delete(i.byUser, ref.UserID)
		}
	}
}

func (i *AttachSessionIndex) DisconnectAppSession(registry *Registry, appSessionID, reason string) int {
	if i == nil || appSessionID == "" {
		return 0
	}
	return i.disconnectRefs(registry, i.snapshotByAppSession(appSessionID), reason)
}

func (i *AttachSessionIndex) DisconnectUser(registry *Registry, userID int64, reason string) int {
	if i == nil || userID == 0 {
		return 0
	}
	return i.disconnectRefs(registry, i.snapshotByUser(userID), reason)
}

func (i *AttachSessionIndex) snapshotByAppSession(appSessionID string) []AttachSessionRef {
	i.mu.Lock()
	defer i.mu.Unlock()

	refs := i.byAppSession[appSessionID]
	out := make([]AttachSessionRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ref)
	}
	return out
}

func (i *AttachSessionIndex) snapshotByUser(userID int64) []AttachSessionRef {
	i.mu.Lock()
	defer i.mu.Unlock()

	refs := i.byUser[userID]
	out := make([]AttachSessionRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ref)
	}
	return out
}

func (i *AttachSessionIndex) disconnectRefs(registry *Registry, refs []AttachSessionRef, reason string) int {
	if registry == nil {
		return 0
	}

	disconnected := 0
	for _, ref := range refs {
		if registry.DetachClient(ref.SessionID, ref.ClientID, reason) {
			disconnected++
		}
	}
	return disconnected
}

func (r AttachSessionRef) indexKey() string {
	return r.SessionID + "\x00" + r.ClientID
}
