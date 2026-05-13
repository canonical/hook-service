package authentication

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

func TestGrpcInterceptor_StreamAuthenticate(t *testing.T) {
	tests := []struct {
		name       string
		setupMD    func() metadata.MD
		setupMocks func(*gomock.Controller) TokenVerifierInterface
		wantCode   codes.Code
	}{
		{
			name:    "missing metadata returns UNAUTHENTICATED",
			setupMD: func() metadata.MD { return nil },
			setupMocks: func(ctrl *gomock.Controller) TokenVerifierInterface {
				return NewMockTokenVerifierInterface(ctrl)
			},
			wantCode: codes.Unauthenticated,
		},
		{
			name: "missing authorization header returns UNAUTHENTICATED",
			setupMD: func() metadata.MD {
				return metadata.Pairs("something", "else")
			},
			setupMocks: func(ctrl *gomock.Controller) TokenVerifierInterface {
				return NewMockTokenVerifierInterface(ctrl)
			},
			wantCode: codes.Unauthenticated,
		},
		{
			name: "invalid authorization format returns UNAUTHENTICATED",
			setupMD: func() metadata.MD {
				return metadata.Pairs("authorization", "InvalidFormat")
			},
			setupMocks: func(ctrl *gomock.Controller) TokenVerifierInterface {
				return NewMockTokenVerifierInterface(ctrl)
			},
			wantCode: codes.Unauthenticated,
		},
		{
			name: "token verification failure returns UNAUTHENTICATED",
			setupMD: func() metadata.MD {
				return metadata.Pairs("authorization", "Bearer bad-token")
			},
			setupMocks: func(ctrl *gomock.Controller) TokenVerifierInterface {
				mockVerifier := NewMockTokenVerifierInterface(ctrl)
				mockVerifier.EXPECT().VerifyToken(gomock.Any(), "bad-token").Return(false, fmt.Errorf("invalid token"))
				return mockVerifier
			},
			wantCode: codes.Unauthenticated,
		},
		{
			name: "unauthorized token returns UNAUTHENTICATED",
			setupMD: func() metadata.MD {
				return metadata.Pairs("authorization", "Bearer valid-but-unauthorized")
			},
			setupMocks: func(ctrl *gomock.Controller) TokenVerifierInterface {
				mockVerifier := NewMockTokenVerifierInterface(ctrl)
				mockVerifier.EXPECT().VerifyToken(gomock.Any(), "valid-but-unauthorized").Return(false, nil)
				return mockVerifier
			},
			wantCode: codes.Unauthenticated,
		},
		{
			name: "valid token passes through",
			setupMD: func() metadata.MD {
				return metadata.Pairs("authorization", "Bearer valid-token")
			},
			setupMocks: func(ctrl *gomock.Controller) TokenVerifierInterface {
				mockVerifier := NewMockTokenVerifierInterface(ctrl)
				mockVerifier.EXPECT().VerifyToken(gomock.Any(), "valid-token").Return(true, nil)
				return mockVerifier
			},
			wantCode: codes.OK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockTracer := NewMockTracingInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockVerifier := tt.setupMocks(ctrl)

			ctx := context.Background()
			if tt.setupMD() != nil {
				ctx = metadata.NewIncomingContext(ctx, tt.setupMD())
			}
			mockTracer.EXPECT().Start(gomock.Any(), "authentication.GrpcInterceptor.StreamAuthenticate").Return(ctx, trace.SpanFromContext(ctx))
			mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

			interceptor := NewGrpcInterceptor(mockVerifier, mockTracer, mockMonitor, mockLogger)

			called := false
			handler := func(srv interface{}, ss grpc.ServerStream) error {
				called = true
				return nil
			}

			stream := &testServerStream{ctx: ctx}
			err := interceptor.StreamAuthenticate()(nil, stream, nil, handler)

			if tt.wantCode == codes.OK {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if !called {
					t.Error("expected handler to be called")
				}
				return
			}

			s, ok := status.FromError(err)
			if !ok {
				t.Fatalf("error is not a gRPC status: %v", err)
			}
			if s.Code() != tt.wantCode {
				t.Errorf("expected code %v, got %v", tt.wantCode, s.Code())
			}
		})
	}
}

func TestGrpcInterceptor_StreamAuthenticate_Integration(t *testing.T) {
	t.Run("unauthenticated stream is rejected", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockTracer := NewMockTracingInterface(ctrl)
		mockMonitor := NewMockMonitorInterface(ctrl)
		mockLogger := NewMockLoggerInterface(ctrl)
		mockVerifier := NewMockTokenVerifierInterface(ctrl)

		noMD := metadata.Pairs()
		noMDctx := metadata.NewIncomingContext(context.Background(), noMD)
		mockTracer.EXPECT().Start(gomock.Any(), "authentication.GrpcInterceptor.StreamAuthenticate").Return(noMDctx, trace.SpanFromContext(noMDctx))
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

		interceptor := NewGrpcInterceptor(mockVerifier, mockTracer, mockMonitor, mockLogger)
		stream := &testServerStream{ctx: noMDctx}

		err := interceptor.StreamAuthenticate()(nil, stream, nil, func(srv interface{}, ss grpc.ServerStream) error { return nil })
		s, ok := status.FromError(err)
		if !ok || s.Code() != codes.Unauthenticated {
			t.Errorf("expected UNAUTHENTICATED, got %v", err)
		}
	})

	t.Run("authenticated stream succeeds", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockTracer := NewMockTracingInterface(ctrl)
		mockMonitor := NewMockMonitorInterface(ctrl)
		mockLogger := NewMockLoggerInterface(ctrl)
		mockVerifier := NewMockTokenVerifierInterface(ctrl)

		md := metadata.Pairs("authorization", "Bearer good-token")
		mdctx := metadata.NewIncomingContext(context.Background(), md)
		mockTracer.EXPECT().Start(gomock.Any(), "authentication.GrpcInterceptor.StreamAuthenticate").Return(mdctx, trace.SpanFromContext(mdctx))
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
		mockVerifier.EXPECT().VerifyToken(gomock.Any(), "good-token").Return(true, nil)

		interceptor := NewGrpcInterceptor(mockVerifier, mockTracer, mockMonitor, mockLogger)
		stream := &testServerStream{ctx: mdctx}

		called := false
		err := interceptor.StreamAuthenticate()(nil, stream, nil, func(srv interface{}, ss grpc.ServerStream) error {
			called = true
			return nil
		})
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if !called {
			t.Error("expected handler to be called")
		}
	})
}

type testServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *testServerStream) Context() context.Context {
	return s.ctx
}
