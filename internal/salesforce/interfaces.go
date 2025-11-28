// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package salesforce

type SalesforceInterface interface {
	Query(string, any) error
}
