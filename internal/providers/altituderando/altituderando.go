package altituderando

import (
	"net/http"
	"regexp"

	"wanderer-import/internal/providers/engines/jsonpoints"
	"wanderer-import/internal/providers/engines/pagejson"
)

var traceURLPattern = regexp.MustCompile(`(?i)url_trace:\s*['"]([^'"]+\.json)['"]`)

func New(httpClient *http.Client) *pagejson.Provider {
	return pagejson.NewProvider(pagejson.Config{
		ID:         "altituderando",
		Name:       "AltitudeRando",
		Domains:    []string{"altituderando.com"},
		Score:      90,
		URLPattern: traceURLPattern,
		Parse:      jsonpoints.ParseGeoJSONLineString("latlon"),
	}, httpClient)
}
