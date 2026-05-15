package bergfex

import (
	"net/url"
	"testing"
)

func TestExtractID(t *testing.T) {
	parsed, err := url.Parse("https://www.bergfex.fr/sommer/okzitanien/touren/wanderung/3878525%2Ccol-de-lane--roc-blanc--montagne-de-la-seranne/")
	if err != nil {
		t.Fatal(err)
	}
	id, ok := extractID(parsed)
	if !ok {
		t.Fatal("expected tour ID")
	}
	if id != "3878525" {
		t.Fatalf("id = %q, want 3878525", id)
	}
}
