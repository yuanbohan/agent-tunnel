package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	connectivitypairing "yuanbohan/tunnel/internal/connectivity/pairing"
)

const (
	PairingStateVersion  = 2
	PairingInvitationTTL = 5 * time.Minute

	TrustedDeviceStatusTrusted = "trusted"
	TrustedDeviceStatusRevoked = "revoked"
)

var (
	ErrPairingInvitationNotFound = errors.New("pairing invitation not found")
	ErrPairingInvitationExpired  = errors.New("pairing invitation expired")
	ErrPairingInvitationConsumed = errors.New("pairing invitation already consumed")
	ErrPairingSASMismatch        = errors.New("pairing sas mismatch")
	ErrTrustedDeviceNotFound     = errors.New("trusted device not found")
	ErrInvalidAndroidFingerprint = errors.New("invalid android device fingerprint")

	pairingStateMu sync.Mutex
)

type PairingState struct {
	Version          int                            `json:"version"`
	Invitations      []PairingInvitationRecord      `json:"invitations"`
	PendingResponses []PendingPairingResponseRecord `json:"pending_responses,omitempty"`
	TrustedDevices   []TrustedAndroidDevice         `json:"trusted_clients"`
}

func (s *PairingState) UnmarshalJSON(payload []byte) error {
	var raw pairingStateJSON
	if err := json.Unmarshal(payload, &raw); err != nil {
		return err
	}
	s.Version = raw.Version
	s.Invitations = make([]PairingInvitationRecord, 0, len(raw.Invitations))
	for _, invitation := range raw.Invitations {
		s.Invitations = append(s.Invitations, invitation.record())
	}
	s.PendingResponses = make([]PendingPairingResponseRecord, 0, len(raw.PendingResponses))
	for _, pending := range raw.PendingResponses {
		s.PendingResponses = append(s.PendingResponses, pending.record())
	}
	if raw.TrustedClients != nil {
		s.TrustedDevices = raw.TrustedClients
	} else {
		s.TrustedDevices = raw.TrustedDevices
	}
	return nil
}

type pairingStateJSON struct {
	Version          int                                `json:"version"`
	Invitations      []pairingInvitationRecordJSON      `json:"invitations"`
	PendingResponses []pendingPairingResponseRecordJSON `json:"pending_responses,omitempty"`
	TrustedClients   []TrustedAndroidDevice             `json:"trusted_clients"`
	TrustedDevices   []TrustedAndroidDevice             `json:"trusted_devices"`
}

type PairingInvitationRecord struct {
	InvitationID      string `json:"invitation_id"`
	CorrelationID     string `json:"correlation_id"`
	Nonce             string `json:"nonce"`
	AccountID         string `json:"account_id,omitempty"`
	RelayBaseURL      string `json:"relay_base_url"`
	DeviceID          string `json:"device_id"`
	DaemonFingerprint string `json:"computer_fingerprint"`
	ExpiresAt         int64  `json:"expires_at"`
	CreatedAt         int64  `json:"created_at"`
	ConsumedAt        *int64 `json:"consumed_at,omitempty"`
}

type pairingInvitationRecordJSON struct {
	InvitationID            string `json:"invitation_id"`
	CorrelationID           string `json:"correlation_id"`
	Nonce                   string `json:"nonce"`
	AccountID               string `json:"account_id,omitempty"`
	RelayBaseURL            string `json:"relay_base_url"`
	DeviceID                string `json:"device_id"`
	ComputerFingerprint     string `json:"computer_fingerprint"`
	LegacyDaemonFingerprint string `json:"daemon_fingerprint"`
	ExpiresAt               int64  `json:"expires_at"`
	CreatedAt               int64  `json:"created_at"`
	ConsumedAt              *int64 `json:"consumed_at,omitempty"`
}

func (r pairingInvitationRecordJSON) record() PairingInvitationRecord {
	fingerprint := r.ComputerFingerprint
	if fingerprint == "" {
		fingerprint = r.LegacyDaemonFingerprint
	}
	return PairingInvitationRecord{
		InvitationID:      r.InvitationID,
		CorrelationID:     r.CorrelationID,
		Nonce:             r.Nonce,
		AccountID:         r.AccountID,
		RelayBaseURL:      r.RelayBaseURL,
		DeviceID:          r.DeviceID,
		DaemonFingerprint: fingerprint,
		ExpiresAt:         r.ExpiresAt,
		CreatedAt:         r.CreatedAt,
		ConsumedAt:        r.ConsumedAt,
	}
}

type TrustedAndroidDevice struct {
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"public_key"`
	DisplayName string `json:"display_name,omitempty"`
	AccountID   string `json:"account_id,omitempty"`
	Status      string `json:"status"`
	PairedAt    int64  `json:"paired_at"`
	RevokedAt   *int64 `json:"revoked_at,omitempty"`
	Warning     string `json:"warning,omitempty"`
}

type PendingPairingResponseRecord struct {
	InvitationID       string `json:"invitation_id"`
	CorrelationID      string `json:"correlation_id"`
	AccountID          string `json:"account_id"`
	AndroidPublicKey   string `json:"client_public_key"`
	AndroidFingerprint string `json:"client_fingerprint"`
	AndroidDisplayName string `json:"client_display_name,omitempty"`
	Signature          string `json:"signature"`
	SAS                string `json:"sas"`
	ReceivedAt         int64  `json:"received_at"`
	ExpiresAt          int64  `json:"expires_at"`
}

type pendingPairingResponseRecordJSON struct {
	InvitationID         string `json:"invitation_id"`
	CorrelationID        string `json:"correlation_id"`
	AccountID            string `json:"account_id"`
	ClientPublicKey      string `json:"client_public_key"`
	LegacyAndroidPubKey  string `json:"android_pubkey"`
	ClientFingerprint    string `json:"client_fingerprint"`
	LegacyAndroidFP      string `json:"android_fingerprint"`
	ClientDisplayName    string `json:"client_display_name,omitempty"`
	LegacyAndroidDisplay string `json:"android_display_name,omitempty"`
	Signature            string `json:"signature"`
	SAS                  string `json:"sas"`
	ReceivedAt           int64  `json:"received_at"`
	ExpiresAt            int64  `json:"expires_at"`
}

func (r pendingPairingResponseRecordJSON) record() PendingPairingResponseRecord {
	publicKey := r.ClientPublicKey
	if publicKey == "" {
		publicKey = r.LegacyAndroidPubKey
	}
	fingerprint := r.ClientFingerprint
	if fingerprint == "" {
		fingerprint = r.LegacyAndroidFP
	}
	displayName := r.ClientDisplayName
	if displayName == "" {
		displayName = r.LegacyAndroidDisplay
	}
	return PendingPairingResponseRecord{
		InvitationID:       r.InvitationID,
		CorrelationID:      r.CorrelationID,
		AccountID:          r.AccountID,
		AndroidPublicKey:   publicKey,
		AndroidFingerprint: fingerprint,
		AndroidDisplayName: displayName,
		Signature:          r.Signature,
		SAS:                r.SAS,
		ReceivedAt:         r.ReceivedAt,
		ExpiresAt:          r.ExpiresAt,
	}
}

type PairInvitation struct {
	Version           int    `json:"version"`
	InvitationID      string `json:"invitation_id"`
	CorrelationID     string `json:"correlation_id"`
	Nonce             string `json:"nonce"`
	AccountID         string `json:"account_id,omitempty"`
	RelayBaseURL      string `json:"relay_base_url"`
	DeviceID          string `json:"computer_id"`
	DisplayName       string `json:"computer_display_name,omitempty"`
	DaemonPublicKey   string `json:"computer_public_key"`
	DaemonFingerprint string `json:"computer_fingerprint"`
	ExpiresAt         int64  `json:"expires_at"`
	Signature         string `json:"signature"`
}

type PairInvitationOptions struct {
	BaseURL        string
	DeviceID       string
	DisplayName    string
	AccountID      string
	CorrelationID  string
	Now            time.Time
	DaemonIdentity ConnectivityIdentity
}

type PairingCompletion struct {
	Device  TrustedAndroidDevice `json:"device"`
	SAS     string               `json:"sas"`
	Warning string               `json:"warning,omitempty"`
}

type PendingPairingResponse struct {
	InvitationID       string `json:"invitation_id"`
	CorrelationID      string `json:"correlation_id"`
	AccountID          string `json:"account_id"`
	AndroidFingerprint string `json:"client_fingerprint"`
	AndroidDisplayName string `json:"client_display_name,omitempty"`
	SAS                string `json:"sas"`
	ReceivedAt         int64  `json:"received_at"`
	ExpiresAt          int64  `json:"expires_at"`
}

func LoadPairingState(paths Paths) (PairingState, error) {
	payload, err := os.ReadFile(paths.PairingStateFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PairingState{Version: PairingStateVersion}, nil
		}
		return PairingState{}, err
	}
	var state PairingState
	if err := json.Unmarshal(payload, &state); err != nil {
		return PairingState{}, err
	}
	if state.Version == 0 {
		state.Version = PairingStateVersion
	}
	if state.Version == 1 {
		state.Version = PairingStateVersion
	}
	if state.Version != PairingStateVersion {
		return PairingState{}, fmt.Errorf("unsupported pairing state version %d", state.Version)
	}
	return state, nil
}

func SavePairingState(paths Paths, state PairingState) error {
	state.Version = PairingStateVersion
	return writePrivateJSONFile(paths.PairingStateFile, state)
}

func CreatePairInvitation(paths Paths, opts PairInvitationOptions) (PairInvitation, error) {
	pairingStateMu.Lock()
	defer pairingStateMu.Unlock()
	return createPairInvitationLocked(paths, opts)
}

func createPairInvitationLocked(paths Paths, opts PairInvitationOptions) (PairInvitation, error) {
	if err := validateConnectivityIdentity(opts.DaemonIdentity); err != nil {
		return PairInvitation{}, err
	}
	now := opts.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	invitationID, err := newOpaqueID("pair", 12)
	if err != nil {
		return PairInvitation{}, err
	}
	correlationID := strings.TrimSpace(opts.CorrelationID)
	if correlationID == "" {
		correlationID, err = newOpaqueID("corr", 12)
		if err != nil {
			return PairInvitation{}, err
		}
	}
	nonce, err := randomHex(16)
	if err != nil {
		return PairInvitation{}, err
	}
	expiresAt := now.Add(PairingInvitationTTL).Unix()

	state, err := LoadPairingState(paths)
	if err != nil {
		return PairInvitation{}, err
	}
	state.Invitations = sweepExpiredInvitationRecords(state.Invitations, now)
	record := PairingInvitationRecord{
		InvitationID:      invitationID,
		CorrelationID:     correlationID,
		Nonce:             nonce,
		AccountID:         strings.TrimSpace(opts.AccountID),
		RelayBaseURL:      strings.TrimSpace(opts.BaseURL),
		DeviceID:          strings.TrimSpace(opts.DeviceID),
		DaemonFingerprint: opts.DaemonIdentity.Fingerprint,
		ExpiresAt:         expiresAt,
		CreatedAt:         now.Unix(),
	}
	state.Invitations = append(state.Invitations, record)
	if err := SavePairingState(paths, state); err != nil {
		return PairInvitation{}, err
	}

	invitation := PairInvitation{
		Version:           PairingStateVersion,
		InvitationID:      invitationID,
		CorrelationID:     correlationID,
		Nonce:             nonce,
		AccountID:         record.AccountID,
		RelayBaseURL:      record.RelayBaseURL,
		DeviceID:          record.DeviceID,
		DisplayName:       strings.TrimSpace(opts.DisplayName),
		DaemonPublicKey:   hex.EncodeToString(opts.DaemonIdentity.PublicKey),
		DaemonFingerprint: opts.DaemonIdentity.Fingerprint,
		ExpiresAt:         expiresAt,
	}
	signed, err := connectivitypairing.SignInvitation(connectivitypairing.Invitation{
		Version:           connectivitypairing.Version,
		AccountID:         invitation.AccountID,
		DaemonID:          invitation.DeviceID,
		DaemonDisplayName: invitation.DisplayName,
		DaemonPublicKey:   invitation.DaemonPublicKey,
		DaemonFingerprint: invitation.DaemonFingerprint,
		InvitationID:      invitation.InvitationID,
		CorrelationID:     invitation.CorrelationID,
		Nonce:             invitation.Nonce,
		ExpiresAt:         invitation.ExpiresAt,
		RelayBaseURL:      invitation.RelayBaseURL,
	}, opts.DaemonIdentity.PrivateKey)
	if err != nil {
		return PairInvitation{}, err
	}
	invitation.Signature = signed.Signature
	return invitation, nil
}

func ConsumePairInvitation(paths Paths, invitationID string, now time.Time) (PairingInvitationRecord, error) {
	pairingStateMu.Lock()
	defer pairingStateMu.Unlock()
	state, err := LoadPairingState(paths)
	if err != nil {
		return PairingInvitationRecord{}, err
	}
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for i := range state.Invitations {
		record := &state.Invitations[i]
		if record.InvitationID != invitationID {
			continue
		}
		if record.ConsumedAt != nil {
			return PairingInvitationRecord{}, ErrPairingInvitationConsumed
		}
		if !time.Unix(record.ExpiresAt, 0).After(now) {
			return PairingInvitationRecord{}, ErrPairingInvitationExpired
		}
		consumedAt := now.Unix()
		record.ConsumedAt = &consumedAt
		if err := SavePairingState(paths, state); err != nil {
			return PairingInvitationRecord{}, err
		}
		return *record, nil
	}
	return PairingInvitationRecord{}, ErrPairingInvitationNotFound
}

func CompletePairingResponse(paths Paths, response connectivitypairing.AndroidResponse, expectedSAS string, now time.Time) (PairingCompletion, error) {
	pairingStateMu.Lock()
	defer pairingStateMu.Unlock()
	return completePairingResponseLocked(paths, response, expectedSAS, now)
}

func completePairingResponseLocked(paths Paths, response connectivitypairing.AndroidResponse, expectedSAS string, now time.Time) (PairingCompletion, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	state, err := LoadPairingState(paths)
	if err != nil {
		return PairingCompletion{}, err
	}
	identity, err := ReadConnectivityIdentity(paths)
	if err != nil {
		return PairingCompletion{}, err
	}
	for i := range state.Invitations {
		record := &state.Invitations[i]
		if record.InvitationID != response.InvitationID {
			continue
		}
		if record.ConsumedAt != nil {
			return PairingCompletion{}, ErrPairingInvitationConsumed
		}
		if !time.Unix(record.ExpiresAt, 0).After(now) {
			return PairingCompletion{}, ErrPairingInvitationExpired
		}
		verified, err := verifyPairingResponseForRecord(*record, identity, response, now)
		if err != nil {
			return PairingCompletion{}, err
		}
		consumedAt := now.Unix()
		record.ConsumedAt = &consumedAt
		state.PendingResponses = removePendingPairingResponseRecords(state.PendingResponses, response.InvitationID)
		if strings.TrimSpace(expectedSAS) != verified.SAS {
			if saveErr := SavePairingState(paths, state); saveErr != nil {
				return PairingCompletion{}, saveErr
			}
			return PairingCompletion{}, ErrPairingSASMismatch
		}
		device := TrustedAndroidDevice{
			Fingerprint: verified.AndroidFingerprint,
			PublicKey:   response.AndroidPublicKey,
			DisplayName: strings.TrimSpace(response.AndroidDisplayName),
			AccountID:   strings.TrimSpace(response.AccountID),
			Status:      TrustedDeviceStatusTrusted,
			PairedAt:    consumedAt,
		}
		state.TrustedDevices = upsertTrustedDeviceRecord(state.TrustedDevices, device)
		if err := SavePairingState(paths, state); err != nil {
			return PairingCompletion{}, err
		}
		return PairingCompletion{Device: device, SAS: verified.SAS}, nil
	}
	return PairingCompletion{}, ErrPairingInvitationNotFound
}

func StorePendingPairingResponse(paths Paths, response connectivitypairing.AndroidResponse, now time.Time) (PendingPairingResponse, error) {
	pairingStateMu.Lock()
	defer pairingStateMu.Unlock()
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	state, err := LoadPairingState(paths)
	if err != nil {
		return PendingPairingResponse{}, err
	}
	identity, err := ReadConnectivityIdentity(paths)
	if err != nil {
		return PendingPairingResponse{}, err
	}
	for i := range state.Invitations {
		record := &state.Invitations[i]
		if record.InvitationID != response.InvitationID {
			continue
		}
		if record.ConsumedAt != nil {
			return PendingPairingResponse{}, ErrPairingInvitationConsumed
		}
		if !time.Unix(record.ExpiresAt, 0).After(now) {
			return PendingPairingResponse{}, ErrPairingInvitationExpired
		}
		verified, err := verifyPairingResponseForRecord(*record, identity, response, now)
		if err != nil {
			return PendingPairingResponse{}, err
		}
		pending := PendingPairingResponseRecord{
			InvitationID:       response.InvitationID,
			CorrelationID:      response.CorrelationID,
			AccountID:          strings.TrimSpace(response.AccountID),
			AndroidPublicKey:   strings.TrimSpace(response.AndroidPublicKey),
			AndroidFingerprint: verified.AndroidFingerprint,
			AndroidDisplayName: strings.TrimSpace(response.AndroidDisplayName),
			Signature:          strings.TrimSpace(response.Signature),
			SAS:                verified.SAS,
			ReceivedAt:         now.Unix(),
			ExpiresAt:          record.ExpiresAt,
		}
		state.PendingResponses = upsertPendingPairingResponseRecord(state.PendingResponses, pending)
		if err := SavePairingState(paths, state); err != nil {
			return PendingPairingResponse{}, err
		}
		return pendingPairingResponseFromRecord(pending), nil
	}
	return PendingPairingResponse{}, ErrPairingInvitationNotFound
}

func ListPendingPairingResponses(paths Paths) ([]PendingPairingResponse, error) {
	pairingStateMu.Lock()
	defer pairingStateMu.Unlock()
	state, err := LoadPairingState(paths)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	pending := make([]PendingPairingResponse, 0, len(state.PendingResponses))
	for _, record := range state.PendingResponses {
		if !time.Unix(record.ExpiresAt, 0).After(now) {
			continue
		}
		pending = append(pending, pendingPairingResponseFromRecord(record))
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].ReceivedAt == pending[j].ReceivedAt {
			return pending[i].InvitationID < pending[j].InvitationID
		}
		return pending[i].ReceivedAt > pending[j].ReceivedAt
	})
	return pending, nil
}

func ConfirmPendingPairingResponse(paths Paths, invitationID string, expectedSAS string, now time.Time) (PairingCompletion, error) {
	pairingStateMu.Lock()
	defer pairingStateMu.Unlock()
	state, err := LoadPairingState(paths)
	if err != nil {
		return PairingCompletion{}, err
	}
	for _, pending := range state.PendingResponses {
		if pending.InvitationID != strings.TrimSpace(invitationID) {
			continue
		}
		return completePairingResponseLocked(paths, connectivitypairing.AndroidResponse{
			Version:            connectivitypairing.Version,
			AccountID:          pending.AccountID,
			InvitationID:       pending.InvitationID,
			CorrelationID:      pending.CorrelationID,
			AndroidPublicKey:   pending.AndroidPublicKey,
			AndroidFingerprint: pending.AndroidFingerprint,
			AndroidDisplayName: pending.AndroidDisplayName,
			Signature:          pending.Signature,
		}, expectedSAS, now)
	}
	return PairingCompletion{}, ErrPairingInvitationNotFound
}

func UpsertTrustedAndroidDevice(paths Paths, device TrustedAndroidDevice) error {
	pairingStateMu.Lock()
	defer pairingStateMu.Unlock()
	fingerprint, err := normalizeAndroidFingerprint(device.Fingerprint)
	if err != nil {
		return err
	}
	state, err := LoadPairingState(paths)
	if err != nil {
		return err
	}
	device.Fingerprint = fingerprint
	device.Status = TrustedDeviceStatusTrusted
	device.RevokedAt = nil
	if device.PairedAt == 0 {
		device.PairedAt = time.Now().UTC().Unix()
	}
	state.TrustedDevices = upsertTrustedDeviceRecord(state.TrustedDevices, device)
	return SavePairingState(paths, state)
}

func ListTrustedAndroidDevices(paths Paths) ([]TrustedAndroidDevice, error) {
	pairingStateMu.Lock()
	defer pairingStateMu.Unlock()
	state, err := LoadPairingState(paths)
	if err != nil {
		return nil, err
	}
	devices := make([]TrustedAndroidDevice, 0, len(state.TrustedDevices))
	for _, device := range state.TrustedDevices {
		if device.Status == TrustedDeviceStatusRevoked || device.RevokedAt != nil {
			continue
		}
		if device.Status == "" {
			device.Status = TrustedDeviceStatusTrusted
		}
		devices = append(devices, device)
	}
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].PairedAt == devices[j].PairedAt {
			return devices[i].Fingerprint < devices[j].Fingerprint
		}
		return devices[i].PairedAt > devices[j].PairedAt
	})
	return devices, nil
}

func RevokeTrustedAndroidDevice(paths Paths, fingerprint string) (TrustedAndroidDevice, error) {
	pairingStateMu.Lock()
	defer pairingStateMu.Unlock()
	normalized, err := normalizeAndroidFingerprint(fingerprint)
	if err != nil {
		return TrustedAndroidDevice{}, err
	}
	state, err := LoadPairingState(paths)
	if err != nil {
		return TrustedAndroidDevice{}, err
	}
	now := time.Now().UTC().Unix()
	for i := range state.TrustedDevices {
		device := &state.TrustedDevices[i]
		if device.Fingerprint != normalized {
			continue
		}
		if device.RevokedAt == nil {
			device.RevokedAt = &now
		}
		device.Status = TrustedDeviceStatusRevoked
		if err := SavePairingState(paths, state); err != nil {
			return TrustedAndroidDevice{}, err
		}
		return *device, nil
	}
	return TrustedAndroidDevice{}, ErrTrustedDeviceNotFound
}

func sweepExpiredInvitationRecords(records []PairingInvitationRecord, now time.Time) []PairingInvitationRecord {
	out := records[:0]
	for _, record := range records {
		if time.Unix(record.ExpiresAt, 0).After(now) {
			out = append(out, record)
		}
	}
	return out
}

func verifyPairingResponseForRecord(record PairingInvitationRecord, identity ConnectivityIdentity, response connectivitypairing.AndroidResponse, now time.Time) (connectivitypairing.VerifiedPairing, error) {
	invitation, err := connectivitypairing.SignInvitation(connectivitypairing.Invitation{
		Version:           connectivitypairing.Version,
		AccountID:         record.AccountID,
		DaemonID:          record.DeviceID,
		DaemonPublicKey:   hex.EncodeToString(identity.PublicKey),
		DaemonFingerprint: identity.Fingerprint,
		InvitationID:      record.InvitationID,
		CorrelationID:     record.CorrelationID,
		Nonce:             record.Nonce,
		ExpiresAt:         record.ExpiresAt,
		RelayBaseURL:      record.RelayBaseURL,
	}, identity.PrivateKey)
	if err != nil {
		return connectivitypairing.VerifiedPairing{}, err
	}
	return connectivitypairing.VerifyPairingResponse(invitation, response, now.Unix())
}

func randomHex(numBytes int) (string, error) {
	buffer := make([]byte, numBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func normalizeAndroidFingerprint(raw string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if len(normalized) != 64 {
		return "", ErrInvalidAndroidFingerprint
	}
	if _, err := hex.DecodeString(normalized); err != nil {
		return "", ErrInvalidAndroidFingerprint
	}
	return normalized, nil
}

func upsertTrustedDeviceRecord(devices []TrustedAndroidDevice, device TrustedAndroidDevice) []TrustedAndroidDevice {
	for i := range devices {
		if devices[i].Fingerprint == device.Fingerprint {
			devices[i] = device
			return devices
		}
	}
	return append(devices, device)
}

func upsertPendingPairingResponseRecord(records []PendingPairingResponseRecord, record PendingPairingResponseRecord) []PendingPairingResponseRecord {
	for i := range records {
		if records[i].InvitationID == record.InvitationID {
			records[i] = record
			return records
		}
	}
	return append(records, record)
}

func removePendingPairingResponseRecords(records []PendingPairingResponseRecord, invitationID string) []PendingPairingResponseRecord {
	out := records[:0]
	for _, record := range records {
		if record.InvitationID != invitationID {
			out = append(out, record)
		}
	}
	return out
}

func pendingPairingResponseFromRecord(record PendingPairingResponseRecord) PendingPairingResponse {
	return PendingPairingResponse{
		InvitationID:       record.InvitationID,
		CorrelationID:      record.CorrelationID,
		AccountID:          record.AccountID,
		AndroidFingerprint: record.AndroidFingerprint,
		AndroidDisplayName: record.AndroidDisplayName,
		SAS:                record.SAS,
		ReceivedAt:         record.ReceivedAt,
		ExpiresAt:          record.ExpiresAt,
	}
}
