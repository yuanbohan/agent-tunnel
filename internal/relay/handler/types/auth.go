package types

type RegisterRequest struct {
	InviteCode string `json:"invite_code"`
	Username   string `json:"username"`
	Password   string `json:"password"`
}

type LoginRequest struct {
	Username          string `json:"username"`
	Password          string `json:"password"`
	ClientFingerprint string `json:"client_fingerprint,omitempty"`
	DeviceFingerprint string `json:"device_fingerprint,omitempty"`
}

type RefreshRequest struct {
	RefreshToken      string `json:"refresh_token"`
	ClientFingerprint string `json:"client_fingerprint,omitempty"`
	DeviceFingerprint string `json:"device_fingerprint,omitempty"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type AppSessionResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
	AccountID    string `json:"account_id"`
}

type AccountPolicyResponse struct {
	AccountID string `json:"account_id"`
	Tier      string `json:"tier"`
}
