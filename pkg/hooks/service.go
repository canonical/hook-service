package hooks

import (
	"cmp"
	"context"
	"slices"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

type Service struct {
	clients []ClientInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (s *Service) FetchUserGroups(ctx context.Context, user User) ([]string, error) {
	ctx, span := s.tracer.Start(ctx, "hooks.Service.FetchUserGroups")
	defer span.End()

	ret := make([]string, 0)

	for _, c := range s.clients {
		// TODO: Generate go routines to run this in parallel
		groups, err := c.FetchUserGroups(ctx, user)
		if err != nil {
			return nil, err
		}
		ret = append(ret, groups...)
	}

	ret = removeDuplicates(ret)
	if len(ret) > 0 && ret[0] == "" {
		ret = ret[1:]
	}
	return ret, nil
}

func NewService(
	clients []ClientInterface,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) *Service {
	s := new(Service)

	s.clients = clients

	s.monitor = monitor
	s.tracer = tracer
	s.logger = logger

	return s
}

func removeDuplicates[S ~[]E, E cmp.Ordered](s S) S {
	slices.Sort(s)
	return slices.Compact(s)
}
