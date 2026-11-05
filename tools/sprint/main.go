package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"lionsql/tools/sprint/core"
)

const defaultEndpoint = "http://127.0.0.1:8090/api/sql"

type LionSQLBackend struct {
	endpoint string
}

func NewLionSQLBackend(endpoint string) *LionSQLBackend {
	return &LionSQLBackend{endpoint: endpoint}
}

func (b *LionSQLBackend) Name() string {
	return "LionSQL"
}

func (b *LionSQLBackend) Init() error {
	_, err := b.Exec(core.SchemaDDL)
	return err
}

func (b *LionSQLBackend) ensureServerRunning() {
	if !strings.Contains(b.endpoint, "127.0.0.1:8090") && !strings.Contains(b.endpoint, "localhost:8090") {
		return
	}

	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:8090/health")
	if err == nil {
		resp.Body.Close()
		return
	}

	scriptPath := "scripts/sprint_server.sh"
	if _, err := os.Stat(scriptPath); err != nil {
		if exe, err := os.Executable(); err == nil {
			cand := filepath.Join(filepath.Dir(exe), "..", "scripts", "sprint_server.sh")
			if _, err := os.Stat(cand); err == nil {
				scriptPath = cand
			}
		}
	}

	cmd := exec.Command(scriptPath, "start")
	_ = cmd.Run()
	time.Sleep(1 * time.Second)
}

func (b *LionSQLBackend) getAuthToken() (string, error) {
	loginURL := strings.TrimSuffix(b.endpoint, "/api/sql") + "/api/auth/login"
	loginBody := `{"email":"admin@lionsql.local","password":"1234567890"}`

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post(loginURL, "application/json", strings.NewReader(loginBody))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login failed with status: %d", resp.StatusCode)
	}

	var res struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	return res.Token, nil
}

func (b *LionSQLBackend) Exec(sql string) (*core.QueryResp, error) {
	b.ensureServerRunning()

	type QueryReq struct {
		Query string `json:"query"`
	}
	payload, err := json.Marshal(QueryReq{Query: sql})
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodPost, b.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	if token, err := b.getAuthToken(); err == nil && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LionSQL API connection failed (%s): %w", b.endpoint, err)
	}
	defer resp.Body.Close()

	var qResp core.QueryResp
	if err := json.NewDecoder(resp.Body).Decode(&qResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	if !qResp.Success && qResp.Error != "" {
		return nil, fmt.Errorf("SQL error: %s", qResp.Error)
	}
	return &qResp, nil
}

func (b *LionSQLBackend) Close() error {
	return nil
}

func main() {
	endpoint := flag.String("addr", defaultEndpoint, "LionSQL HTTP API endpoint")
	flag.Parse()

	backend := NewLionSQLBackend(*endpoint)
	defer backend.Close()

	core.RunCLI(backend, "LionSQL Sprint Task Tracker (tools/sprint)", flag.Args())
}
