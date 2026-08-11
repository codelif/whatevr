package app

import (
	"errors"
	"fmt"
)

// CommandErrorKind classifies a daemon command failure independently of any
// transport. The daemon (app/wa) raises CommandError values; the frontend
// boundary maps the kind into its own vocabulary — the whatevr-protocol layer
// into its error codes — so no transport detail leaks into the daemon core.
type CommandErrorKind int

const (
	// CommandErrorInternal is an unexpected failure with no better classification.
	CommandErrorInternal CommandErrorKind = iota
	// CommandErrorInvalidArgument is a malformed or out-of-range caller input.
	CommandErrorInvalidArgument
	// CommandErrorNotFound is a reference to something that does not exist.
	CommandErrorNotFound
	// CommandErrorNotLoggedIn means there is no authenticated WhatsApp session.
	CommandErrorNotLoggedIn
	// CommandErrorNotConnected means the session exists but is offline / not ready.
	CommandErrorNotConnected
	// CommandErrorExpired means an action window (edit/revoke/retry) has passed.
	CommandErrorExpired
	// CommandErrorRejected is a precondition or permission failure the caller
	// cannot fix by retrying with the same inputs.
	CommandErrorRejected
	// CommandErrorAlreadyExists is a duplicate of something that already exists.
	CommandErrorAlreadyExists
)

// CommandError is a transport-neutral command failure carrying a classification
// (Kind) and a human-readable message. It is the single error contract between
// the daemon core and every frontend boundary.
type CommandError struct {
	Kind    CommandErrorKind
	Message string
}

func (e *CommandError) Error() string { return e.Message }

// NewCommandError builds a CommandError with a formatted message.
func NewCommandError(kind CommandErrorKind, format string, args ...any) *CommandError {
	return &CommandError{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// AsCommandError reports the CommandError in err's chain, if any.
func AsCommandError(err error) (*CommandError, bool) {
	var ce *CommandError
	if errors.As(err, &ce) {
		return ce, true
	}
	return nil, false
}
