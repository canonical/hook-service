// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package testhelpers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

// IntegrationClient is a test HTTP client for exercising the service's JSON
// API through an httptest.Server. Request failures are fatal to the calling
// test.
type IntegrationClient struct {
	t       *testing.T
	baseURL string
	client  *http.Client
}

// NewIntegrationClient builds a client targeting baseURL with a 10s request
// timeout.
func NewIntegrationClient(t *testing.T, baseURL string) *IntegrationClient {
	t.Helper()
	return &IntegrationClient{
		t:       t,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// Request issues an HTTP request with an optional JSON body and returns the
// status code and raw response body.
func (c *IntegrationClient) Request(method, path string, body interface{}) (int, []byte) {
	c.t.Helper()

	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("failed to marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		c.t.Fatalf("failed to create request: %v", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatalf("failed to execute request: %v", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatalf("failed to read response body: %v", err)
	}

	return resp.StatusCode, respBody
}

// CreateGroup creates a local test group via the groups API and returns its ID.
func (c *IntegrationClient) CreateGroup() string {
	c.t.Helper()

	name := fmt.Sprintf("test-group-%d", time.Now().UnixNano())
	body := map[string]interface{}{
		"name":        name,
		"description": "A test group",
		"type":        "local",
	}
	status, respBody := c.Request(http.MethodPost, "/groups", body)
	if status != http.StatusOK {
		c.t.Fatalf("expected status OK, got %d. Body: %s", status, string(respBody))
	}

	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		c.t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(resp.Data) == 0 {
		c.t.Fatal("expected created group data, got empty list")
	}
	return resp.Data[0].ID
}

// DeleteGroup deletes a group via the groups API.
func (c *IntegrationClient) DeleteGroup(groupID string) {
	c.t.Helper()

	status, _ := c.Request(http.MethodDelete, "/groups/"+groupID, nil)
	if status != http.StatusOK {
		c.t.Fatalf("failed to delete group %s, status: %d", groupID, status)
	}
}
