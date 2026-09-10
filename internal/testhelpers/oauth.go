// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package testhelpers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	hydra "github.com/ory/hydra-client-go/v2"
)

// CreateHydraClient registers an OAuth2 client_credentials client in Hydra
// and returns its credentials. No explicit cleanup is needed: clients live
// in the container's in-memory DSN and disappear with container teardown.
// Any failure is fatal to the calling test.
func CreateHydraClient(t *testing.T, env *HydraEnv, name string) (clientID, clientSecret string) {
	t.Helper()

	configuration := hydra.NewConfiguration()
	configuration.Servers = []hydra.ServerConfiguration{{URL: env.AdminURL}}
	apiClient := hydra.NewAPIClient(configuration)

	client := hydra.NewOAuth2Client()
	client.SetClientName(name)
	client.SetGrantTypes([]string{"client_credentials"})

	createdClient, _, err := apiClient.OAuth2API.CreateOAuth2Client(context.Background()).OAuth2Client(*client).Execute()
	if err != nil {
		t.Fatalf("failed to create hydra client: %v", err)
	}

	if createdClient.ClientId == nil || createdClient.ClientSecret == nil {
		t.Fatalf("hydra client creation succeeded but returned no credentials")
	}

	return *createdClient.ClientId, *createdClient.ClientSecret
}

// GetAccessToken performs the client_credentials flow against Hydra's public
// endpoint and returns the access token. Any failure is fatal to the calling
// test.
func GetAccessToken(t *testing.T, env *HydraEnv, clientID, clientSecret string) string {
	t.Helper()

	data := url.Values{}
	data.Set("grant_type", "client_credentials")

	tokenURL := fmt.Sprintf("%s/oauth2/token", env.PublicURL)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		t.Fatalf("failed to create token request: %v", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, clientSecret)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("failed to execute token request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("token request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to parse token response: %v", err)
	}

	return result.AccessToken
}
