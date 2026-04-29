package pairing

import (
	"crypto/ed25519"
	"encoding/hex"
	"strconv"
)

type Invitation struct {
	Version           int    `json:"version"`
	AccountID         string `json:"account_id,omitempty"`
	DaemonID          string `json:"daemon_id"`
	DaemonDisplayName string `json:"daemon_display_name,omitempty"`
	DaemonPublicKey   string `json:"daemon_pubkey"`
	DaemonFingerprint string `json:"daemon_fingerprint"`
	InvitationID      string `json:"invitation_id"`
	CorrelationID     string `json:"correlation_id"`
	Nonce             string `json:"nonce"`
	ExpiresAt         int64  `json:"expires_at"`
	RelayBaseURL      string `json:"relay_base_url"`
	Signature         string `json:"signature,omitempty"`
}

type AndroidResponse struct {
	Version            int    `json:"version"`
	AccountID          string `json:"account_id"`
	InvitationID       string `json:"invitation_id"`
	CorrelationID      string `json:"correlation_id"`
	AndroidPublicKey   string `json:"android_pubkey"`
	AndroidFingerprint string `json:"android_fingerprint"`
	AndroidDisplayName string `json:"android_display_name,omitempty"`
	Signature          string `json:"signature,omitempty"`
}

type VerifiedPairing struct {
	DaemonPublicKey    ed25519.PublicKey
	AndroidPublicKey   ed25519.PublicKey
	InvitationID       string
	Nonce              []byte
	SAS                string
	AndroidFingerprint string
}

func SignInvitation(invitation Invitation, privateKey ed25519.PrivateKey) (Invitation, error) {
	if invitation.Version == 0 {
		invitation.Version = Version
	}
	if invitation.Version != Version {
		return Invitation{}, ErrInvalidVersion
	}
	publicKey, err := decodePublicKeyHex(invitation.DaemonPublicKey)
	if err != nil {
		return Invitation{}, err
	}
	if err := validateFingerprint(publicKey, invitation.DaemonFingerprint); err != nil {
		return Invitation{}, err
	}
	canonical, err := canonicalInvitation(invitation)
	if err != nil {
		return Invitation{}, err
	}
	invitation.Signature = hex.EncodeToString(ed25519.Sign(privateKey, canonical))
	return invitation, nil
}

func VerifyInvitation(invitation Invitation) error {
	if invitation.Version != Version {
		return ErrInvalidVersion
	}
	publicKey, err := decodePublicKeyHex(invitation.DaemonPublicKey)
	if err != nil {
		return err
	}
	if err := validateFingerprint(publicKey, invitation.DaemonFingerprint); err != nil {
		return err
	}
	signature, err := decodeHexField("signature", invitation.Signature)
	if err != nil {
		return err
	}
	if len(signature) != ed25519.SignatureSize {
		return ErrInvalidSignature
	}
	canonical, err := canonicalInvitation(invitation)
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, canonical, signature) {
		return ErrInvalidSignature
	}
	return nil
}

func SignAndroidResponse(response AndroidResponse, privateKey ed25519.PrivateKey) (AndroidResponse, error) {
	if response.Version == 0 {
		response.Version = Version
	}
	if response.Version != Version {
		return AndroidResponse{}, ErrInvalidVersion
	}
	publicKey, err := decodePublicKeyHex(response.AndroidPublicKey)
	if err != nil {
		return AndroidResponse{}, err
	}
	if err := validateFingerprint(publicKey, response.AndroidFingerprint); err != nil {
		return AndroidResponse{}, err
	}
	canonical, err := canonicalAndroidResponse(response)
	if err != nil {
		return AndroidResponse{}, err
	}
	response.Signature = hex.EncodeToString(ed25519.Sign(privateKey, canonical))
	return response, nil
}

func VerifyAndroidResponse(response AndroidResponse) error {
	if response.Version != Version {
		return ErrInvalidVersion
	}
	publicKey, err := decodePublicKeyHex(response.AndroidPublicKey)
	if err != nil {
		return err
	}
	if err := validateFingerprint(publicKey, response.AndroidFingerprint); err != nil {
		return err
	}
	signature, err := decodeHexField("signature", response.Signature)
	if err != nil {
		return err
	}
	if len(signature) != ed25519.SignatureSize {
		return ErrInvalidSignature
	}
	canonical, err := canonicalAndroidResponse(response)
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, canonical, signature) {
		return ErrInvalidSignature
	}
	return nil
}

func VerifyPairingResponse(invitation Invitation, response AndroidResponse, nowUnix int64) (VerifiedPairing, error) {
	if err := VerifyInvitation(invitation); err != nil {
		return VerifiedPairing{}, err
	}
	if err := VerifyAndroidResponse(response); err != nil {
		return VerifiedPairing{}, err
	}
	if invitation.InvitationID != response.InvitationID ||
		invitation.CorrelationID != response.CorrelationID ||
		(invitation.AccountID != "" && invitation.AccountID != response.AccountID) {
		return VerifiedPairing{}, ErrTranscriptMismatch
	}
	if nowUnix > 0 && invitation.ExpiresAt <= nowUnix {
		return VerifiedPairing{}, ErrInvitationExpired
	}
	daemonPublicKey, err := decodePublicKeyHex(invitation.DaemonPublicKey)
	if err != nil {
		return VerifiedPairing{}, err
	}
	androidPublicKey, err := decodePublicKeyHex(response.AndroidPublicKey)
	if err != nil {
		return VerifiedPairing{}, err
	}
	nonce, err := decodeHexField("nonce", invitation.Nonce)
	if err != nil {
		return VerifiedPairing{}, err
	}
	sas, err := ComputeSAS(daemonPublicKey, androidPublicKey, invitation.InvitationID, nonce)
	if err != nil {
		return VerifiedPairing{}, err
	}
	return VerifiedPairing{
		DaemonPublicKey:    daemonPublicKey,
		AndroidPublicKey:   androidPublicKey,
		InvitationID:       invitation.InvitationID,
		Nonce:              nonce,
		SAS:                sas,
		AndroidFingerprint: normalizeFingerprint(response.AndroidFingerprint),
	}, nil
}

func canonicalInvitation(invitation Invitation) ([]byte, error) {
	out := []byte("tunnel-pairing-invitation-v1")
	fields := [][]byte{
		[]byte(invitation.AccountID),
		[]byte(invitation.DaemonID),
		[]byte(invitation.DaemonDisplayName),
		[]byte(normalizeFingerprint(invitation.DaemonFingerprint)),
		[]byte(invitation.DaemonPublicKey),
		[]byte(invitation.InvitationID),
		[]byte(invitation.CorrelationID),
		[]byte(invitation.Nonce),
		[]byte(invitation.RelayBaseURL),
		[]byte(stringInt64(invitation.ExpiresAt)),
	}
	var err error
	for _, field := range fields {
		out, err = appendLengthPrefixed(out, field)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func canonicalAndroidResponse(response AndroidResponse) ([]byte, error) {
	out := []byte("tunnel-pairing-android-response-v1")
	fields := [][]byte{
		[]byte(response.AccountID),
		[]byte(response.InvitationID),
		[]byte(response.CorrelationID),
		[]byte(normalizeFingerprint(response.AndroidFingerprint)),
		[]byte(response.AndroidPublicKey),
		[]byte(response.AndroidDisplayName),
	}
	var err error
	for _, field := range fields {
		out, err = appendLengthPrefixed(out, field)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func stringInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}
