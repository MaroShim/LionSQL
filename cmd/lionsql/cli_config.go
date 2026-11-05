package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"
)

func runGet(args []string) error {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	addr := fs.String("addr", "http://127.0.0.1:8090", "LionSQL HTTP service address")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}

	targetURL := strings.TrimRight(*addr, "/") + "/api/config"
	extraArgs := fs.Args()
	if len(extraArgs) > 0 {
		key := strings.TrimSpace(extraArgs[0])
		targetURL += "?key=" + url.QueryEscape(key)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(targetURL)
	if err != nil {
		return fmt.Errorf("failed to connect to LionSQL at %s: %w", *addr, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return fmt.Errorf("%s", errResp.Error)
		}
		return fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(body))
	}

	if len(extraArgs) > 0 {
		var item struct {
			Key         string `json:"key"`
			Value       any    `json:"value"`
			Type        string `json:"type"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(body, &item); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
		fmt.Printf("%s = %v\n", item.Key, item.Value)
		return nil
	}

	var listResp struct {
		Configs []struct {
			Key         string `json:"key"`
			Value       any    `json:"value"`
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"configs"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "KEY\tVALUE\tTYPE\tDESCRIPTION")
	for _, c := range listResp.Configs {
		fmt.Fprintf(w, "%s\t%v\t%s\t%s\n", c.Key, c.Value, c.Type, c.Description)
	}
	_ = w.Flush()
	return nil
}

func runSet(args []string) error {
	fs := flag.NewFlagSet("set", flag.ContinueOnError)
	addr := fs.String("addr", "http://127.0.0.1:8090", "LionSQL HTTP service address")
	adminEmail := fs.String("admin-email", "admin@lionsql.local", "Superuser administrator email")
	passFlag := fs.String("pass", "", "Administrator password (optional, prompted interactively if omitted)")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}

	posArgs := fs.Args()
	if len(posArgs) < 2 {
		return fmt.Errorf("usage: lionsql set <key> <value> [--addr=http://127.0.0.1:8090]")
	}
	key := posArgs[0]
	val := posArgs[1]

	// 1. Password resolution: Flag > LIONSQL_ADMIN_PASS env > Interactive terminal prompt
	password := *passFlag
	if password == "" {
		password = os.Getenv("LIONSQL_ADMIN_PASS")
	}
	if password == "" {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return fmt.Errorf("terminal prompt not available; provide password via LIONSQL_ADMIN_PASS or --pass flag")
		}
		fmt.Fprintf(os.Stderr, "[sudo] Password for %s: ", *adminEmail)
		passBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return fmt.Errorf("failed to read password: %w", err)
		}
		password = string(passBytes)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	baseURL := strings.TrimRight(*addr, "/")

	// 2. Authenticate against /api/auth/login to obtain JWT bearer token
	loginPayload, _ := json.Marshal(map[string]string{
		"identity": *adminEmail,
		"password": password,
	})
	loginResp, err := client.Post(baseURL+"/api/auth/login", "application/json", bytes.NewReader(loginPayload))
	if err != nil {
		return fmt.Errorf("failed to authenticate against %s: %w", *addr, err)
	}
	defer loginResp.Body.Close()

	loginBody, _ := io.ReadAll(loginResp.Body)
	if loginResp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(loginBody, &errResp) == nil && errResp.Error != "" {
			return fmt.Errorf("authentication failed: %s", errResp.Error)
		}
		return fmt.Errorf("authentication failed (status %d)", loginResp.StatusCode)
	}

	var authResult struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(loginBody, &authResult); err != nil || authResult.Token == "" {
		return fmt.Errorf("failed to extract token from login response")
	}

	// 3. Post to /api/config with Bearer Token
	setPayload, _ := json.Marshal(map[string]any{
		"key":   key,
		"value": val,
	})
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/config", bytes.NewReader(setPayload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+authResult.Token)

	setResp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to set config on %s: %w", *addr, err)
	}
	defer setResp.Body.Close()

	setBody, _ := io.ReadAll(setResp.Body)
	if setResp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(setBody, &errResp) == nil && errResp.Error != "" {
			return fmt.Errorf("%s", errResp.Error)
		}
		return fmt.Errorf("server returned status %d: %s", setResp.StatusCode, string(setBody))
	}

	var resData struct {
		Success bool `json:"success"`
		Key     string `json:"key"`
		Value   any    `json:"value"`
	}
	_ = json.Unmarshal(setBody, &resData)
	fmt.Printf("OK: %s set to %v (in-memory volatile)\n", resData.Key, resData.Value)
	return nil
}
