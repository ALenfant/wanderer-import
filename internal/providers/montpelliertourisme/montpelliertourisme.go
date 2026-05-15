package montpelliertourisme

import (
	"net/http"

	"wanderer-import/internal/providers/engines/gpxlinks"
)

func New(httpClient *http.Client) *gpxlinks.Provider {
	return gpxlinks.NewProvider(gpxlinks.Config{
		ID:                 "montpellier-tourisme",
		Name:               "Montpellier Tourisme",
		Domains:            []string{"montpellier-tourisme.fr"},
		AllowExternalLinks: true,
		Score:              85,
	}, httpClient)
}
