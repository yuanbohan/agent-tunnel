package relay

const (
	OperatorInviteCodesPath   = "/operator/invite-codes"
	OperatorInviteDisablePath = "/operator/invite-codes/disable"
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
