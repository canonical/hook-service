package hooks

import (
	"context"
	"errors"
	reflect "reflect"
	"testing"

	trace "go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_hooks.go -source=./interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_tracing.go -source=../../internal/tracing/interfaces.go

func TestServiceFetchUserGroups(t *testing.T) {
	err := errors.New("some error")
	u := User{SubjectId: "123", Email: "a@a.com"}
	tests := []struct {
		name  string
		input User

		mockedClients func(*gomock.Controller) []ClientInterface

		expectedResult []string
		expectedError  error
	}{
		{
			name:  "Single service",
			input: u,
			mockedClients: func(ctrl *gomock.Controller) []ClientInterface {
				mockClient1 := NewMockClientInterface(ctrl)
				mockClient1.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]string{"g1", "g2"}, nil)
				return []ClientInterface{mockClient1}
			},
			expectedResult: []string{"g1", "g2"},
		},
		{
			name:  "Single service repeated",
			input: u,
			mockedClients: func(ctrl *gomock.Controller) []ClientInterface {
				mockClient1 := NewMockClientInterface(ctrl)
				mockClient1.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]string{"g1", "g2", "g1"}, nil)
				return []ClientInterface{mockClient1}
			},
			expectedResult: []string{"g1", "g2"},
		},
		{
			name:  "Multiple services",
			input: u,
			mockedClients: func(ctrl *gomock.Controller) []ClientInterface {
				mockClient1 := NewMockClientInterface(ctrl)
				mockClient1.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]string{"g1", "g2"}, nil)
				mockClient2 := NewMockClientInterface(ctrl)
				mockClient2.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]string{"g3", "g1"}, nil)
				return []ClientInterface{mockClient1, mockClient2}
			},
			expectedResult: []string{"g1", "g2", "g3"},
		},
		{
			name:  "Multiple services with empty result",
			input: u,
			mockedClients: func(ctrl *gomock.Controller) []ClientInterface {
				mockClient1 := NewMockClientInterface(ctrl)
				mockClient1.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]string{"g1", "g2"}, nil)
				mockClient2 := NewMockClientInterface(ctrl)
				mockClient2.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]string{""}, nil)
				mockClient3 := NewMockClientInterface(ctrl)
				mockClient3.EXPECT().FetchUserGroups(gomock.Any(), u).Return(nil, nil)
				return []ClientInterface{mockClient1, mockClient2, mockClient3}
			},
			expectedResult: []string{"g1", "g2"},
		},
		{
			name:  "Multiple services with error",
			input: u,
			mockedClients: func(ctrl *gomock.Controller) []ClientInterface {
				mockClient1 := NewMockClientInterface(ctrl)
				mockClient1.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]string{"g1", "g2"}, nil)
				mockClient2 := NewMockClientInterface(ctrl)
				mockClient2.EXPECT().FetchUserGroups(gomock.Any(), u).Return(nil, err)
				mockClient3 := NewMockClientInterface(ctrl)
				return []ClientInterface{mockClient1, mockClient2, mockClient3}
			},
			expectedError: err,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockLogger := NewMockLoggerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			mockTracer.EXPECT().Start(gomock.Any(), "hooks.Service.FetchUserGroups").Times(1).Return(context.TODO(), trace.SpanFromContext(context.TODO()))

			s := NewService(test.mockedClients(ctrl), mockTracer, mockMonitor, mockLogger)

			groups, err := s.FetchUserGroups(context.TODO(), test.input)

			if err != test.expectedError {
				t.Fatalf("expected error to be %v not %v", test.expectedError, err)
			}
			if !reflect.DeepEqual(groups, test.expectedResult) {
				t.Fatalf("expected return value to be %v not %v", test.expectedResult, groups)
			}
		})
	}

}
