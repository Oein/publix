package deployspec

import (
	"os"
	"path/filepath"
	"testing"
)

// The examples are the first thing anyone copies. If one of them does not
// parse, it teaches a mistake — so they are checked like code.
func TestExamplesParse(t *testing.T) {
	matches, err := filepath.Glob("../../examples/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no examples found — this test is not checking anything")
	}

	for _, path := range matches {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// Parse rejects unknown fields, so this also catches an example
			// documenting a setting that does not exist.
			if _, err := Parse(raw); err != nil {
				t.Errorf("%s does not parse: %v", filepath.Base(path), err)
			}
		})
	}
}

// The full reference is meant to list every field. If a field exists in the
// schema but appears nowhere in it, the reference has drifted.
func TestFullReferenceCoversEveryField(t *testing.T) {
	raw, err := os.ReadFile("../../examples/full-reference.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)

	// Top-level and nested keys that must be documented somewhere in it.
	required := []string{
		"name:", "type:", "context:", "dockerfile:", "compose:", "service:",
		"image:", "port:", "replicas:", "command:", "env:", "domains:",
		"routes:", "volumes:", "build:", "health:", "resources:", "release:",
		"cron:",
		"target:", "pull:", "args:", "install:", "output:", "spa:", "runtime:",
		"framework:", "start:", "builder:",
		"cpu:", "memory:", "memoryReservation:", "pidsLimit:",
		"strategy:", "drain:", "autoRollback:", "branch:",
		"interval:", "timeout:", "grace:", "status:",
		"mountPath:", "subPath:", "readOnly:",
		"redirectTo:", "stripPath:", "basicAuth:", "headers:", "tls:", "path:",
		"schedule:",
	}
	for _, key := range required {
		if !contains(text, key) {
			t.Errorf("examples/full-reference.yaml does not document %q", key)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
