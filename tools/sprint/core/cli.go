package core

import (
	"fmt"
	"os"
	"strings"
)

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
		fmt.Printf("Table 'sprint_tasks' successfully initialized in %s.\n", backend.Name())

	case "list":
		query := "SELECT id, status, domain, priority, title, plan_doc, metrics FROM sprint_tasks ORDER BY status DESC, priority ASC, id ASC;"
		res, err := backend.Exec(query)
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
		query := fmt.Sprintf("SELECT * FROM sprint_tasks WHERE id = '%s';", EscapeSQL(id))
		res, err := backend.Exec(query)
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
			EscapeSQL(id), EscapeSQL(domain), EscapeSQL(title), prio, EscapeSQL(planDoc))
		if _, err := backend.Exec(sql); err != nil {
			fmt.Fprintf(os.Stderr, "Add error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Added task: [%s] %s (%s, Prio %d) [%s]\n", id, title, domain, prio, backend.Name())
		_ = BackupToFile(backend, "TASKS.md")

	case "set":
		if len(args) < 3 {
			fmt.Println("Usage: sprint set <id> <status>  (e.g. sprint set P-2 DONE)")
			return
		}
		id := args[1]
		status := strings.ToUpper(args[2])
		sql := fmt.Sprintf("UPDATE sprint_tasks SET status = '%s', updated_at = CURRENT_TIMESTAMP WHERE id = '%s';", EscapeSQL(status), EscapeSQL(id))
		if _, err := backend.Exec(sql); err != nil {
			fmt.Fprintf(os.Stderr, "Set error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Updated task [%s] -> %s [%s]\n", id, status, backend.Name())
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
	fmt.Println("  sprint init                          : Create sprint_tasks table")
	fmt.Println("  sprint list                          : View all sprint tasks (Grouped by status)")
	fmt.Println("  sprint show <id>                     : Inspect task details, phases, and metrics")
	fmt.Println("  sprint add <id> <title> [dom] [prio] : Register a new task")
	fmt.Println("  sprint set <id> <status>             : Update task status (PENDING, IN_PROGRESS, DONE)")
	fmt.Println("  sprint backup [filename]             : Dump current sprint_tasks table to Markdown file (ID ASC)")
	fmt.Println("  sprint restore [filename]            : Restore sprint_tasks table 1:1 from Markdown file")
}
