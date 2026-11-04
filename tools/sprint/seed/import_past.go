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
	reTableBug = regexp.MustCompile(`^\|\s*\*\*` + "`" + `(BUG-[^` + "`" + `]+)` + "`" + `\*\*\s*\|\s*\*\*([^*]+)\*\*\s*\|\s*` + "`" + `([^` + "`" + `]+)` + "`" + `\s*\|\s*(.*?)\s*\|$`)
)

func main() {
	f, err := os.Open("BUG_ARCHIVE.ko.md")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot open BUG_ARCHIVE.ko.md: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	count := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if m := reTableBug.FindStringSubmatch(line); len(m) > 4 {
			id := m[1]
			domain := strings.TrimSpace(m[2])
			title := strings.TrimSpace(m[4])

			// clean strings
			title = strings.ReplaceAll(title, "'", "")
			title = strings.ReplaceAll(title, "`", "")
			domain = strings.ReplaceAll(domain, "'", "")
			domain = strings.ReplaceAll(domain, "`", "")

			cmdAdd := exec.Command("./bin/sprint", "add", id, title, domain, "2")
			_ = cmdAdd.Run()
			cmdSet := exec.Command("./bin/sprint", "set", id, "DONE")
			_ = cmdSet.Run()
			count++
		}
	}

	fmt.Printf("Imported %d past bug-fix tasks from BUG_ARCHIVE.ko.md\n", count)

	cmdBackup := exec.Command("./bin/sprint", "backup")
	_ = cmdBackup.Run()
	fmt.Println("SPRINT_TASKS_BACKUP.md updated.")
}
