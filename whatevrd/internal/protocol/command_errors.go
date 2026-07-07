package protocol

import (
	"database/sql"
	"errors"
	"strings"

	"whatevrd/internal/app"
)

func mapCommandError(err error) *Error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return errorf(CodeNotFound, "%v", err)
	}
	// The daemon core raises transport-neutral app.CommandError values; the
	// protocol layer maps their kind to a protocol error code. This is the whole
	// contract — no gRPC status vocabulary reaches this package anymore.
	if ce, ok := app.AsCommandError(err); ok {
		return errorf(commandErrorCode(ce.Kind), "%s", ce.Message)
	}
	// Fallbacks for raw store/network errors that never went through the
	// CommandError contract and surface directly.
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "not connected") {
		return errorf(CodeNotConnected, "%v", err)
	}
	if strings.Contains(lower, "invalid jid") {
		return errorf(CodeInvalidParams, "%v", err)
	}
	return errorf(CodeInternal, "%v", err)
}

// commandErrorCode maps a daemon CommandError kind to the protocol error code
// clients see. F10: not-logged-in is its own code (was folded into not_connected
// before), and every raised failure now carries a deliberate code rather than
// falling through to internal.
func commandErrorCode(kind app.CommandErrorKind) string {
	switch kind {
	case app.CommandErrorInvalidArgument:
		return CodeInvalidParams
	case app.CommandErrorNotFound:
		return CodeNotFound
	case app.CommandErrorNotLoggedIn:
		return CodeNotLoggedIn
	case app.CommandErrorNotConnected:
		return CodeNotConnected
	case app.CommandErrorExpired:
		return CodeExpired
	case app.CommandErrorRejected:
		return CodeRejected
	case app.CommandErrorAlreadyExists:
		return CodeAlreadyExists
	default:
		return CodeInternal
	}
}
