package genericgpxlinks

import (
	"net/http"

	"wanderer-import/internal/providers/engines/gpxlinks"
)

func New(httpClient *http.Client) *gpxlinks.Provider {
	return NewWithOptions(gpxlinks.Options{HTTPClient: httpClient})
}

func NewWithOptions(opts gpxlinks.Options) *gpxlinks.Provider {
	return gpxlinks.NewProviderWithOptions(gpxlinks.Config{
		ID:                 "gpx-link-scraper",
		Name:               "Generic GPX/KML/GeoJSON link scraper",
		AllowAnyDomain:     true,
		AllowExternalLinks: true,
		Score:              50,
	}, opts)
}
