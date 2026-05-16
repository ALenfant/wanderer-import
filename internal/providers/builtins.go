package providers

import (
	"net/http"

	"wanderer-import/internal/browserfetch"
	"wanderer-import/internal/importer"
	"wanderer-import/internal/providers/alltrails"
	"wanderer-import/internal/providers/altituderando"
	"wanderer-import/internal/providers/bergfex"
	"wanderer-import/internal/providers/cevennestourisme"
	"wanderer-import/internal/providers/cirkwi"
	"wanderer-import/internal/providers/decathlonoutdoor"
	"wanderer-import/internal/providers/direct"
	"wanderer-import/internal/providers/engines/gpxlinks"
	"wanderer-import/internal/providers/genericgpxlinks"
	"wanderer-import/internal/providers/geotrekgard"
	"wanderer-import/internal/providers/grandpicsaintloup"
	"wanderer-import/internal/providers/helloways"
	"wanderer-import/internal/providers/heraulttourisme"
	"wanderer-import/internal/providers/komoot"
	"wanderer-import/internal/providers/montpelliertourisme"
	"wanderer-import/internal/providers/sitytrail"
	"wanderer-import/internal/providers/tourismeaveyron"
	"wanderer-import/internal/providers/tourismegard"
	"wanderer-import/internal/providers/tourismelodevoislarzac"
	"wanderer-import/internal/providers/trailsviewer"
	"wanderer-import/internal/providers/visorando"
	"wanderer-import/internal/providers/visugpx"
	"wanderer-import/internal/providers/wikiloc"
)

func Builtins(httpClient *http.Client) []importer.Provider {
	return BuiltinsWithOptions(Options{HTTPClient: httpClient})
}

type Options struct {
	HTTPClient     *http.Client
	BrowserFetcher browserfetch.Fetcher
}

func BuiltinsWithOptions(opts Options) []importer.Provider {
	return []importer.Provider{
		geotrekgard.New(opts.HTTPClient),
		cirkwi.New(opts.HTTPClient),
		visugpx.New(opts.HTTPClient),
		komoot.New(opts.HTTPClient),
		sitytrail.New(opts.HTTPClient),
		altituderando.New(opts.HTTPClient),
		alltrails.NewWithOptions(alltrails.Options{HTTPClient: opts.HTTPClient, BrowserFetcher: opts.BrowserFetcher}),
		bergfex.New(opts.HTTPClient),
		trailsviewer.NewWithOptions(trailsviewer.Options{HTTPClient: opts.HTTPClient, BrowserFetcher: opts.BrowserFetcher}),
		heraulttourisme.New(opts.HTTPClient),
		montpelliertourisme.New(opts.HTTPClient),
		tourismegard.New(opts.HTTPClient),
		tourismeaveyron.New(opts.HTTPClient),
		cevennestourisme.New(opts.HTTPClient),
		grandpicsaintloup.New(opts.HTTPClient),
		tourismelodevoislarzac.New(opts.HTTPClient),
		visorando.NewWithOptions(gpxlinks.Options{HTTPClient: opts.HTTPClient, BrowserFetcher: opts.BrowserFetcher}),
		wikiloc.NewWithOptions(gpxlinks.Options{HTTPClient: opts.HTTPClient, BrowserFetcher: opts.BrowserFetcher}),
		decathlonoutdoor.New(opts.HTTPClient),
		helloways.New(opts.HTTPClient),
		genericgpxlinks.NewWithOptions(gpxlinks.Options{HTTPClient: opts.HTTPClient, BrowserFetcher: opts.BrowserFetcher}),
		direct.New(opts.HTTPClient),
	}
}
