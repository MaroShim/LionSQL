package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
)

type FullTask struct {
	ID          string
	Domain      string
	Title       string
	Priority    int
	Status      string
	PlanDoc     string
	Metrics     string
	TargetFiles string
	Phases      string
	Description string
}

func main() {
	loginURL := "http://127.0.0.1:8090/api/auth/login"
	loginBody := `{"email":"admin@lionsql.local","password":"1234567890"}`
	resp, err := http.Post(loginURL, "application/json", strings.NewReader(loginBody))
	if err != nil {
		fmt.Printf("Login err: %v\n", err)
		return
	}
	defer resp.Body.Close()
	var authRes struct {
		Token string `json:"token"`
	}
	json.NewDecoder(resp.Body).Decode(&authRes)

	token := authRes.Token
	execSQL := func(query string) error {
		reqBody, _ := json.Marshal(map[string]string{"query": query})
		req, _ := http.NewRequest("POST", "http://127.0.0.1:8090/api/sql", bytes.NewReader(reqBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		var qResp struct {
			Success bool   `json:"success"`
			Error   string `json:"error"`
		}
		json.NewDecoder(res.Body).Decode(&qResp)
		if !qResp.Success && qResp.Error != "" {
			return fmt.Errorf("SQL error: %s", qResp.Error)
		}
		return nil
	}

	// 1. Truncate table
	if err := execSQL("TRUNCATE TABLE sprint_tasks;"); err != nil {
		fmt.Printf("Truncate err: %v\n", err)
		return
	}
	fmt.Println("Cleaned sprint_tasks via TRUNCATE.")

	// 2. Parse TODO.md
	todoTasks := parseTODO("TODO.md")
	fmt.Printf("Parsed %d tasks from TODO.md\n", len(todoTasks))

	// 3. Parse BUG_ARCHIVE.ko.md
	bugTasks := parseBugArchive("BUG_ARCHIVE.ko.md")
	fmt.Printf("Parsed %d tasks from BUG_ARCHIVE.ko.md\n", len(bugTasks))

	// Deduplicate by ID
	taskMap := make(map[string]FullTask)
	for _, t := range bugTasks {
		taskMap[t.ID] = t
	}
	for _, t := range todoTasks {
		taskMap[t.ID] = t
	}

	fmt.Printf("Total unique tasks to insert: %d\n", len(taskMap))

	// Insert into DB
	inserted := 0
	for _, t := range taskMap {
		escape := func(s string) string {
			return strings.ReplaceAll(s, "'", "''")
		}

		insertSQL := fmt.Sprintf(`INSERT INTO sprint_tasks (id, domain, title, priority, status, plan_doc, metrics, target_files, phases, description) 
VALUES ('%s', '%s', '%s', %d, '%s', '%s', '%s', '%s', '%s', '%s');`,
			escape(t.ID), escape(t.Domain), escape(t.Title), t.Priority, escape(t.Status),
			escape(t.PlanDoc), escape(t.Metrics), escape(t.TargetFiles), escape(t.Phases), escape(t.Description))

		if err := execSQL(insertSQL); err != nil {
			fmt.Printf("Insert error for %s: %v\n", t.ID, err)
		} else {
			inserted++
		}
	}
	fmt.Printf("Total successfully inserted into sprint_tasks: %d\n", inserted)
}

func parseTODO(filename string) []FullTask {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	var tasks []FullTask

	var currentTask *FullTask
	var currentPhases []string
	var currentDesc []string
	currentPriority := 2
	currentDomain := "Ecosystem"

	rePrio := regexp.MustCompile(`^## Priority (\d+)`)
	rePlan := regexp.MustCompile(`notes/([A-Za-z0-9_]+_PLAN(?:\.ko)?\.md)`)

	flushTask := func() {
		if currentTask != nil && currentTask.ID != "" {
			currentTask.Phases = strings.Join(currentPhases, "\n")
			currentTask.Description = strings.Join(currentDesc, "\n")
			tasks = append(tasks, *currentTask)
			currentTask = nil
			currentPhases = nil
			currentDesc = nil
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check Priority section
		if m := rePrio.FindStringSubmatch(trimmed); len(m) > 1 {
			flushTask()
			switch m[1] {
			case "1":
				currentPriority = 1
				currentDomain = "Performance"
			case "2":
				currentPriority = 2
				currentDomain = "Durability"
			default:
				currentPriority = 3
				currentDomain = "Ecosystem"
			}
			continue
		}

		// Check Task header: starts with ### [
		if strings.HasPrefix(trimmed, "### [") {
			flushTask()

			status := "PENDING"
			if strings.HasPrefix(trimmed, "### [x]") {
				status = "DONE"
			} else if strings.HasPrefix(trimmed, "### [-]") {
				status = "IN_PROGRESS"
			}

			// Parse header line: ### [x] 1.0.1 `P-2` Title
			// Or: ### [x] M1. Title
			// Or: ### [-] 1.8 1.0 GA 검증 및 잔여 정리
			headerContent := trimmed[7:] // after "### [ ] "
			headerContent = strings.TrimSpace(headerContent)

			var id, title string
			if idx := strings.Index(headerContent, "`"); idx != -1 {
				endIdx := strings.Index(headerContent[idx+1:], "`")
				if endIdx != -1 {
					id = headerContent[idx+1 : idx+1+endIdx]
					title = strings.TrimSpace(headerContent[idx+1+endIdx+1:])
					title = strings.TrimPrefix(title, ":")
					title = strings.TrimSpace(title)
				}
			}

			if id == "" {
				// No backtick in header, e.g., 1.8 1.0 GA 검증 및 잔여 정리, or M1. v0.90.0 Feature Freeze
				parts := strings.SplitN(headerContent, " ", 2)
				if len(parts) == 2 {
					id = strings.Trim(parts[0], ".:")
					title = strings.TrimSpace(parts[1])
				} else {
					id = "TODO-" + fmt.Sprintf("%d", len(tasks)+1)
					title = headerContent
				}
			}

			// Clean up title
			title = strings.TrimSuffix(title, " — **[완료 (Pass)]**")
			title = strings.TrimSuffix(title, " — **[완료]**")
			title = strings.TrimSuffix(title, " — **[완료 (Scope Locked)]**")
			title = strings.TrimSuffix(title, " — **[백로그 (2단계)]**")
			title = strings.TrimSuffix(title, " (완료)")
			title = strings.TrimSuffix(title, " (진행 중)")
			title = strings.TrimSpace(title)

			// Clean id
			id = strings.Trim(id, " `*:")

			currentTask = &FullTask{
				ID:       id,
				Domain:   currentDomain,
				Title:    title,
				Priority: currentPriority,
				Status:   status,
			}
			continue
		}

		if currentTask != nil {
			if strings.HasPrefix(trimmed, "- [") || strings.HasPrefix(trimmed, "* [") {
				currentPhases = append(currentPhases, trimmed)
				// Target files
				if strings.Contains(trimmed, "pkg/") || strings.Contains(trimmed, "cmd/") {
					reFile := regexp.MustCompile(`(?:pkg|cmd)/[a-zA-Z0-9_\./\-]+`)
					matches := reFile.FindAllString(trimmed, -1)
					for _, f := range matches {
						f = strings.TrimRight(f, ".,;:)")
						if !strings.Contains(currentTask.TargetFiles, f) {
							if currentTask.TargetFiles != "" {
								currentTask.TargetFiles += ", "
							}
							currentTask.TargetFiles += f
						}
					}
				}
				// Metrics
				if strings.Contains(trimmed, "tpmC") || strings.Contains(trimmed, "TPS") || strings.Contains(trimmed, "ns/op") || strings.Contains(trimmed, "µs/op") || strings.Contains(trimmed, "ms/op") {
					if currentTask.Metrics == "" {
						currentTask.Metrics = trimmed
					} else {
						currentTask.Metrics += " | " + trimmed
					}
				}
			} else if strings.HasPrefix(trimmed, "- 설계 계획서:") || strings.HasPrefix(trimmed, "> 설계 및 실측 문서:") {
				if pm := rePlan.FindStringSubmatch(trimmed); len(pm) > 0 {
					currentTask.PlanDoc = "notes/" + pm[1]
				}
				currentDesc = append(currentDesc, trimmed)
			} else if trimmed != "" && !strings.HasPrefix(trimmed, "---") {
				currentDesc = append(currentDesc, trimmed)
				if pm := rePlan.FindStringSubmatch(trimmed); len(pm) > 0 && currentTask.PlanDoc == "" {
					currentTask.PlanDoc = "notes/" + pm[1]
				}
			}
		}
	}
	flushTask()

	return tasks
}

func parseBugArchive(filename string) []FullTask {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	tableMap := make(map[string]*FullTask)

	reRow := regexp.MustCompile(`^\|\s*\*\*` + "`" + `(BUG-[A-Z0-9\-]+)` + "`" + `\*\*\s*\|\s*\*\*([^*]+)\*\*\s*\|\s*` + "`" + `([^` + "`" + `]+)` + "`" + `\s*\|\s*([^|]+)\|`)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if m := reRow.FindStringSubmatch(trimmed); len(m) > 4 {
			id := m[1]
			title := strings.TrimSpace(m[2])
			desc := strings.TrimSpace(m[4])
			tableMap[id] = &FullTask{
				ID:          id,
				Domain:      "Integrity",
				Title:       title,
				Priority:    2,
				Status:      "DONE",
				Description: desc,
			}
		}
	}

	detailRe := regexp.MustCompile(`^\s*\d+\.\s+\*\*` + "`" + `(BUG-[A-Z0-9\-]+)` + "`" + `\s*(?:\(([^)]+)\))?\*\*:`)
	var curBug *FullTask
	var curDetail []string

	flushDetail := func() {
		if curBug != nil && len(curDetail) > 0 {
			curBug.Description += "\n\n" + strings.Join(curDetail, "\n")
			curBug = nil
			curDetail = nil
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if m := detailRe.FindStringSubmatch(trimmed); len(m) > 1 {
			flushDetail()
			id := m[1]
			if b, exists := tableMap[id]; exists {
				curBug = b
				if len(m) > 2 && m[2] != "" {
					curBug.TargetFiles = m[2]
				}
			}
			continue
		}

		if curBug != nil {
			if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
				curDetail = append(curDetail, trimmed)
			} else if trimmed == "" || strings.HasPrefix(trimmed, "###") || strings.HasPrefix(trimmed, "---") {
				flushDetail()
			}
		}
	}
	flushDetail()

	var tasks []FullTask
	for _, b := range tableMap {
		tasks = append(tasks, *b)
	}
	return tasks
}
