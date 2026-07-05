// Package protocol implements the daemon side of the whatevr protocol
// (PROTOCOL.md): newline-delimited JSON over a Unix socket, with a
// request/response/event envelope and a subscription-based view model.
//
// This package is built alongside the legacy gRPC server during the
// migration and replaces it entirely once the migration finishes.
package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"

	"whatevrd/internal/app"
)

// ProtocolVersion is the single integer protocol version negotiated by
// `hello`. Within a version, additions are always allowed; see PROTOCOL.md.
const ProtocolVersion = 1

// Version is the daemon's reported version. It defaults to "dev" and is
// overridden at build time via -ldflags "-X whatevrd/internal/protocol.Version=...".
var Version = "dev"

// Core error codes from PROTOCOL.md. Methods may document additional codes.
const (
	CodeInvalidRequest = "invalid_request"
	CodeUnknownMethod  = "unknown_method"
	CodeInvalidParams  = "invalid_params"
	CodeNotFound       = "not_found"
	CodeNotLoggedIn    = "not_logged_in"
	CodeNotConnected   = "not_connected"
	CodeAlreadyExists  = "already_exists"
	CodeExpired        = "expired"
	CodeRejected       = "rejected"
	CodeIO             = "io"
	CodeInternal       = "internal"
)

// Error is the wire error object: a stable machine-readable code and a
// human-readable message.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}

func errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// request is the single frontend→daemon shape on the wire.
type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// response carries exactly one of Result or Error, correlated by the
// client-chosen ID echoed back verbatim.
type response struct {
	ID     json.RawMessage `json:"id"`
	Result any             `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// nullID is echoed when a request's own id is missing or unusable.
var nullID = json.RawMessage("null")

// validRequestID reports whether raw is a client-chosen number or string,
// the only id types PROTOCOL.md allows.
func validRequestID(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	c := trimmed[0]
	return c == '"' || c == '-' || (c >= '0' && c <= '9')
}

// stateString maps the daemon connection state onto the protocol's
// lowercase state names.
func stateString(s app.State) string {
	switch s {
	case app.StateNeedLogin:
		return "need_login"
	case app.StateConnecting:
		return "connecting"
	case app.StateOnline:
		return "online"
	case app.StateReconnecting:
		return "reconnecting"
	case app.StateOffline:
		return "offline"
	default:
		return "starting"
	}
}
