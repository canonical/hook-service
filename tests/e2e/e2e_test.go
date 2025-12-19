package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

type E2EClient struct {
	t       *testing.T
	baseURL string
	client  *http.Client
}

func NewE2EClient(t *testing.T) *E2EClient {
	baseURL := os.Getenv("BASE_URL")
	if baseURL == "" {
		if testEnv != nil {
			baseURL = testEnv.BaseURL
		} else {
			baseURL = defaultBaseURL
		}
	}

	return &E2EClient{
		t:       t,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *E2EClient) Request(method, path string, body interface{}) (int, []byte) {
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

	req.Header.Set("Content-Type", "application/json")
	if token := os.Getenv("API_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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

func (c *E2EClient) CreateGroup() string {
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
	err := json.Unmarshal(respBody, &resp)
	if err != nil {
		c.t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(resp.Data) == 0 {
		c.t.Fatal("expected created group data, got empty list")
	}
	return resp.Data[0].ID
}

func (c *E2EClient) DeleteGroup(groupID string) {
	status, _ := c.Request(http.MethodDelete, "/groups/"+groupID, nil)
	if status != http.StatusOK {
		c.t.Fatalf("failed to delete group %s, status: %d", groupID, status)
	}
}

func TestGroupLifecycle(t *testing.T) {
	client := NewE2EClient(t)
	groupID := client.CreateGroup()
	defer client.DeleteGroup(groupID)

	t.Run("Get Group", func(t *testing.T) {
		status, body := client.Request(http.MethodGet, "/groups/"+groupID, nil)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		err := json.Unmarshal(body, &resp)
		if err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if len(resp.Data) == 0 || resp.Data[0].ID != groupID {
			t.Errorf("expected group ID %s, got %v", groupID, resp.Data)
		}
	})

	t.Run("Update Group", func(t *testing.T) {
		updateBody := map[string]interface{}{
			"description": "Updated description",
			"type":        "local",
		}
		status, body := client.Request(http.MethodPut, "/groups/"+groupID, updateBody)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}

		var resp struct {
			Data []struct {
				ID          string `json:"id"`
				Description string `json:"description"`
			} `json:"data"`
		}
		err := json.Unmarshal(body, &resp)
		if err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if len(resp.Data) == 0 || resp.Data[0].Description != "Updated description" {
			t.Errorf("expected updated description, got %v", resp.Data)
		}
	})

	t.Run("List Groups", func(t *testing.T) {
		status, body := client.Request(http.MethodGet, "/groups", nil)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		err := json.Unmarshal(body, &resp)
		if err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		found := false
		for _, g := range resp.Data {
			if g.ID == groupID {
				found = true
				break
			}
		}
		if !found {
			t.Error("created group not found in list")
		}
	})
}

func TestUserMembership(t *testing.T) {
	client := NewE2EClient(t)
	groupID := client.CreateGroup()
	defer client.DeleteGroup(groupID)

	userID := fmt.Sprintf("test-user-%d@example.com", time.Now().UnixNano())

	t.Run("Add User", func(t *testing.T) {
		body := []string{userID}
		status, _ := client.Request(http.MethodPost, "/groups/"+groupID+"/users", body)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}
	})

	t.Run("List Users in Group", func(t *testing.T) {
		status, body := client.Request(http.MethodGet, "/groups/"+groupID+"/users", nil)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		err := json.Unmarshal(body, &resp)
		if err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		found := false
		for _, u := range resp.Data {
			if u.ID == userID {
				found = true
				break
			}
		}
		if !found {
			t.Error("added user not found in group")
		}
	})

	t.Run("List Groups for User", func(t *testing.T) {
		status, body := client.Request(http.MethodGet, "/users/"+userID+"/groups", nil)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		err := json.Unmarshal(body, &resp)
		if err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		found := false
		for _, g := range resp.Data {
			if g.ID == groupID {
				found = true
				break
			}
		}
		if !found {
			t.Error("group not found in user's groups")
		}
	})

	t.Run("Remove User", func(t *testing.T) {
		status, _ := client.Request(http.MethodDelete, "/groups/"+groupID+"/users/"+userID, nil)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}

		status, body := client.Request(http.MethodGet, "/groups/"+groupID+"/users", nil)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		err := json.Unmarshal(body, &resp)
		if err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		for _, u := range resp.Data {
			if u.ID == userID {
				t.Error("user still found in group after removal")
			}
		}
	})
}

func TestAppAuthorization(t *testing.T) {
	client := NewE2EClient(t)
	groupID := client.CreateGroup()
	defer client.DeleteGroup(groupID)

	appID := uuid.New().String()

	t.Run("Add App", func(t *testing.T) {
		body := map[string]string{"client_id": appID}
		status, _ := client.Request(http.MethodPost, "/groups/"+groupID+"/apps", body)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}
	})

	t.Run("Get Allowed Apps", func(t *testing.T) {
		status, body := client.Request(http.MethodGet, "/groups/"+groupID+"/apps", nil)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}
		var resp struct {
			Data []struct {
				ClientID string `json:"client_id"`
			} `json:"data"`
		}
		err := json.Unmarshal(body, &resp)
		if err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		found := false
		for _, a := range resp.Data {
			if a.ClientID == appID {
				found = true
				break
			}
		}
		if !found {
			t.Error("added app not found in group")
		}
	})

	t.Run("Get Allowed Groups for App", func(t *testing.T) {
		status, body := client.Request(http.MethodGet, "/apps/"+appID+"/groups", nil)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}
		var resp struct {
			Data []struct {
				ID string `json:"group_id"`
			} `json:"data"`
		}
		err := json.Unmarshal(body, &resp)
		if err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		found := false
		for _, g := range resp.Data {
			if g.ID == groupID {
				found = true
				break
			}
		}
		if !found {
			t.Error("group not found in app's groups")
		}
	})

	t.Run("Remove App", func(t *testing.T) {
		status, _ := client.Request(http.MethodDelete, "/groups/"+groupID+"/apps/"+appID, nil)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}

		status, body := client.Request(http.MethodGet, "/groups/"+groupID+"/apps", nil)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}
		var resp struct {
			Data []struct {
				ClientID string `json:"client_id"`
			} `json:"data"`
		}
		err := json.Unmarshal(body, &resp)
		if err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		for _, a := range resp.Data {
			if a.ClientID == appID {
				t.Error("app still found in group after removal")
			}
		}
	})
}
