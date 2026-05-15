package tourismegard

import (
	"net/http"

	"wanderer-import/internal/providers/engines/gpxlinks"
)

func New(httpClient *http.Client) *gpxlinks.Provider {
	return gpxlinks.NewProvider(gpxlinks.Config{
		ID:                 "tourismegard",
		Name:               "Tourisme Gard",
		Domains:            []string{"tourismegard.com"},
		AllowExternalLinks: true,
		Score:              85,
	}, httpClient)
}
