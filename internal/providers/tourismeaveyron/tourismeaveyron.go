package tourismeaveyron

import (
	"net/http"

	"wanderer-import/internal/providers/engines/gpxlinks"
)

func New(httpClient *http.Client) *gpxlinks.Provider {
	return gpxlinks.NewProvider(gpxlinks.Config{
		ID:                 "tourisme-aveyron",
		Name:               "Tourisme Aveyron",
		Domains:            []string{"tourisme-aveyron.com"},
		AllowExternalLinks: true,
		Score:              85,
	}, httpClient)
}
