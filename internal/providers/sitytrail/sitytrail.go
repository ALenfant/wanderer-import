package sitytrail

import (
	"net/http"
	"net/url"
	"regexp"

	"wanderer-import/internal/providers/engines/jsonpoints"
)

var numericIDPattern = regexp.MustCompile(`([0-9]{3,})`)

func New(httpClient *http.Client) *jsonpoints.Provider {
	return jsonpoints.NewProvider(jsonpoints.Config{
		ID:      "sitytrail",
		Name:    "SityTrail",
		Domains: []string{"sitytrail.com", "geolives.com"},
		Score:   90,
		Templates: []string{
			"https://capi.geolives.com/qq0zvz2zws4bq2lad0pu/sitytour/trails/{id}",
		},
		ExtractID:     extractID,
		Parse:         jsonpoints.ParseSityTrailWKT,
		ParseMetadata: jsonpoints.ParseSityTrailMetadata,
	}, httpClient)
}

func extractID(parsed *url.URL) (string, bool) {
	match := numericIDPattern.FindStringSubmatch(parsed.Path)
	if len(match) < 2 {
		return "", false
	}
	return match[1], true
}
