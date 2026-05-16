package sitytrail

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
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
			fmt.Sprintf("https://capi.geolives.com/%s/sitytour/trails/{id}", getSitytrailToken()),
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

func getSitytrailToken() string {
	t := os.Getenv("SITYTRAIL_TOKEN")
	if t == "" {
		log.Println("Warning: SITYTRAIL_TOKEN environment variable is missing; SityTrail extraction will fail")
	}
	return t
}
