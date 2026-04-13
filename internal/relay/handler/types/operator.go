package types

const (
	OperatorInviteCodesPath   = "/operator/invite-codes"
	OperatorInviteDisablePath = "/operator/invite-codes/disable"
	OperatorInviteListPath    = "/operator/invite-codes/list"
	OperatorDeleteUserPath    = "/operator/users/delete"
)

type OperatorCreateInvitesRequest struct {
	Count         int `json:"count"`
	ExpiresInDays int `json:"expires_in_days"`
}

type OperatorCreateInvitesResponse struct {
	Codes []string `json:"codes"`
}

type OperatorDisableInviteRequest struct {
	Code string `json:"code"`
}

type OperatorDeleteUserRequest struct {
	Username string `json:"username"`
}

type OperatorInviteCodeListEntry struct {
	Code               string `json:"code"`
	CreatedBy          string `json:"created_by"`
	CreatedAt          int64  `json:"created_at"`
	ExpiresAt          int64  `json:"expires_at"`
	Expired            bool   `json:"expired"`
	Available          bool   `json:"available"`
	Disabled           bool   `json:"disabled"`
	DisabledAt         *int64 `json:"disabled_at,omitempty"`
	DisabledBy         string `json:"disabled_by,omitempty"`
	Consumed           bool   `json:"consumed"`
	ConsumedAt         *int64 `json:"consumed_at,omitempty"`
	ConsumedByUserID   *int64 `json:"consumed_by_user_id,omitempty"`
	ConsumedByUsername string `json:"consumed_by_username,omitempty"`
}

type OperatorInviteCodesListResponse struct {
	Invites []OperatorInviteCodeListEntry `json:"invite_codes"`
}
