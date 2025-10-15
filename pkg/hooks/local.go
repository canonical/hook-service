package hooks

import (
	"context"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/pkg/groups"
)

type LocalGroups struct {
	g groups.ServiceInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (l *LocalGroups) FetchUserGroups(ctx context.Context, user User) ([]string, error) {
	_, span := l.tracer.Start(ctx, "hooks.LocalGroups.FetchUserGroups")
	defer span.End()

	if user.GetUserId() == "" {
		l.logger.Infof("User `%v` has no ID, skipping local groups call", user)
		return nil, nil
	}

	groups, err := l.g.ListUserGroups(
		ctx,
		user.GetUserId(),
	)
	if err != nil {
		l.logger.Errorf("Failed to get user groups from local storage: %v", err)
		return nil, err
	}

	groupNames := make([]string, 0, len(groups))
	for _, g := range groups {
		groupNames = append(groupNames, g.Name)
	}

	return groupNames, nil
}

func NewLocalClient(g groups.ServiceInterface, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *LocalGroups {
	l := new(LocalGroups)

	l.g = g

	l.tracer = tracer
	l.monitor = monitor
	l.logger = logger

	return l
}
