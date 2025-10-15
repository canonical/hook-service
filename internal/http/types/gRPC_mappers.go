// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package types

import (
	"context"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	v0Types "github.com/canonical/identity-platform-api/v0/http"
	rpcStatus "google.golang.org/genproto/googleapis/rpc/status"
)

// ForwardErrorResponseRewriter rewrites error message to comply with Admin UI
// standard json response for errors. It doesn't do anything on other messages
// usage example:
//
// mux := runtime.NewServeMux(
//
//	runtime.WithForwardResponseRewriter(ForwardErrorResponseRewriter),
//
// )
func ForwardErrorResponseRewriter(_ context.Context, response proto.Message) (any, error) {
	codeError, ok := response.(*rpcStatus.Status)
	if !ok {
		return response, nil
	}

	httpStatus := runtime.HTTPStatusFromCode(
		codes.Code(codeError.Code),
	)

	return &v0Types.ErrorResponse{
		Status:  int32(httpStatus),
		Message: codeError.GetMessage(),
	}, nil
}
