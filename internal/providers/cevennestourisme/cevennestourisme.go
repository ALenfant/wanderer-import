package cevennestourisme

import (
	"net/http"

	"wanderer-import/internal/providers/engines/gpxlinks"
)

func New(httpClient *http.Client) *gpxlinks.Provider {
	return gpxlinks.NewProvider(gpxlinks.Config{
		ID:                 "cevennes-tourisme",
		Name:               "Cevennes Tourisme",
		Domains:            []string{"cevennes-tourisme.fr"},
		AllowExternalLinks: true,
		Score:              90,
	}, httpClient)
}
