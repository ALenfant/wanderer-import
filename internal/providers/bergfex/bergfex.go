package bergfex

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"wanderer-import/internal/providers/engines/urltemplate"
)

var tourIDPattern = regexp.MustCompile(`(?i)/touren/[^/]+/([0-9]+)(?:%2c|,|/)`)

func New(httpClient *http.Client) *urltemplate.Provider {
	return urltemplate.NewProvider(urltemplate.Config{
		ID:      "bergfex",
		Name:    "Bergfex",
		Domains: []string{"bergfex.at", "bergfex.ch", "bergfex.com", "bergfex.de", "bergfex.es", "bergfex.fr", "bergfex.it"},
		Score:   90,
		Templates: []string{
			"https://www.bergfex.fr/downloads/gps/?id={id}&fileType=gpx",
		},
		ExtractID: extractID,
	}, httpClient)
}

func extractID(parsed *url.URL) (string, bool) {
	path := strings.ToLower(parsed.EscapedPath())
	match := tourIDPattern.FindStringSubmatch(path)
	if len(match) != 2 {
		return "", false
	}
	return match[1], true
}
