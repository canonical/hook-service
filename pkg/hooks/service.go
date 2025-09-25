package hooks

import (
	"cmp"
	"context"
	"slices"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/pkg/groups"
	"github.com/ory/hydra/v2/oauth2"
)

type Service struct {
	clients []ClientInterface
	groups  groups.ServiceInterface
	authz   AuthorizerInterface

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

// This implements deny by default
// TODO: we should make this configurable
func (s *Service) AuthorizeRequest(
	ctx context.Context,
	user User,
	req oauth2.TokenHookRequest,
	groups []string,
) (bool, error) {
	ctx, span := s.tracer.Start(ctx, "hooks.Service.AuthorizeRequest")
	defer span.End()

	var gg = make([]string, 0)
	// Convert group names to group IDs
	for _, groupName := range groups {
		group, err := s.groups.GetGroupByName(ctx, groupName)
		if err != nil {
			s.logger.Infof("Failed to get group by name `%s`: %v", groupName, err)
			continue
		}
		gg = append(gg, group.ID)
	}

	if !isServiceAccount(req.Request.GrantTypes) {
		return s.authz.CanAccess(ctx, user.GetUserId(), req.Request.ClientID, gg)
	} else {
		return s.authz.BatchCanAccess(ctx, user.GetUserId(), req.Request.GrantedAudience, gg)
	}
}

func NewService(
	clients []ClientInterface,
	groups groups.ServiceInterface,
	authz AuthorizerInterface,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) *Service {
	s := new(Service)

	s.clients = clients
	s.groups = groups
	s.authz = authz

	s.monitor = monitor
	s.tracer = tracer
	s.logger = logger

	return s
}

func removeDuplicates[S ~[]E, E cmp.Ordered](s S) S {
	slices.Sort(s)
	return slices.Compact(s)
}
