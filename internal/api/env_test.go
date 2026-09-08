package api

import (
	"strings"
	"testing"

	"github.com/Oein/publix/internal/store"
)

// The same paste has to mean the same thing whether it lands on the
// environment screen or in the import dialog, which is why both go through
// this one function.
func TestCleanEnv(t *testing.T) {
	out, err := cleanEnv([]store.EnvVar{
		{Key: "  DATABASE_URL  ", Value: "postgres://x"},
		{Key: "", Value: "dropped: a blank row is not a variable"},
		{Key: "API_KEY", Value: "abc", Secret: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d variables, want 2: %+v", len(out), out)
	}
	if out[0].Key != "DATABASE_URL" {
		t.Errorf("key = %q, want the surrounding space trimmed", out[0].Key)
	}
	if !out[1].Secret {
		t.Error("the secret flag was dropped")
	}
}

func TestCleanEnvRejectsNamesAShellCannotExport(t *testing.T) {
	for _, key := range []string{"1LEADING_DIGIT", "WITH-DASH", "WITH SPACE", "WITH.DOT"} {
		if _, err := cleanEnv([]store.EnvVar{{Key: key}}); err == nil {
			t.Errorf("%q was accepted as a variable name", key)
		}
	}
	if _, err := cleanEnv([]store.EnvVar{{Key: "GOOD_NAME_2"}}); err != nil {
		t.Errorf("a valid name was rejected: %v", err)
	}
}

// Two rows with the same key is a mistake worth stopping at, not something
// to resolve by silently keeping one of them.
func TestCleanEnvRejectsDuplicates(t *testing.T) {
	_, err := cleanEnv([]store.EnvVar{{Key: "A", Value: "1"}, {Key: "A", Value: "2"}})
	if err == nil {
		t.Fatal("a duplicated key was accepted")
	}
	if !strings.Contains(err.Error(), "twice") {
		t.Errorf("error %q does not say what is wrong", err)
	}
}
