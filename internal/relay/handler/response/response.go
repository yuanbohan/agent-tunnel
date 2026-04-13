package response

import (
	"encoding/json"
	"net/http"
)

type Envelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Body    any    `json:"body"`
}

const (
	CodeSuccess            = 0
	CodeInvalidRequest     = 1001
	CodeRateLimited        = 1002
	CodeUsernameTaken      = 1003
	CodePasswordTooShort   = 1004
	CodeInvalidAccessCode  = 1005
	CodeInviteCodeNotFound = 1006
	CodeInviteCodeExpired  = 1007
	CodeInviteCodeDisabled = 1008
	CodeInviteCodeConsumed = 1009
	CodeInvalidUsername    = 1010
	CodeInvalidCredentials = 1011
	CodeInvalidSession     = 1012
	CodeAgentTokenNotFound = 1013
	CodeUserNotFound       = 1014
	CodeSessionNotFound    = 1015
	CodeUnauthorized       = 1016
	CodeForbidden          = 1017
	CodeNotFound           = 1018
	CodeMethodNotAllowed   = 1019
	CodeServiceUnavailable = 2001
	CodeInternalError      = 2002
)

type reasonInfo struct {
	Code    int
	Message string
}

var reasonMap = map[string]reasonInfo{
	"invalid_request": {
		Code:    CodeInvalidRequest,
		Message: "The request is invalid.",
	},
	"rate_limited": {
		Code:    CodeRateLimited,
		Message: "Too many requests. Please try again later.",
	},
	"username_taken": {
		Code:    CodeUsernameTaken,
		Message: "The username is already taken.",
	},
	"password_too_short": {
		Code:    CodePasswordTooShort,
		Message: "The password must be at least 6 characters.",
	},
	"invalid_access_code": {
		Code:    CodeInvalidAccessCode,
		Message: "Invalid access code.",
	},
	"invite_code_not_found": {
		Code:    CodeInviteCodeNotFound,
		Message: "This access code is invalid.",
	},
	"invite_code_expired": {
		Code:    CodeInviteCodeExpired,
		Message: "This access code has expired.",
	},
	"invite_code_disabled": {
		Code:    CodeInviteCodeDisabled,
		Message: "This access code has been disabled.",
	},
	"invite_code_consumed": {
		Code:    CodeInviteCodeConsumed,
		Message: "This access code has already been used.",
	},
	"invalid_username": {
		Code:    CodeInvalidUsername,
		Message: "The username is invalid.",
	},
	"invalid_credentials": {
		Code:    CodeInvalidCredentials,
		Message: "The username or password is invalid.",
	},
	"invalid_session": {
		Code:    CodeInvalidSession,
		Message: "The session is invalid.",
	},
	"agent_token_not_found": {
		Code:    CodeAgentTokenNotFound,
		Message: "This agent token was not found.",
	},
	"user_not_found": {
		Code:    CodeUserNotFound,
		Message: "The user was not found.",
	},
	"session_not_found": {
		Code:    CodeSessionNotFound,
		Message: "The session was not found or is offline.",
	},
	"unauthorized": {
		Code:    CodeUnauthorized,
		Message: "The request is unauthorized.",
	},
	"forbidden": {
		Code:    CodeForbidden,
		Message: "The request is forbidden.",
	},
	"not_found": {
		Code:    CodeNotFound,
		Message: "The requested endpoint was not found.",
	},
	"method_not_allowed": {
		Code:    CodeMethodNotAllowed,
		Message: "The HTTP method is not allowed for this endpoint.",
	},
	"service_unavailable": {
		Code:    CodeServiceUnavailable,
		Message: "The service is temporarily unavailable.",
	},
	"internal_error": {
		Code:    CodeInternalError,
		Message: "An unexpected internal error occurred.",
	},
}

func Write(w http.ResponseWriter, status int, payload any) {
	WriteWithMessage(w, status, CodeSuccess, "success", payload)
}

func WriteError(w http.ResponseWriter, status int, reason string) {
	WriteErrorWithMessage(w, status, reason, "")
}

func WriteErrorWithMessage(w http.ResponseWriter, status int, reason, message string) {
	code, resolvedMessage := codeAndMessageFromReason(reason)
	if message != "" {
		resolvedMessage = message
	}
	WriteWithMessage(w, status, code, resolvedMessage, nil)
}

func WriteWithMessage(w http.ResponseWriter, status int, code int, message string, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Envelope{
		Code:    code,
		Message: message,
		Body:    body,
	})
}

func codeAndMessageFromReason(reason string) (int, string) {
	if detail, ok := reasonMap[reason]; ok {
		return detail.Code, detail.Message
	}
	return CodeInternalError, reason
}
