package sitytrail

import (
	"os"
	"testing"
)

func TestGetSitytrailToken(t *testing.T) {
	if os.Getenv("SITYTRAIL_TOKEN") == "" {
		t.Skip("skipping test; SITYTRAIL_TOKEN not set")
	}
	token := getSitytrailToken()
	if token == "" {
		t.Error("expected token to be non-empty")
	}
}
