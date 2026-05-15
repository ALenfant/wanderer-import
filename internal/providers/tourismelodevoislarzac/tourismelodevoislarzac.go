package tourismelodevoislarzac

import (
	"net/http"

	"wanderer-import/internal/providers/engines/gpxlinks"
)

func New(httpClient *http.Client) *gpxlinks.Provider {
	return gpxlinks.NewProvider(gpxlinks.Config{
		ID:                 "tourisme-lodevois-larzac",
		Name:               "Tourisme Lodevois Larzac",
		Domains:            []string{"tourisme-lodevois-larzac.fr"},
		AllowExternalLinks: true,
		Score:              85,
	}, httpClient)
}
