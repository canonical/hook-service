package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	tc "github.com/testcontainers/testcontainers-go/modules/compose"
)

const (
	defaultBaseURL = "http://localhost:8000/api/v0/authz"
	fgaAPIToken    = "42"
)

var (
	testEnv *TestEnvironment
)

type TestEnvironment struct {
	Compose    tc.ComposeStack
	Cmd        *exec.Cmd
	BaseURL    string
	CancelFunc context.CancelFunc
	BinPath    string
}

func TestMain(m *testing.M) {
	var err error
	// Check if we should use existing deployment
	if os.Getenv("E2E_USE_EXISTING_DEPLOYMENT") == "true" {
		fmt.Println("Using existing deployment...")
		os.Exit(m.Run())
	}

	fmt.Println("Starting test environment...")
	testEnv, err = setupTestEnvironment()
	if err != nil {
		fmt.Printf("Failed to setup test environment: %v\n", err)
		if testEnv != nil {
			testEnv.Teardown()
		}
		os.Exit(1)
	}

	code := m.Run()

	testEnv.Teardown()

	os.Exit(code)
}

func setupTestEnvironment() (*TestEnvironment, error) {
	var (
		compose *tc.DockerCompose
		binPath string
	)

	ctx, cancel := context.WithCancel(context.Background())

	cleanup := func() {
		if compose != nil {
			compose.Down(context.Background(), tc.RemoveOrphans(true), tc.RemoveImagesLocal)
		}
		if binPath != "" {
			os.Remove(binPath)
		}
		cancel()
	}

	// Locate docker-compose file
	rootDir, err := findRootDir()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to find root dir: %w", err)
	}
	composeFile := filepath.Join(rootDir, "docker-compose.dev.yml")

	// Build App
	binPath, err = buildApp(rootDir)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to build app: %w", err)
	}

	// Start Docker Compose
	compose, err = tc.NewDockerCompose(composeFile)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to create docker compose: %w", err)
	}

	// Start services
	err = compose.Up(ctx, tc.Wait(false))
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to start docker compose: %w", err)
	}

	// Wait for OpenFGA
	openfgaURL := "http://localhost:8080"
	if err := waitForHTTP(ctx, openfgaURL+"/healthz"); err != nil {
		cleanup()
		return nil, fmt.Errorf("openfga not ready: %w", err)
	}

	// Run Migrations (retries until Postgres is ready)
	dsn := "postgres://groups:groups@localhost:5432/groups?sslmode=disable"
	if err := runMigrations(ctx, binPath, dsn); err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	// Setup OpenFGA
	storeID, modelID, err := setupOpenFGA(ctx, binPath, openfgaURL)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to setup openfga: %w", err)
	}

	// Start Hook Service
	envVars := map[string]string{
		"DSN":                            dsn,
		"OPENFGA_API_SCHEME":             "http",
		"OPENFGA_API_HOST":               "localhost:8080",
		"OPENFGA_STORE_ID":               storeID,
		"OPENFGA_AUTHORIZATION_MODEL_ID": modelID,
		"OPENFGA_API_TOKEN":              fgaAPIToken,
		"AUTHORIZATION_ENABLED":          "true",
		"SALESFORCE_ENABLED":             "false", // Disable SF for now as we don't have a mock/container for it in compose
		"PORT":                           "8001",  // Use a different port than default 8000 to avoid conflict if running locally? Or just 8000.
		"LOG_LEVEL":                      "debug",
		"TRACING_ENABLED":                "false",
		"API_TOKEN":                      "test-token",
	}

	cmd, err := startServer(ctx, binPath, envVars)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to start server: %w", err)
	}

	// Wait for Server
	baseURL := "http://localhost:8001/api/v0/authz"
	if err := waitForHTTP(ctx, "http://localhost:8001/api/v0/status"); err != nil {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		cleanup()
		return nil, fmt.Errorf("server not ready: %w", err)
	}

	return &TestEnvironment{
		Compose:    compose,
		Cmd:        cmd,
		BaseURL:    baseURL,
		CancelFunc: cancel,
		BinPath:    binPath,
	}, nil
}

func (e *TestEnvironment) Teardown() {
	if e.Cmd != nil && e.Cmd.Process != nil {
		e.Cmd.Process.Signal(os.Interrupt)
		e.Cmd.Wait()
	}
	if e.BinPath != "" {
		os.Remove(e.BinPath)
	}
	if e.Compose != nil {
		e.Compose.Down(context.Background(), tc.RemoveOrphans(true), tc.RemoveImagesLocal)
	}
	if e.CancelFunc != nil {
		e.CancelFunc()
	}
}

func findRootDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "docker-compose.dev.yml")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("root dir not found")
		}
		dir = parent
	}
}

func waitForHTTP(ctx context.Context, url string) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	timeout := time.After(30 * time.Second)
	client := &http.Client{Timeout: 1 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timeout waiting for %s", url)
		case <-ticker.C:
			resp, err := client.Get(url)
			if err == nil && resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				return nil
			}
		}
	}
}

func runMigrations(ctx context.Context, binPath, dsn string) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	timeout := time.After(60 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timeout waiting for migrations")
		case <-ticker.C:
			cmd := exec.CommandContext(ctx, binPath, "migrate", "up", "--dsn", dsn)
			_, err := cmd.CombinedOutput()
			if err == nil {
				return nil
			}
		}
	}
}

func setupOpenFGA(ctx context.Context, binPath, apiURL string) (string, string, error) {
	cmd := exec.CommandContext(ctx, binPath, "create-fga-model",
		"--fga-api-url", apiURL,
		"--fga-api-token", fgaAPIToken,
		"--format", "json",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("failed to create fga model: %v, output: %s", err, string(output))
	}

	var result struct {
		StoreID string `json:"store_id"`
		ModelID string `json:"model_id"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return "", "", fmt.Errorf("failed to parse fga model output: %v, output: %s", err, string(output))
	}

	return result.StoreID, result.ModelID, nil
}

func buildApp(rootDir string) (string, error) {
	binPath := filepath.Join(os.TempDir(), fmt.Sprintf("hook-service-e2e-%d", time.Now().UnixNano()))
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = rootDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return binPath, nil
}

func startServer(ctx context.Context, binPath string, envVars map[string]string) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, binPath, "serve")
	cmd.Env = os.Environ()
	for k, v := range envVars {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}
