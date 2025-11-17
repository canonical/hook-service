// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package salesforce

type NoopClient struct {
}

func (c *NoopClient) Query(q string, r any) error {
	return nil
}

func NewNoopClient() *NoopClient {
	return new(NoopClient)
}
