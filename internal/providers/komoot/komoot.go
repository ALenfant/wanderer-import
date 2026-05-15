package komoot

import (
	"net/http"
	"net/url"
	"regexp"

	"wanderer-import/internal/providers/engines/jsonpoints"
)

var numericIDPattern = regexp.MustCompile(`([0-9]{5,})`)

func New(httpClient *http.Client) *jsonpoints.Provider {
	return jsonpoints.NewProvider(jsonpoints.Config{
		ID:      "komoot",
		Name:    "Komoot",
		Domains: []string{"komoot.com"},
		Score:   90,
		Templates: []string{
			"https://api.komoot.de/v007/smart_tours/{id}/coordinates",
		},
		ExtractID:     extractID,
		Parse:         jsonpoints.ParseKomootItems,
		ParseMetadata: jsonpoints.ParseKomootMetadata,
	}, httpClient)
}

func extractID(parsed *url.URL) (string, bool) {
	match := numericIDPattern.FindStringSubmatch(parsed.Path)
	if len(match) < 2 {
		return "", false
	}
	return match[1], true
}
