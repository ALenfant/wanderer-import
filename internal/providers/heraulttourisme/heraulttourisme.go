package heraulttourisme

import (
	"net/http"

	"wanderer-import/internal/providers/engines/gpxlinks"
)

func New(httpClient *http.Client) *gpxlinks.Provider {
	return gpxlinks.NewProvider(gpxlinks.Config{
		ID:                 "herault-tourisme",
		Name:               "Herault Tourisme",
		Domains:            []string{"herault-tourisme.com"},
		AllowExternalLinks: true,
		Score:              85,
	}, httpClient)
}
