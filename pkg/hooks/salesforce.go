// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package hooks

import (
	"context"
	"fmt"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/salesforce"
	"github.com/canonical/hook-service/internal/tracing"
)

var ErrInvalidTotalSize = fmt.Errorf("invalid total size")

const query = "SELECT Department2__c, fHCM2__Team__c FROM fHCM2__Team_Member__c WHERE fHCM2__Email__c = '%s'"

type Record struct {
	Department string `mapstructure:"Department2__c"`
	Team       string `mapstructure:"fHCM2__Team__c"`
}

type Salesforce struct {
	c salesforce.SalesforceInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (s *Salesforce) FetchUserGroups(ctx context.Context, user User) ([]string, error) {
	_, span := s.tracer.Start(ctx, "hooks.Salesforce.FetchUserGroups")
	defer span.End()

	if user.Email == "" {
		s.logger.Infof("User `%v` has no email, skipping salesforce call", user)
		return nil, nil
	}

	q := fmt.Sprintf(query, user.Email)
	rs := []Record{}
	err := s.c.Query(q, &rs)

	if err != nil {
		s.logger.Errorf("Failed to query salesforce: %v", err)
		return nil, err
	}

	if len(rs) == 0 {
		return nil, nil
	}
	if len(rs) > 1 {
		s.logger.Errorf("Salesforce returned '%v' records for user `%v`, cannot parse result", len(rs), user)
		return nil, ErrInvalidTotalSize
	}
	r := rs[0]

	return []string{r.Department, r.Team}, nil
}

func NewSalesforceClient(c salesforce.SalesforceInterface, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *Salesforce {
	r := new(Salesforce)

	r.c = c

	r.logger = logger
	r.tracer = tracer
	r.monitor = monitor

	return r
}
