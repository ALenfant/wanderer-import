package visorando

import (
	"net/http"

	"wanderer-import/internal/providers/engines/gpxlinks"
)

func New(httpClient *http.Client) *gpxlinks.Provider {
	return gpxlinks.NewProvider(gpxlinks.Config{
		ID:                 "visorando",
		Name:               "Visorando",
		Domains:            []string{"visorando.com"},
		AllowExternalLinks: true,
		Score:              85,
	}, httpClient)
}
