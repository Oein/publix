package api

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// The dashboard falls back to English for a key a translation is missing, so
// a gap never breaks a screen — which is exactly why it needs a test. Without
// one, a Korean dashboard would quietly grow English patches that nobody
// notices until a user reports them.

var localeKey = regexp.MustCompile(`(?m)^  '([A-Za-z0-9.]+)':`)

func localeKeys(t *testing.T, name string) []string {
	t.Helper()
	path := filepath.Join("..", "..", "web", "src", "lib", "locales", name)
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var keys []string
	seen := map[string]bool{}
	for _, match := range localeKey.FindAllStringSubmatch(string(source), -1) {
		if seen[match[1]] {
			t.Errorf("%s declares %q twice; the second silently wins", name, match[1])
		}
		seen[match[1]] = true
		keys = append(keys, match[1])
	}
	if len(keys) == 0 {
		t.Fatalf("%s: no keys found — has the catalogue's format changed?", name)
	}
	sort.Strings(keys)
	return keys
}

func TestLocaleCatalogsHaveTheSameKeys(t *testing.T) {
	english := localeKeys(t, "en.js")
	korean := localeKeys(t, "ko.js")

	has := func(keys []string, want string) bool {
		i := sort.SearchStrings(keys, want)
		return i < len(keys) && keys[i] == want
	}

	for _, key := range english {
		if !has(korean, key) {
			t.Errorf("ko.js is missing %q, so it will show the English string", key)
		}
	}
	for _, key := range korean {
		if !has(english, key) {
			t.Errorf("ko.js has %q, which en.js does not — a typo, or a stale key", key)
		}
	}
}
