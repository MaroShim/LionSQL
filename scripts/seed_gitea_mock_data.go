package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	baseURL = "http://localhost:3000/api/v1"
	token   = "01ccbd7896a8d0738b0fa9defefaf781d166a5e4"
)

var client = &http.Client{Timeout: 15 * time.Second}

func apiRequest(method, endpoint string, payload any) (int, []byte, error) {
	var bodyReader io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, baseURL+endpoint, bodyReader)
	if err != nil {
		return 0, nil, err
	}

	req.SetBasicAuth("bob", "1234567890")
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	return resp.StatusCode, respBytes, err
}

func main() {
	fmt.Println("==================================================")
	fmt.Println("  Gitea Comprehensive Mock Data Seeder on LionSQL")
	fmt.Println("==================================================")

	// 1. Create Users
	users := []struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
		FullName string `json:"full_name"`
		Admin    bool   `json:"admin"`
	}{
		{"developer1", "dev1@lionsql.local", "1234567890", "Primary Developer", false},
		{"reviewer1", "reviewer1@lionsql.local", "1234567890", "Senior Code Reviewer", false},
		{"qa_lead", "qa@lionsql.local", "1234567890", "QA Test Automation Lead", false},
		{"perf_eng", "perf@lionsql.local", "1234567890", "High-Load Performance Engineer", false},
	}

	for _, u := range users {
		status, resp, err := apiRequest("POST", "/admin/users", map[string]any{
			"username":              u.Username,
			"email":                 u.Email,
			"password":              u.Password,
			"full_name":             u.FullName,
			"must_change_password":  false,
			"send_notify":           false,
			"admin":                 u.Admin,
		})
		if err != nil {
			fmt.Printf("[FAIL] Create user %s error: %v\n", u.Username, err)
			continue
		}
		if status == 201 {
			fmt.Printf("[OK] Created User: %s (%s)\n", u.Username, u.Email)
		} else if status == 422 {
			fmt.Printf("[EXISTS] User %s already exists\n", u.Username)
		} else {
			fmt.Printf("[INFO] User %s HTTP %d: %s\n", u.Username, status, string(resp))
		}
	}

	// 2. Create Organization
	orgName := "lionsql-core"
	status, resp, _ := apiRequest("POST", "/orgs", map[string]any{
		"username":    orgName,
		"full_name":   "LionSQL Engineering Foundation",
		"description": "Autonomous OLTP Engine, T-SQL Bytecode VM & Hybrid Wire Protocols",
		"visibility":  "public",
	})
	if status == 201 {
		fmt.Printf("[OK] Created Organization: %s\n", orgName)
	} else if status == 422 {
		fmt.Printf("[EXISTS] Organization %s already exists\n", orgName)
	} else {
		fmt.Printf("[INFO] Org %s HTTP %d: %s\n", orgName, status, string(resp))
	}

	// 3. Create Teams in Organization
	teams := []string{"engineers", "reviewers"}
	for _, tName := range teams {
		status, _, _ := apiRequest("POST", fmt.Sprintf("/orgs/%s/teams", orgName), map[string]any{
			"name":                    tName,
			"description":             fmt.Sprintf("Team %s", tName),
			"permission":              "write",
			"can_create_org_repo":     true,
			"includes_all_repositories": true,
		})
		if status == 201 {
			fmt.Printf("[OK] Created Team: %s/%s\n", orgName, tName)
		}
	}

	// 4. Create Repositories
	repos := []struct {
		Org         string
		Name        string
		Description string
	}{
		{orgName, "lion-engine", "Core OLTP Storage Kernel, SQLite VDBE-style VM and Volcano CBO"},
		{orgName, "lion-bench", "Enterprise TPC-C, SSB, and TPC-H Benchmark Suites with 141k tpmC OCC"},
		{orgName, "lion-docs", "Official documentation, architecture guides, and technical specifications"},
		{"bob", "personal-scripts", "Automation and daily database administration scripts"},
	}

	for _, r := range repos {
		var endpoint string
		if r.Org == "bob" {
			endpoint = "/user/repos"
		} else {
			endpoint = fmt.Sprintf("/orgs/%s/repos", r.Org)
		}

		status, resp, _ := apiRequest("POST", endpoint, map[string]any{
			"name":          r.Name,
			"description":   r.Description,
			"private":       false,
			"auto_init":     true,
			"gitignores":    "Go",
			"license":       "MIT",
			"readme":        "Default",
			"default_branch": "main",
		})
		if status == 201 {
			fmt.Printf("[OK] Created Repository: %s/%s\n", r.Org, r.Name)
		} else if status == 409 || status == 422 {
			fmt.Printf("[EXISTS] Repository %s/%s already exists\n", r.Org, r.Name)
		} else {
			fmt.Printf("[INFO] Repo %s/%s HTTP %d: %s\n", r.Org, r.Name, status, string(resp))
		}
	}

	targetRepo := fmt.Sprintf("%s/lion-docs", orgName)

	// 5. Create Milestones
	milestones := []struct {
		Title       string    `json:"title"`
		Description string    `json:"description"`
		DueOn       time.Time `json:"due_on"`
	}{
		{"v1.0.0-GA", "First Official Production GA Release", time.Now().AddDate(0, 1, 0)},
		{"v1.1.0-Scale", "Massive Scale & OCC Hyper-Memory Compaction", time.Now().AddDate(0, 3, 0)},
	}

	var milestoneID int64
	for _, m := range milestones {
		status, resp, _ := apiRequest("POST", fmt.Sprintf("/repos/%s/milestones", targetRepo), map[string]any{
			"title":       m.Title,
			"description": m.Description,
			"due_on":      m.DueOn.Format(time.RFC3339),
		})
		if status == 201 {
			var created struct {
				ID int64 `json:"id"`
			}
			_ = json.Unmarshal(resp, &created)
			if milestoneID == 0 {
				milestoneID = created.ID
			}
			fmt.Printf("[OK] Created Milestone: %s in %s (ID=%d)\n", m.Title, targetRepo, created.ID)
		}
	}
	if milestoneID == 0 {
		_, resp, _ := apiRequest("GET", fmt.Sprintf("/repos/%s/milestones", targetRepo), nil)
		var list []struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(resp, &list)
		if len(list) > 0 {
			milestoneID = list[0].ID
		}
	}

	// 6. Create Labels
	labels := []struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
	}{
		{"bug", "#cf222e", "Something is not working properly"},
		{"enhancement", "#0969da", "New feature or architectural improvement"},
		{"performance", "#d4a72c", "Throughput, latency, and memory optimization"},
		{"protocol", "#8250df", "PGWire and TDS wire protocol compatibility"},
		{"benchmark", "#1a7f37", "TPC-C, SSB, and TPC-H workload validation"},
	}

	labelIDMap := make(map[string]int64)
	for _, l := range labels {
		status, resp, _ := apiRequest("POST", fmt.Sprintf("/repos/%s/labels", targetRepo), l)
		if status == 201 {
			var created struct {
				ID int64 `json:"id"`
			}
			_ = json.Unmarshal(resp, &created)
			labelIDMap[l.Name] = created.ID
			fmt.Printf("[OK] Created Label: %s (#%s) ID=%d\n", l.Name, l.Color, created.ID)
		}
	}

	// If labels already existed, fetch them
	if len(labelIDMap) == 0 {
		_, resp, _ := apiRequest("GET", fmt.Sprintf("/repos/%s/labels", targetRepo), nil)
		var list []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		}
		_ = json.Unmarshal(resp, &list)
		for _, item := range list {
			labelIDMap[item.Name] = item.ID
		}
	}

	// 7. Create Issues with descriptions, assignees, and labels
	issues := []struct {
		Title     string
		Body      string
		Labels    []string
		Milestone int64
	}{
		{
			Title: "Support Subquery Decorrelation in Volcano CBO",
			Body:  "Correlated subqueries in WHERE EXISTS and NOT IN should be transformed into Flat Hash Anti-Joins for sub-50ms execution.",
			Labels: []string{"enhancement", "performance"},
			Milestone: 2,
		},
		{
			Title: "Memory OCC 141,000 tpmC Stress Test Stability Verification",
			Body:  "Verify OCC lock-free monotonic version counters under 64 parallel workers on PostgreSQL Wire Protocol v3.",
			Labels: []string{"benchmark", "performance"},
			Milestone: 1,
		},
		{
			Title: "Fix TDS RPC Parameter Decode for PLP Partially Length-Prefixed NVARCHAR(MAX)",
			Body:  "When clients send NVARCHAR(MAX) with maxLen=0xFFFF, collation (5 bytes) must be consumed before reading PLP chunk streams.",
			Labels: []string{"bug", "protocol"},
			Milestone: 1,
		},
		{
			Title: "Add Client Protocol Recommendation: Default to PGWire :5433",
			Body:  "Document that PGWire is recommended by default for both T-SQL and standard queries due to lightweight framing and 141k tpmC.",
			Labels: []string{"enhancement", "protocol"},
			Milestone: 1,
		},
	}

	for _, issue := range issues {
		var lIDs []int64
		for _, lName := range issue.Labels {
			if id, ok := labelIDMap[lName]; ok {
				lIDs = append(lIDs, id)
			}
		}

		status, resp, err := apiRequest("POST", fmt.Sprintf("/repos/%s/issues", targetRepo), map[string]any{
			"title":     issue.Title,
			"body":      issue.Body,
			"assignees": []string{"bob", "developer1"},
			"labels":    lIDs,
			"milestone": issue.Milestone,
		})
		if err != nil {
			fmt.Printf("[FAIL] Create issue error: %v\n", err)
			continue
		}
		if status == 201 {
			var created struct {
				Index int64 `json:"number"`
			}
			_ = json.Unmarshal(resp, &created)
			fmt.Printf("[OK] Created Issue #%d: %s\n", created.Index, issue.Title)

			// Add conversation comments to Issue #created.Index
			comments := []struct {
				Author string
				Body   string
			}{
				{"reviewer1", "Architecture looks clean! Please make sure regression tests pass with -race flag enabled."},
				{"developer1", "Verified! All 13-domain integration smoke tests passed in 14 seconds without data races."},
			}
			for _, c := range comments {
				cStatus, _, _ := apiRequest("POST", fmt.Sprintf("/repos/%s/issues/%d/comments", targetRepo, created.Index), map[string]any{
					"body": fmt.Sprintf("**@%s**: %s", c.Author, c.Body),
				})
				if cStatus == 201 {
					fmt.Printf("    -> Added comment from @%s on Issue #%d\n", c.Author, created.Index)
				}
			}
		} else {
			fmt.Printf("[INFO] Issue HTTP %d: %s\n", status, string(resp))
		}
	}

	// 8. Create a feature branch and commit a new file
	var branchesResp []struct {
		Name string `json:"name"`
	}
	bStatus, bData, _ := apiRequest("GET", fmt.Sprintf("/repos/%s/branches", targetRepo), nil)
	_ = json.Unmarshal(bData, &branchesResp)
	defaultBranch := "main"
	if len(branchesResp) > 0 {
		defaultBranch = branchesResp[0].Name
		fmt.Printf("[INFO] Discovered repository branches (status %d): ", bStatus)
		for _, b := range branchesResp {
			fmt.Printf("%s ", b.Name)
		}
		fmt.Println()
	} else {
		fmt.Printf("[INFO] No branches found on %s (HTTP %d: %s)\n", targetRepo, bStatus, string(bData))
	}

	branchName := "feature/flat-hash-join"
	status, resp, err := apiRequest("POST", fmt.Sprintf("/repos/%s/branches", targetRepo), map[string]any{
		"new_branch_name": branchName,
		"old_ref_name":    defaultBranch,
	})
	if status == 201 {
		fmt.Printf("[OK] Created Branch: %s in %s\n", branchName, targetRepo)
	} else if status == 409 {
		fmt.Printf("[EXISTS] Branch %s already exists\n", branchName)
	} else {
		fmt.Printf("[INFO] Create Branch HTTP %d: %s (err: %v)\n", status, string(resp), err)
	}

	// Commit a file on the feature branch
	fileContent := `# Flat Hash Join Implementation
This module provides zero-allocation chunked hash join execution for Tier 2 relational algebra.
- Target latency: Sub-50ms on 100k rows
- Zero false-positives
`
	status, resp, err = apiRequest("POST", fmt.Sprintf("/repos/%s/contents/docs/HASH_JOIN.md", targetRepo), map[string]any{
		"branch":  branchName,
		"message": "feat(planner): add flat hash join architecture specification",
		"content": base64.StdEncoding.EncodeToString([]byte(fileContent)),
	})
	if status == 201 {
		fmt.Printf("[OK] Committed docs/HASH_JOIN.md on %s\n", branchName)
	} else if status == 422 {
		fmt.Printf("[EXISTS] File docs/HASH_JOIN.md already committed\n")
	} else {
		fmt.Printf("[INFO] Commit file HTTP %d: %s (err: %v)\n", status, string(resp), err)
	}

	// 9. Create a Pull Request from feature branch to main
	status, resp, err = apiRequest("POST", fmt.Sprintf("/repos/%s/pulls", targetRepo), map[string]any{
		"title":     "feat(planner): implement flat hash join algorithm",
		"body":      "Closes #1. Implements chunked flat hash join for correlated subqueries and high-scale dimension table scans.",
		"head":      branchName,
		"base":      "main",
		"assignees": []string{"bob", "developer1"},
		"labels":    []int64{labelIDMap["enhancement"], labelIDMap["performance"]},
		"milestone": milestoneID,
	})

	var prIndex int64
	if status == 201 {
		var pr struct {
			Index int64 `json:"number"`
		}
		_ = json.Unmarshal(resp, &pr)
		prIndex = pr.Index
		fmt.Printf("[OK] Created Pull Request #%d: %s -> main\n", prIndex, branchName)

		// Review comment on PR
		_, _, _ = apiRequest("POST", fmt.Sprintf("/repos/%s/pulls/%d/reviews", targetRepo, prIndex), map[string]any{
			"body":  "LGTM! Benchmark results show 38ms flight time. Ready to merge.",
			"event": "APPROVED",
		})
		fmt.Printf("    -> Approved PR #%d\n", prIndex)

		// Merge the Pull Request!
		mStatus, mResp, _ := apiRequest("POST", fmt.Sprintf("/repos/%s/pulls/%d/merge", targetRepo, prIndex), map[string]any{
			"Do":    "merge",
			"Title": fmt.Sprintf("Merge pull request #%d from %s", prIndex, branchName),
		})
		if mStatus == 200 {
			fmt.Printf("[OK] Merged Pull Request #%d into main!\n", prIndex)
		} else {
			fmt.Printf("[INFO] PR Merge HTTP %d: %s\n", mStatus, string(mResp))
		}
	} else if status == 409 {
		fmt.Printf("[EXISTS] Pull request already created or merged\n")
	} else {
		fmt.Printf("[INFO] Create PR HTTP %d: %s (err: %v)\n", status, string(resp), err)
	}

	// 10. Create Release Tag v1.0.0-rc1
	status, _, _ = apiRequest("POST", fmt.Sprintf("/repos/%s/releases", targetRepo), map[string]any{
		"tag_name":         "v1.0.0-rc1",
		"target_commitish": "main",
		"name":             "v1.0.0-rc1: Production Candidate",
		"body":             "## LionSQL v1.0.0-rc1 Release Candidate\n- 141k tpmC OCC Engine\n- Full TDS 7.4/8.0 wire listener compatibility\n- Embedded Web Studio\n- Dual-Parser Fast Pratt + ANTLR4 Fallback\n",
		"draft":            false,
		"prerelease":       true,
	})
	if status == 201 {
		fmt.Printf("[OK] Published Release: v1.0.0-rc1 in %s\n", targetRepo)
	} else if status == 409 {
		fmt.Printf("[EXISTS] Release v1.0.0-rc1 already published in %s\n", targetRepo)
	} else {
		fmt.Printf("[INFO] Release HTTP %d\n", status)
	}

	fmt.Println("==================================================")
	fmt.Println("  Mock Data Seeding & Feature Testing Complete!")
	fmt.Println("  Visit Web UI: http://localhost:3000")
	fmt.Println("==================================================")
	_ = os.Stdout.Sync()
}
