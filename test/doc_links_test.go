package test

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMarkdownDocLinks validates that all local relative markdown links
// across doc/, notes/, guide/, examples/, and root markdown files resolve
// to existing physical files on disk.
func TestMarkdownDocLinks(t *testing.T) {
	// Find project root
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("failed to resolve project root: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		if _, err := os.Stat("go.mod"); err == nil {
			root = "."
		} else {
			t.Fatalf("could not locate go.mod at root: %v", err)
		}
	}

	dirsToScan := []string{"doc", "notes", "guide", "examples"}
	var mdFiles []string

	rootEntries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("failed to read root directory: %v", err)
	}
	for _, entry := range rootEntries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			// Skip TASKS.md generated table which contains CLI flag artifacts
			if entry.Name() == "TASKS.md" {
				continue
			}
			mdFiles = append(mdFiles, filepath.Join(root, entry.Name()))
		}
	}

	for _, dir := range dirsToScan {
		dirPath := filepath.Join(root, dir)
		if _, err := os.Stat(dirPath); os.IsNotExist(err) {
			continue
		}
		_ = filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
				mdFiles = append(mdFiles, path)
			}
			return nil
		})
	}

	var brokenLinks []string

	for _, file := range mdFiles {
		links := extractMarkdownLinks(file)
		fileDir := filepath.Dir(file)

		for _, link := range links {
			target := link.Target
			if isExternalOrAnchorOrFlag(target) {
				continue
			}

			// Strip query/anchor
			cleanTarget := target
			if idx := strings.IndexAny(cleanTarget, "#?"); idx != -1 {
				cleanTarget = cleanTarget[:idx]
			}
			cleanTarget = strings.TrimSpace(cleanTarget)
			if cleanTarget == "" {
				continue
			}

			var resolvedPath string
			if filepath.IsAbs(cleanTarget) {
				resolvedPath = cleanTarget
			} else {
				resolvedPath = filepath.Clean(filepath.Join(fileDir, cleanTarget))
			}

			// Check physical existence
			if _, err := os.Stat(resolvedPath); os.IsNotExist(err) {
				// Check if it's in notes/plans/ instead of notes/
				altPath := ""
				if strings.Contains(resolvedPath, "/notes/") && !strings.Contains(resolvedPath, "/notes/plans/") {
					altPath = strings.Replace(resolvedPath, "/notes/", "/notes/plans/", 1)
				} else if strings.Contains(resolvedPath, "/notes/plans/") {
					altPath = strings.Replace(resolvedPath, "/notes/plans/", "/notes/", 1)
				}
				if altPath != "" {
					if _, err2 := os.Stat(altPath); err2 == nil {
						continue // Exists in alternative plans directory
					}
				}

				relSource, _ := filepath.Rel(root, file)
				brokenLinks = append(brokenLinks, relSource+": -> "+target+" (resolved: "+resolvedPath+")")
			}
		}
	}

	if len(brokenLinks) > 0 {
		t.Fatalf("Found %d broken link(s):\n%s", len(brokenLinks), strings.Join(brokenLinks, "\n"))
	} else {
		t.Logf("Successfully verified all markdown links across %d documents without broken links.", len(mdFiles))
	}
}

type mdLink struct {
	Target string
}

func isExternalOrAnchorOrFlag(target string) bool {
	t := strings.TrimSpace(target)
	if strings.HasPrefix(t, "http://") ||
		strings.HasPrefix(t, "https://") ||
		strings.HasPrefix(t, "mailto:") ||
		strings.HasPrefix(t, "ftp://") ||
		strings.HasPrefix(t, "conversation://") ||
		strings.HasPrefix(t, "file://") ||
		strings.HasPrefix(t, "#") ||
		strings.HasPrefix(t, "-") ||
		t == "path.md" { // placeholder in doc
		return true
	}
	return false
}

func extractMarkdownLinks(filePath string) []mdLink {
	f, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var links []mdLink
	scanner := bufio.NewScanner(f)
	inCodeBlock := false

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock {
			continue
		}

		// Find [text](target) without regex
		pos := 0
		for {
			openBracket := strings.Index(line[pos:], "[")
			if openBracket == -1 {
				break
			}
			openBracket += pos

			// Skip if preceded by backtick
			if openBracket > 0 && line[openBracket-1] == '`' {
				pos = openBracket + 1
				continue
			}

			closeBracket := strings.Index(line[openBracket:], "](")
			if closeBracket == -1 {
				pos = openBracket + 1
				continue
			}
			closeBracket += openBracket

			closeParen := strings.Index(line[closeBracket+2:], ")")
			if closeParen == -1 {
				pos = closeBracket + 2
				continue
			}
			closeParen += closeBracket + 2

			target := strings.TrimSpace(line[closeBracket+2 : closeParen])
			if target != "" {
				links = append(links, mdLink{Target: target})
			}

			pos = closeParen + 1
		}
	}

	return links
}
