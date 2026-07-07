package rpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"whatevrd/internal/app"
)

// commandErrorInterceptor is the single place the frozen gRPC boundary maps the
// daemon's transport-neutral app.CommandError back into a gRPC status. The
// daemon core (app/wa) no longer speaks gRPC — it raises app.CommandError — so
// this interceptor keeps the legacy status codes intact for the gRPC clients
// without any status/codes vocabulary leaking back into wa. It retires with the
// rest of the gRPC stack at the end of the migration.
func commandErrorInterceptor(
	ctx context.Context,
	req any,
	_ *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	resp, err := handler(ctx, req)
	if err == nil {
		return resp, nil
	}
	if ce, ok := app.AsCommandError(err); ok {
		return resp, status.Error(commandErrorCode(ce.Kind), ce.Message)
	}
	return resp, err
}

func commandErrorCode(kind app.CommandErrorKind) codes.Code {
	switch kind {
	case app.CommandErrorInvalidArgument:
		return codes.InvalidArgument
	case app.CommandErrorNotFound:
		return codes.NotFound
	case app.CommandErrorNotLoggedIn, app.CommandErrorExpired, app.CommandErrorRejected:
		return codes.FailedPrecondition
	case app.CommandErrorNotConnected:
		return codes.Unavailable
	case app.CommandErrorAlreadyExists:
		return codes.AlreadyExists
	default:
		return codes.Internal
	}
}
