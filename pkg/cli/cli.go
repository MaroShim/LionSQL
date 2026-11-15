package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"lionsql/pkg/config"
	"lionsql/pkg/engine"
	"lionsql/pkg/pathutil"
)

// Config configures the CLI execution environment.
type Config struct {
	Execute     string    // -e / --execute inline SQL string
	File        string    // -f / --file SQL script path
	Interactive bool      // -i / --interactive REPL mode
	DataDir     string    // data directory
	DiskType    string    // disk engine backend
	MemoryType  string    // memory engine backend
	In          io.Reader // input stream (defaults to os.Stdin)
	Out         io.Writer // output stream (defaults to os.Stdout)
	Err         io.Writer // error stream (defaults to os.Stderr)
}

// FormatQueryResult writes tabular query results to the given writer.
// If multiple result sets are chained via res.Next, all result sets are rendered sequentially.
func FormatQueryResult(w io.Writer, res *engine.QueryResult) {
	if res == nil {
		return
	}
	for cur := res; cur != nil; cur = cur.Next {
		if len(cur.Columns) > 0 {
			tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
			fmt.Fprintln(tw, strings.Join(cur.Columns, "\t"))
			for _, row := range cur.Rows {
				var vals []string
				for _, col := range cur.Columns {
					v := row[col]
					if v == nil {
						vals = append(vals, "NULL")
					} else {
						vals = append(vals, fmt.Sprintf("%v", v))
					}
				}
				fmt.Fprintln(tw, strings.Join(vals, "\t"))
			}
			tw.Flush()
		}
		for _, msg := range cur.Messages {
			if strings.TrimSpace(msg) != "" {
				fmt.Fprintln(w, msg)
			}
		}
		fmt.Fprintf(w, "(%d row(s) affected, %d µs)\n", cur.RowsAffected, cur.DurationUS)
		if cur.Next != nil {
			fmt.Fprintln(w)
		}
	}
}

// RunCLI executes SQL statements non-interactively (-e, -f) or launches the interactive REPL.
func RunCLI(cfg Config, coord *engine.Coordinator) error {
	in := cfg.In
	if in == nil {
		in = os.Stdin
	}
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}
	errOut := cfg.Err
	if errOut == nil {
		errOut = os.Stderr
	}

	// 1. Headless inline query execution (-e / --execute)
	if cfg.Execute != "" {
		res, err := coord.ExecuteSQL(cfg.Execute)
		if err != nil {
			return err
		}
		if res.Error != "" {
			return fmt.Errorf("sql execution error: %s", res.Error)
		}
		FormatQueryResult(out, res)
		return nil
	}

	// 2. Headless script file execution (-f / --file)
	if cfg.File != "" {
		cleanPath := filepath.Clean(cfg.File)
		if err := pathutil.ValidateRegularFile(cleanPath, true); err != nil {
			return fmt.Errorf("invalid file path %q: %w", cfg.File, err)
		}
		content, err := os.ReadFile(cleanPath)
		if err != nil {
			return fmt.Errorf("failed to read script file %q: %w", cfg.File, err)
		}
		res, err := coord.ExecuteSQL(string(content))
		if err != nil {
			return err
		}
		if res.Error != "" {
			return fmt.Errorf("sql execution error in %s: %s", cfg.File, res.Error)
		}
		FormatQueryResult(out, res)
		return nil
	}

	// 3. Interactive REPL Mode
	return runREPL(in, out, errOut, coord)
}

func runREPL(in io.Reader, out io.Writer, errOut io.Writer, coord *engine.Coordinator) error {
	fmt.Fprintf(out, "LionSQL Interactive Shell (v%s)\n", config.Version)
	fmt.Fprintf(out, "Type \"help\" for help, \"exit\" or \"quit\" to exit.\n\n")

	scanner := bufio.NewScanner(in)
	var queryBuffer strings.Builder
	prompt := "LionSQL> "

	for {
		fmt.Fprint(out, prompt)
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())

		// Single-line commands
		if queryBuffer.Len() == 0 {
			lower := strings.ToLower(line)
			switch lower {
			case "exit", "quit", "\\q", "bye":
				fmt.Fprintln(out, "Bye")
				return nil
			case "help", "\\h", "?":
				printREPLHelp(out)
				continue
			case "clear", "cls":
				fmt.Fprint(out, "\033[H\033[2J")
				continue
			case "":
				continue
			}
		}

		if strings.EqualFold(line, "GO") {
			// Execute buffered batch
			q := strings.TrimSpace(queryBuffer.String())
			queryBuffer.Reset()
			prompt = "LionSQL> "
			if q != "" {
				executeAndPrint(out, errOut, coord, q)
			}
			continue
		}

		if queryBuffer.Len() > 0 {
			queryBuffer.WriteString(" ")
		}
		queryBuffer.WriteString(line)

		// If line ends with semicolon, execute immediately
		if strings.HasSuffix(line, ";") {
			q := strings.TrimSpace(queryBuffer.String())
			queryBuffer.Reset()
			prompt = "LionSQL> "
			if q != "" {
				executeAndPrint(out, errOut, coord, q)
			}
		} else {
			// Multi-line continuation prompt
			prompt = "   ...> "
		}
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		return err
	}
	fmt.Fprintln(out, "\nBye")
	return nil
}

func executeAndPrint(out, errOut io.Writer, coord *engine.Coordinator, query string) {
	res, err := coord.ExecuteSQL(query)
	if err != nil {
		fmt.Fprintf(errOut, "Error: %v\n\n", err)
		return
	}
	if res.Error != "" {
		fmt.Fprintf(errOut, "Error: %s\n\n", res.Error)
		return
	}
	FormatQueryResult(out, res)
	fmt.Fprintln(out)
}

func printREPLHelp(w io.Writer) {
	fmt.Fprintln(w, `LionSQL Shell Commands:
  help, \h, ?      Show this help text
  exit, quit, \q   Exit the interactive shell
  clear, cls       Clear the terminal screen
  GO               Execute the preceding batch of statements

Enter T-SQL statements ending with a semicolon (;) or type GO on a new line to execute.`)
	fmt.Fprintln(w)
}
