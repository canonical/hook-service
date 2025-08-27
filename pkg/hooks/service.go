package hooks

import (
	"cmp"
	"context"
	"slices"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/ory/hydra/v2/oauth2"
)

type Service struct {
	clients []ClientInterface
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

	var err error
	if !isServiceAccount(req.Request.GrantTypes) {
		return s.authz.CanAccess(ctx, user.GetUserId(), req.Request.ClientID, groups)
	} else {
		// TODO: Implement BatchCanAccess
		for _, aud := range req.Request.GrantedAudience {
			allowed, err := s.authz.CanAccess(ctx, user.GetUserId(), aud, groups)
			if err != nil || allowed == false {
				return false, err
			}
		}
	}
	return true, err
}

func NewService(
	clients []ClientInterface,
	authz AuthorizerInterface,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) *Service {
	s := new(Service)

	s.clients = clients
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
