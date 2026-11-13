package engine_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/engine"
)

func TestMangleSessionIDForIdent(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "default"},
		{"sess1", "sess1"},
		{"sess_1", "sess_5f1"},
		{"sess-1", "sess_2d1"},
		{"sess-temp-insert-exec", "sess_2dtemp_2dinsert_2dexec"},
		{"pg-1", "pg_2d1"},
		{"pg_1", "pg_5f1"},
		{"user@host:1433", "user_40host_3a1433"},
		{"a.b.c", "a_2eb_2ec"},
	}

	for _, tc := range tests {
		got := engine.MangleSessionIDForIdent(tc.input)
		if got != tc.want {
			t.Errorf("MangleSessionIDForIdent(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestMangleSessionIDForIdent_CollisionFreedom(t *testing.T) {
	inputs := []string{
		"sess1",
		"sess_1",
		"sess-1",
		"sess.1",
		"sess#1",
		"sess:1",
		"sess/1",
		"sess 1",
		"sess_2d1",
		"sess_5f1",
		"pg-1",
		"pg_1",
		"pg.1",
		"pg-10",
		"pg_10",
	}

	seen := make(map[string]string)
	for _, in := range inputs {
		mangled := engine.MangleSessionIDForIdent(in)
		if orig, exists := seen[mangled]; exists {
			t.Fatalf("Collision detected! Inputs %q and %q both mangled to %q", orig, in, mangled)
		}
		seen[mangled] = in

		// Verify every character in mangled is a valid SQL unquoted identifier char [a-zA-Z0-9_]
		for _, ch := range mangled {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
				t.Fatalf("Mangled identifier %q for input %q contains illegal SQL character %c", mangled, in, ch)
			}
		}
	}

	// Dynamic fuzzing with 1,000 distinct combinations
	for i := 0; i < 1000; i++ {
		raw := fmt.Sprintf("session-%d_sub.%d@host:%d", i, i%7, i%13)
		mangled := engine.MangleSessionIDForIdent(raw)
		if orig, exists := seen[mangled]; exists {
			t.Fatalf("Collision detected during fuzzing! Inputs %q and %q both mangled to %q", orig, raw, mangled)
		}
		seen[mangled] = raw
	}
}
