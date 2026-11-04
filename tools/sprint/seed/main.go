package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var (
	reH3Task = regexp.MustCompile(`^###\s+\[([ x\-])\]\s+([\d\.]+)?\s*` + "`" + `([^` + "`" + `]+)` + "`" + `\s*(.*?)$`)
	reItemTask = regexp.MustCompile(`^-\s+\[([ x\-])\]\s+\*\*` + "`" + `([^` + "`" + `]+)` + "`" + `\*\*:\s*(.*?)$`)
	rePhaseTask = regexp.MustCompile(`^-\s+\[([ x\-])\]\s+\*\*Phase\s*\d*\s*\(` + "`" + `([^` + "`" + `]+)` + "`" + `\)\*\*:\s*(.*?)$`)
)

func main() {
	f, err := os.Open("TODO.md")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot open TODO.md: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	currentDomain := "Core"

	type task struct {
		id     string
		title  string
		domain string
		prio   int
		status string
	}
	var tasks []task

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "## Priority 1") || strings.HasPrefix(line, "## 1.") {
			currentDomain = "Performance"
		} else if strings.HasPrefix(line, "## Priority 2") || strings.HasPrefix(line, "## 2.") {
			currentDomain = "Durability"
		} else if strings.HasPrefix(line, "## Priority 3") || strings.HasPrefix(line, "## 3.") {
			currentDomain = "Ecosystem"
		} else if strings.HasPrefix(line, "### 도메인 A") {
			currentDomain = "Protocol"
		} else if strings.HasPrefix(line, "### 도메인 B") {
			currentDomain = "Storage"
		} else if strings.HasPrefix(line, "### 도메인 C") {
			currentDomain = "Engine"
		} else if strings.HasPrefix(line, "### 도메인 D") {
			currentDomain = "Catalog"
		}

		status := "PENDING"
		id := ""
		title := ""
		prio := 2

		if m := reH3Task.FindStringSubmatch(line); len(m) > 4 {
			mark := m[1]
			if mark == "x" {
				status = "DONE"
			} else if mark == "-" {
				status = "IN_PROGRESS"
			}
			id = m[3]
			title = strings.TrimSpace(m[4])
			prio = 1
		} else if m := reItemTask.FindStringSubmatch(line); len(m) > 3 {
			mark := m[1]
			if mark == "x" {
				status = "DONE"
			} else if mark == "-" {
				status = "IN_PROGRESS"
			}
			id = m[2]
			title = strings.TrimSpace(m[3])
			prio = 2
		}

		if id != "" && title != "" {
			// clean title
			title = strings.ReplaceAll(title, "'", "")
			title = strings.ReplaceAll(title, "`", "")
			if idx := strings.Index(title, "—"); idx != -1 {
				title = strings.TrimSpace(title[:idx])
			}
			tasks = append(tasks, task{
				id:     id,
				title:  title,
				domain: currentDomain,
				prio:   prio,
				status: status,
			})
		}
	}

	fmt.Printf("Parsed %d tasks from TODO.md\n", len(tasks))
	for _, t := range tasks {
		cmd := exec.Command("./bin/sprint", "add", t.id, t.title, t.domain, fmt.Sprintf("%d", t.prio))
		_ = cmd.Run()
		if t.status != "PENDING" {
			cmdSet := exec.Command("./bin/sprint", "set", t.id, t.status)
			_ = cmdSet.Run()
		}
	}
	fmt.Println("All tasks seeded into LionSQL sprint_tasks successfully.")
}
