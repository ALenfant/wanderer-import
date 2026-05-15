package visugpx

import (
	"net/http"
	"net/url"
	"path"
	"strings"

	"wanderer-import/internal/providers/engines/urltemplate"
)

func New(httpClient *http.Client) *urltemplate.Provider {
	return urltemplate.NewProvider(urltemplate.Config{
		ID:      "visugpx",
		Name:    "VisuGPX",
		Domains: []string{"visugpx.com"},
		Score:   90,
		Templates: []string{
			"https://www.visugpx.com/download.php?id={id}",
		},
		ExtractID: extractID,
	}, httpClient)
}

func extractID(parsed *url.URL) (string, bool) {
	if id := strings.TrimSpace(parsed.Query().Get("id")); id != "" {
		return id, true
	}
	id := strings.TrimSuffix(path.Base(parsed.Path), path.Ext(parsed.Path))
	if id == "." || id == "/" || id == "" {
		return "", false
	}
	return id, true
}
