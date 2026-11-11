package core

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

func statusOrder(status any) int {
	switch strings.ToUpper(fmt.Sprint(status)) {
	case "IN_PROGRESS":
		return 0
	case "PENDING":
		return 1
	case "DONE":
		return 2
	default:
		return 3
	}
}

// RunCLI handles common subcommands for sprint tasks CLI across all backends.
func RunCLI(backend Backend, appTitle string, args []string) {
	if len(args) == 0 {
		PrintUsage(appTitle)
		return
	}

	cmd := strings.ToLower(args[0])
	switch cmd {
	case "init":
		if err := backend.Init(); err != nil {
			fmt.Fprintf(os.Stderr, "Init error: %v\n", err)
			os.Exit(1)
		}
		if err := EnsureMilestoneColumn(backend); err != nil {
			fmt.Fprintf(os.Stderr, "Schema migration error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Table 'sprint_tasks' successfully initialized in %s.\n", backend.Name())

	case "list":
		whereClauses := make([]string, 0, 2)
		for i := 1; i < len(args); i++ {
			arg := strings.TrimSpace(args[i])
			lower := strings.ToLower(strings.TrimLeft(arg, "-"))
			switch lower {
			case "active":
				whereClauses = append(whereClauses, "UPPER(status) != 'DONE'")
			case "pending":
				whereClauses = append(whereClauses, "UPPER(status) = 'PENDING'")
			case "in_progress":
				whereClauses = append(whereClauses, "UPPER(status) = 'IN_PROGRESS'")
			case "done":
				whereClauses = append(whereClauses, "UPPER(status) = 'DONE'")
			default:
				whereClauses = append(whereClauses, fmt.Sprintf("LOWER(COALESCE(milestone, '')) = '%s'", EscapeSQL(strings.ToLower(arg))))
			}
		}
		whereSQL := ""
		if len(whereClauses) > 0 {
			whereSQL = " WHERE " + strings.Join(whereClauses, " AND ")
		}
		query := "SELECT id, status, COALESCE(milestone, '') AS milestone, domain, priority, title, plan_doc, metrics FROM sprint_tasks" + whereSQL + " ORDER BY priority ASC, id ASC;"
		res, err := backend.Exec(query)
		if err != nil {
			if migErr := EnsureMilestoneColumn(backend); migErr == nil {
				res, err = backend.Exec(query)
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "List error: %v\n", err)
			os.Exit(1)
		}
		if len(res.Rows) == 0 {
			fmt.Println("No tasks found in sprint_tasks.")
			return
		}
		sort.SliceStable(res.Rows, func(i, j int) bool {
			return statusOrder(res.Rows[i]["status"]) < statusOrder(res.Rows[j]["status"])
		})
		fmt.Println("=========================================================================================================")
		fmt.Printf(" %-12s | %-10s | %-22s | %-10s | %-4s | %s\n", "STATUS", "MILESTONE", "ID", "DOMAIN", "PRIO", "TITLE")
		fmt.Println("---------------------------------------------------------------------------------------------------------")
		for _, r := range res.Rows {
			ms := fmt.Sprint(r["milestone"])
			if ms == "<nil>" || ms == "" {
				ms = "-"
			}
			fmt.Printf(" %-12v | %-10s | %-22v | %-10v | %-4v | %v\n", r["status"], ms, r["id"], r["domain"], r["priority"], r["title"])
		}
		fmt.Println("=========================================================================================================")

	case "show":
		if len(args) < 2 {
			fmt.Println("Usage: sprint show <id>")
			return
		}
		id := args[1]
		query := fmt.Sprintf("SELECT * FROM sprint_tasks WHERE id = '%s';", EscapeSQL(id))
		res, err := backend.Exec(query)
		if err != nil || len(res.Rows) == 0 {
			fmt.Fprintf(os.Stderr, "Task not found: %s\n", id)
			return
		}
		row := res.Rows[0]
		fmt.Printf("# [%s] %v\n\n", row["id"], row["title"])
		fmt.Printf("- Status: %v\n", row["status"])
		if ms, ok := row["milestone"].(string); ok && ms != "" {
			fmt.Printf("- Milestone: %s\n", ms)
		}
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
			fmt.Println("Usage: sprint add <id> <title> [domain] [priority] [plan_doc] [milestone]")
			return
		}
		id := args[1]
		title := args[2]
		domain := "Engine"
		prio := 2
		planDoc := ""
		milestone := ""
		if len(args) >= 4 {
			domain = args[3]
		}
		if len(args) >= 5 {
			fmt.Sscanf(args[4], "%d", &prio)
		}
		if len(args) >= 6 {
			planDoc = args[5]
		}
		if len(args) >= 7 {
			milestone = args[6]
		}

		sql := fmt.Sprintf("INSERT INTO sprint_tasks (id, domain, title, priority, status, milestone, plan_doc) VALUES ('%s', '%s', '%s', %d, 'PENDING', '%s', '%s');",
			EscapeSQL(id), EscapeSQL(domain), EscapeSQL(title), prio, EscapeSQL(milestone), EscapeSQL(planDoc))
		if _, err := backend.Exec(sql); err != nil {
			if migErr := EnsureMilestoneColumn(backend); migErr == nil {
				_, err = backend.Exec(sql)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "Add error: %v\n", err)
				os.Exit(1)
			}
		}
		fmt.Printf("Added task: [%s] %s (%s, Prio %d, Milestone %q) [%s]\n", id, title, domain, prio, milestone, backend.Name())
		_ = BackupToFile(backend, "TASKS.md")

	case "set":
		if len(args) < 3 {
			fmt.Println("Usage: sprint set <id> <status>  (e.g. sprint set P-2 DONE)")
			return
		}
		id := args[1]
		status := strings.ToUpper(args[2])
		chk, err := backend.Exec(fmt.Sprintf("SELECT id FROM sprint_tasks WHERE id = '%s';", EscapeSQL(id)))
		if err != nil || len(chk.Rows) == 0 {
			fmt.Fprintf(os.Stderr, "Set error: task %q not found\n", id)
			os.Exit(1)
		}
		sql := fmt.Sprintf("UPDATE sprint_tasks SET status = '%s', updated_at = CURRENT_TIMESTAMP WHERE id = '%s';", EscapeSQL(status), EscapeSQL(id))
		if _, err := backend.Exec(sql); err != nil {
			fmt.Fprintf(os.Stderr, "Set error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Updated task [%s] -> %s [%s]\n", id, status, backend.Name())
		_ = BackupToFile(backend, "TASKS.md")

	case "milestone", "set-milestone":
		if len(args) < 3 {
			fmt.Println("Usage: sprint milestone <id> <milestone>  (e.g. sprint milestone TASK-1 1.0-GA)")
			return
		}
		id := args[1]
		milestone := args[2]
		_ = EnsureMilestoneColumn(backend)
		chk, err := backend.Exec(fmt.Sprintf("SELECT id FROM sprint_tasks WHERE id = '%s';", EscapeSQL(id)))
		if err != nil || len(chk.Rows) == 0 {
			fmt.Fprintf(os.Stderr, "Milestone error: task %q not found\n", id)
			os.Exit(1)
		}
		sql := fmt.Sprintf("UPDATE sprint_tasks SET milestone = '%s', updated_at = CURRENT_TIMESTAMP WHERE id = '%s';", EscapeSQL(milestone), EscapeSQL(id))
		if _, err := backend.Exec(sql); err != nil {
			fmt.Fprintf(os.Stderr, "Milestone error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Updated task [%s] milestone -> %s [%s]\n", id, milestone, backend.Name())
		_ = BackupToFile(backend, "TASKS.md")

	case "delete", "rm":
		if len(args) < 2 {
			fmt.Println("Usage: sprint delete <id>")
			return
		}
		id := args[1]
		chk, err := backend.Exec(fmt.Sprintf("SELECT id FROM sprint_tasks WHERE id = '%s';", EscapeSQL(id)))
		if err != nil || len(chk.Rows) == 0 {
			fmt.Fprintf(os.Stderr, "Delete error: task %q not found\n", id)
			os.Exit(1)
		}
		sql := fmt.Sprintf("DELETE FROM sprint_tasks WHERE id = '%s';", EscapeSQL(id))
		if _, err := backend.Exec(sql); err != nil {
			fmt.Fprintf(os.Stderr, "Delete error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Deleted task [%s] from %s and synced TASKS.md\n", id, backend.Name())
		_ = BackupToFile(backend, "TASKS.md")

	case "dump", "backup":
		filename := "TASKS.md"
		if len(args) >= 2 {
			filename = args[1]
		}
		if err := BackupToFile(backend, filename); err != nil {
			fmt.Fprintf(os.Stderr, "Backup error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Successfully backed up sprint tasks to %s via %s (Deterministic ID Sort)\n", filename, backend.Name())

	case "restore":
		filename := "TASKS.md"
		if len(args) >= 2 {
			filename = args[1]
		}
		count, err := RestoreFromFile(backend, filename)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Restore error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Successfully restored %d tasks from %s into %s sprint_tasks table\n", count, filename, backend.Name())

	default:
		PrintUsage(appTitle)
	}
}

// PrintUsage displays standard help text.
func PrintUsage(appTitle string) {
	fmt.Printf("%s\n", appTitle)
	fmt.Println("Usage:")
	fmt.Println("  sprint init                                    : Create/migrate sprint_tasks table")
	fmt.Println("  sprint list [active|pending|done] [milestone]  : View tasks (use 'active' to exclude DONE)")
	fmt.Println("  sprint show <id>                               : Inspect task details, phases, and metrics")
	fmt.Println("  sprint add <id> <title> [dom] [prio] [doc] [ms]: Register a new task")
	fmt.Println("  sprint set <id> <status>                       : Update task status (PENDING, IN_PROGRESS, DONE)")
	fmt.Println("  sprint milestone <id> <milestone>              : Update task milestone (e.g. 1.0-GA, Post-1.0)")
	fmt.Println("  sprint delete <id>                             : Delete task explicitly from DB & sync TASKS.md")
	fmt.Println("  sprint backup [filename]                       : Dump current sprint_tasks table to Markdown file (ID ASC)")
	fmt.Println("  sprint restore [filename]                      : Restore sprint_tasks table 1:1 from Markdown file")
}
