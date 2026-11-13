package main

import (
	"flag"
	"strings"
	"testing"
)

func TestCLIDbMailWorker_Flags(t *testing.T) {
	// 1. -h should return flag.ErrHelp
	err := runDbMailWorker([]string{"-h"})
	if err != flag.ErrHelp {
		t.Fatalf("expected flag.ErrHelp for -h, got %v", err)
	}

	// 2. Missing -smtp-host should return error
	err = runDbMailWorker([]string{"-db-url", "postgres://localhost:5433/master"})
	if err == nil || !strings.Contains(err.Error(), "-smtp-host is required") {
		t.Fatalf("expected '-smtp-host is required' error, got %v", err)
	}
}
