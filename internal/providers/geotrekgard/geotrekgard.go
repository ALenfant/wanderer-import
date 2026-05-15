package geotrekgard

import (
	"net/http"

	"wanderer-import/internal/providers/engines/geotrek"
)

func New(httpClient *http.Client) *geotrek.Provider {
	return geotrek.NewProvider(geotrek.Config{
		ID:      "geotrek-gard",
		Name:    "Rando Gard Geotrek",
		Domains: []string{"rando.gard.fr", "rando-preprod.gard.fr"},
		Score:   90,
	}, httpClient)
}
