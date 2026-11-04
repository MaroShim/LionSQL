package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultEndpoint = "http://127.0.0.1:8090/api/sql"

type QueryReq struct {
	Query string `json:"query"`
}

type QueryResp struct {
	Success bool             `json:"success"`
	Columns []string         `json:"columns"`
	Rows    []map[string]any `json:"rows"`
	Error   string           `json:"error,omitempty"`
}

func getAuthToken(baseAPI string) (string, error) {
	loginURL := strings.TrimSuffix(baseAPI, "/api/sql") + "/api/auth/login"
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

func execSQL(endpoint, sql string) (*QueryResp, error) {
	b, err := json.Marshal(QueryReq{Query: sql})
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	// Try auto-login to get Bearer token
	if token, err := getAuthToken(endpoint); err == nil && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LionSQL API connection failed (%s): %w", endpoint, err)
	}
	defer resp.Body.Close()

	var qResp QueryResp
	if err := json.NewDecoder(resp.Body).Decode(&qResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	if !qResp.Success && qResp.Error != "" {
		return nil, fmt.Errorf("SQL error: %s", qResp.Error)
	}
	return &qResp, nil
}

func main() {
	endpoint := flag.String("addr", defaultEndpoint, "LionSQL HTTP API endpoint")
	flag.Parse()
	args := flag.Args()

	if len(args) == 0 {
		printUsage()
		return
	}

	cmd := strings.ToLower(args[0])
	switch cmd {
	case "init":
		schema := `
CREATE TABLE IF NOT EXISTS sprint_tasks (
    id VARCHAR(64) PRIMARY KEY,
    domain VARCHAR(50) DEFAULT 'Engine',
    title VARCHAR(255),
    priority INT DEFAULT 2,
    status VARCHAR(20) DEFAULT 'PENDING',
    plan_doc VARCHAR(255),
    metrics TEXT,
    target_files TEXT,
    phases TEXT,
    description TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);`
		if _, err := execSQL(*endpoint, schema); err != nil {
			fmt.Fprintf(os.Stderr, "Init error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Table 'sprint_tasks' successfully initialized in LionSQL.")

	case "list":
		query := "SELECT id, status, domain, priority, title, plan_doc, metrics FROM sprint_tasks ORDER BY status DESC, priority ASC, id ASC;"
		res, err := execSQL(*endpoint, query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "List error: %v\n", err)
			os.Exit(1)
		}
		if len(res.Rows) == 0 {
			fmt.Println("No tasks found in sprint_tasks.")
			return
		}
		fmt.Println("==========================================================================================")
		fmt.Printf(" %-12s | %-16s | %-10s | %-4s | %s\n", "STATUS", "ID", "DOMAIN", "PRIO", "TITLE")
		fmt.Println("------------------------------------------------------------------------------------------")
		for _, r := range res.Rows {
			fmt.Printf(" %-12v | %-16v | %-10v | %-4v | %v\n", r["status"], r["id"], r["domain"], r["priority"], r["title"])
		}
		fmt.Println("==========================================================================================")

	case "show":
		if len(args) < 2 {
			fmt.Println("Usage: sprint show <id>")
			return
		}
		id := args[1]
		query := fmt.Sprintf("SELECT * FROM sprint_tasks WHERE id = '%s';", id)
		res, err := execSQL(*endpoint, query)
		if err != nil || len(res.Rows) == 0 {
			fmt.Fprintf(os.Stderr, "Task not found: %s\n", id)
			return
		}
		row := res.Rows[0]
		fmt.Printf("# [%s] %v\n\n", row["id"], row["title"])
		fmt.Printf("- Status: %v\n", row["status"])
		fmt.Printf("- Domain: %v\n", row["domain"])
		fmt.Printf("- Priority: P%v\n", row["priority"])
		if doc, ok := row["plan_doc"].(string); ok && doc != "" {
			fmt.Printf("- Plan Doc: %s\n", doc)
		}
		if files, ok := row["target_files"].(string); ok && files != "" {
			fmt.Printf("- Target Files: %s\n", files)
		}
		if m, ok := row["metrics"].(string); ok && m != "" {
			fmt.Printf("- Metrics: %s\n", m)
		}
		if ph, ok := row["phases"].(string); ok && ph != "" {
			fmt.Printf("\n## Phases & Subtasks\n%s\n", ph)
		}
		if desc, ok := row["description"].(string); ok && desc != "" {
			fmt.Printf("\n## Description & Resolution\n%s\n", desc)
		}

	case "add":
		if len(args) < 3 {
			fmt.Println("Usage: sprint add <id> <title> [domain] [priority] [plan_doc]")
			return
		}
		id := args[1]
		title := args[2]
		domain := "Engine"
		prio := 2
		planDoc := ""
		if len(args) >= 4 {
			domain = args[3]
		}
		if len(args) >= 5 {
			fmt.Sscanf(args[4], "%d", &prio)
		}
		if len(args) >= 6 {
			planDoc = args[5]
		}

		sql := fmt.Sprintf("INSERT INTO sprint_tasks (id, domain, title, priority, status, plan_doc) VALUES ('%s', '%s', '%s', %d, 'PENDING', '%s');",
			id, domain, title, prio, planDoc)
		if _, err := execSQL(*endpoint, sql); err != nil {
			fmt.Fprintf(os.Stderr, "Add error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Added task: [%s] %s (%s, Prio %d)\n", id, title, domain, prio)
		_ = backupToFile(*endpoint, "SPRINT_TASKS_BACKUP.md")

	case "set":
		if len(args) < 3 {
			fmt.Println("Usage: sprint set <id> <status>  (e.g. sprint set P-2 DONE)")
			return
		}
		id := args[1]
		status := strings.ToUpper(args[2])
		sql := fmt.Sprintf("UPDATE sprint_tasks SET status = '%s', updated_at = CURRENT_TIMESTAMP WHERE id = '%s';", status, id)
		if _, err := execSQL(*endpoint, sql); err != nil {
			fmt.Fprintf(os.Stderr, "Set error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Updated task [%s] -> %s\n", id, status)
		_ = backupToFile(*endpoint, "SPRINT_TASKS_BACKUP.md")

	case "dump", "backup":
		filename := "SPRINT_TASKS_BACKUP.md"
		if len(args) >= 2 {
			filename = args[1]
		}
		if err := backupToFile(*endpoint, filename); err != nil {
			fmt.Fprintf(os.Stderr, "Backup error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Successfully backed up sprint tasks to %s\n", filename)

	default:
		printUsage()
	}
}

func backupToFile(endpoint, filename string) error {
	query := "SELECT id, status, domain, priority, title, plan_doc, metrics, target_files, phases, description, created_at, updated_at FROM sprint_tasks ORDER BY status DESC, priority ASC, id ASC;"
	res, err := execSQL(endpoint, query)
	if err != nil {
		return err
	}

	var sb strings.Builder
	sb.WriteString("# SPRINT TASKS BACKUP (LionSQL SSOT Mirror)\n\n")
	sb.WriteString(fmt.Sprintf("> - **Last Synced**: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	sb.WriteString("> - **Primary Source of Truth (SSOT)**: LionSQL `sprint_tasks` Table\n")
	sb.WriteString("> - **Backup File**: Auto-synced on task update & Git commit\n\n")
	sb.WriteString("## 1. Quick Status Overview\n\n")
	sb.WriteString("| Status | ID | Domain | Priority | Title | Plan Doc | Metrics |\n")
	sb.WriteString("| :---: | :---: | :---: | :---: | :--- | :--- | :--- |\n")

	for _, r := range res.Rows {
		planDoc := ""
		if pd, ok := r["plan_doc"].(string); ok && pd != "" {
			planDoc = fmt.Sprintf("[`%s`](%s)", pd, pd)
		}
		metrics := ""
		if m, ok := r["metrics"].(string); ok && m != "" {
			metrics = m
			if len(metrics) > 40 {
				metrics = metrics[:37] + "..."
			}
		}
		sb.WriteString(fmt.Sprintf("| `%v` | **`%v`** | %v | P%v | %v | %v | %v |\n",
			r["status"], r["id"], r["domain"], r["priority"], r["title"], planDoc, metrics))
	}
	sb.WriteString("\n---\n\n")

	sb.WriteString("## 2. Detailed Task Specifications & Execution Records\n\n")
	for _, r := range res.Rows {
		sb.WriteString(fmt.Sprintf("### [%v] %s `%s`\n", r["status"], r["title"], r["id"]))
		sb.WriteString(fmt.Sprintf("- **Domain**: %v | **Priority**: P%v | **Updated**: %v\n", r["domain"], r["priority"], r["updated_at"]))
		if pd, ok := r["plan_doc"].(string); ok && pd != "" {
			sb.WriteString(fmt.Sprintf("- **Plan Doc**: [`%s`](%s)\n", pd, pd))
		}
		if tf, ok := r["target_files"].(string); ok && tf != "" {
			sb.WriteString(fmt.Sprintf("- **Target Files**: `%s`\n", tf))
		}
		if m, ok := r["metrics"].(string); ok && m != "" {
			sb.WriteString(fmt.Sprintf("- **Metrics**: %s\n", m))
		}
		if ph, ok := r["phases"].(string); ok && ph != "" {
			sb.WriteString("\n**Phases & Checklist**:\n")
			sb.WriteString(ph)
			sb.WriteString("\n")
		}
		if desc, ok := r["description"].(string); ok && desc != "" {
			sb.WriteString("\n**Details & Resolution**:\n")
			sb.WriteString(desc)
			sb.WriteString("\n")
		}
		sb.WriteString("\n---\n\n")
	}

	return os.WriteFile(filename, []byte(sb.String()), 0644)
}

func printUsage() {
	fmt.Println("LionSQL Dogfooding Sprint Tracker (tools/sprint)")
	fmt.Println("Usage:")
	fmt.Println("  sprint init                          : Create sprint_tasks table in LionSQL")
	fmt.Println("  sprint list                          : View all sprint tasks")
	fmt.Println("  sprint show <id>                     : Inspect task details, phases, and metrics")
	fmt.Println("  sprint add <id> <title> [dom] [prio] : Register a new task")
	fmt.Println("  sprint set <id> <status>             : Update task status (PENDING, IN_PROGRESS, DONE)")
	fmt.Println("  sprint backup [filename]             : Dump current sprint_tasks table to Markdown file")
}
