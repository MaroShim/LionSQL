package core

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// BackupToFile dumps the sprint_tasks table to Markdown strictly ordered by id ASC (Diff-Stable Invariant).
func BackupToFile(backend Backend, filename string) error {
	query := "SELECT id, status, domain, priority, title, plan_doc, metrics, target_files, phases, description, created_at, updated_at FROM sprint_tasks ORDER BY id ASC;"
	res, err := backend.Exec(query)
	if err != nil {
		return err
	}

	var sb strings.Builder
	sb.WriteString("# TASKS (Sprint SSOT Register & Mirror)\n\n")
	sb.WriteString(fmt.Sprintf("> - **Last Synced**: %s\n", time.Now().Format("2006-01-02")))
	sb.WriteString(fmt.Sprintf("> - **Primary Source of Truth (SSOT)**: %s `sprint_tasks` Table\n", backend.Name()))
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
			runes := []rune(m)
			if len(runes) > 40 {
				metrics = string(runes[:37]) + "..."
			} else {
				metrics = m
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

// RestoreFromFile parses a TASKS.md file and reconstructs the sprint_tasks table 1:1.
func RestoreFromFile(backend Backend, filename string) (int, error) {
	file, err := os.Open(filename)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	// Ensure table exists before restoring
	_ = backend.Init()

	var tasks []ParsedTask
	taskMap := make(map[string]*ParsedTask)

	reHeader := regexp.MustCompile(`^###\s+\[([A-Z_]+)\]\s+(.*?)\s+` + "`" + `([A-Za-z0-9_\-\.]+)` + "`" + `$`)
	reMeta := regexp.MustCompile(`^-\s+\*\*Domain\*\*:\s+(.*?)\s+\|\s+\*\*Priority\*\*:\s+P(\d+)(?:\s+\|\s+\*\*Updated\*\*:\s+(.*))?$`)
	rePlanDoc := regexp.MustCompile(`^-\s+\*\*Plan Doc\*\*:\s+\[` + "`" + `([^` + "`" + `]+)` + "`" + `\]\(([^)]+)\)`)
	reTargetFiles := regexp.MustCompile(`^-\s+\*\*Target Files\*\*:\s+` + "`" + `(.*)` + "`" + `$`)
	reMetrics := regexp.MustCompile(`^-\s+\*\*Metrics\*\*:\s+(.*)$`)

	scanner := bufio.NewScanner(file)
	var cur *ParsedTask
	var curPhases, curDesc []string
	mode := "" // "meta", "phases", "desc"

	flush := func() {
		if cur != nil && cur.ID != "" {
			cur.Phases = strings.TrimSpace(strings.Join(curPhases, "\n"))
			cur.Description = strings.TrimSpace(strings.Join(curDesc, "\n"))
			if existing, ok := taskMap[cur.ID]; ok {
				*existing = *cur
			} else {
				tasks = append(tasks, *cur)
				taskMap[cur.ID] = &tasks[len(tasks)-1]
			}
			cur = nil
			curPhases = nil
			curDesc = nil
			mode = ""
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if m := reHeader.FindStringSubmatch(trimmed); len(m) > 3 {
			flush()
			cur = &ParsedTask{
				Status:   m[1],
				Title:    strings.TrimSpace(m[2]),
				ID:       m[3],
				Domain:   "Engine",
				Priority: 2,
			}
			mode = "meta"
			continue
		}

		if cur == nil {
			continue
		}

		if trimmed == "---" {
			flush()
			continue
		}

		if trimmed == "**Phases & Checklist**:" {
			mode = "phases"
			continue
		}
		if trimmed == "**Details & Resolution**:" {
			mode = "desc"
			continue
		}

		if mode == "meta" {
			if m := reMeta.FindStringSubmatch(trimmed); len(m) > 2 {
				cur.Domain = m[1]
				if p, err := strconv.Atoi(m[2]); err == nil {
					cur.Priority = p
				}
				if len(m) > 3 {
					cur.UpdatedAt = m[3]
				}
				continue
			}
			if m := rePlanDoc.FindStringSubmatch(trimmed); len(m) > 1 {
				cur.PlanDoc = m[1]
				continue
			}
			if m := reTargetFiles.FindStringSubmatch(trimmed); len(m) > 1 {
				cur.TargetFiles = m[1]
				continue
			}
			if m := reMetrics.FindStringSubmatch(trimmed); len(m) > 1 {
				cur.Metrics = m[1]
				continue
			}
		} else if mode == "phases" {
			curPhases = append(curPhases, line)
		} else if mode == "desc" {
			curDesc = append(curDesc, line)
		}
	}
	flush()

	if len(tasks) == 0 {
		return 0, fmt.Errorf("no tasks found in %s", filename)
	}

	// Insert or replace into sprint_tasks table
	for _, t := range tasks {
		upsertSQL := fmt.Sprintf(`DELETE FROM sprint_tasks WHERE id = '%s';
INSERT INTO sprint_tasks (id, domain, title, priority, status, plan_doc, metrics, target_files, phases, description)
VALUES ('%s', '%s', '%s', %d, '%s', '%s', '%s', '%s', '%s', '%s');`,
			EscapeSQL(t.ID),
			EscapeSQL(t.ID), EscapeSQL(t.Domain), EscapeSQL(t.Title), t.Priority, EscapeSQL(t.Status),
			EscapeSQL(t.PlanDoc), EscapeSQL(t.Metrics), EscapeSQL(t.TargetFiles), EscapeSQL(t.Phases), EscapeSQL(t.Description))

		if _, err := backend.Exec(upsertSQL); err != nil {
			return 0, fmt.Errorf("restore failed for task [%s]: %w", t.ID, err)
		}
	}

	return len(tasks), nil
}
