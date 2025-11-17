// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package salesforce

import (
	"fmt"

	"github.com/k-capehart/go-salesforce/v2"
)

func NewSalesforceClient(domain, consumerKey, consumerSecret string) (*salesforce.Salesforce, error) {
	return salesforce.Init(salesforce.Creds{
		Domain:         domain,
		ConsumerKey:    consumerKey,
		ConsumerSecret: consumerSecret,
	})
}

func NewClient(
	domain, consumerKey, consumerSecret string,
) *salesforce.Salesforce {
	salesforceClient, err := NewSalesforceClient(domain, consumerKey, consumerSecret)
	if err != nil {
		panic(fmt.Errorf("failed to initialize salesforce client: %v", err))
	}

	return salesforceClient
}
