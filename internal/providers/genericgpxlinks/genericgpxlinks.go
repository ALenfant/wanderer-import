package genericgpxlinks

import (
	"net/http"

	"wanderer-import/internal/providers/engines/gpxlinks"
)

func New(httpClient *http.Client) *gpxlinks.Provider {
	return gpxlinks.NewProvider(gpxlinks.Config{
		ID:                 "gpx-link-scraper",
		Name:               "Generic GPX/KML/GeoJSON link scraper",
		AllowAnyDomain:     true,
		AllowExternalLinks: true,
		Score:              50,
	}, httpClient)
}
