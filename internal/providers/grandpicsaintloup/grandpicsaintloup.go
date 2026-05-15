package grandpicsaintloup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"

	"wanderer-import/internal/importer"
	"wanderer-import/internal/providers/engines/gpxlinks"
	"wanderer-import/internal/providers/providerkit"
)

var redirectURLPattern = regexp.MustCompile(`url=([^"]+)`)

type Provider struct {
	httpClient *http.Client
	fallback   *gpxlinks.Provider
}

func New(httpClient *http.Client) *Provider {
	client := providerkit.HTTPClient(httpClient)
	if client.Jar == nil {
		jar, _ := cookiejar.New(nil)
		copy := *client
		copy.Jar = jar
		client = &copy
	}
	return &Provider{
		httpClient: client,
		fallback: gpxlinks.NewProvider(gpxlinks.Config{
			ID:                 "grandpicsaintloup-tourisme",
			Name:               "Grand Pic Saint-Loup Tourisme",
			Domains:            []string{"grandpicsaintloup-tourisme.fr"},
			AllowExternalLinks: true,
			Score:              85,
		}, client),
	}
}

func (p *Provider) Name() string {
	return "grandpicsaintloup-tourisme"
}

func (p *Provider) Descriptor() importer.Descriptor {
	return importer.Descriptor{
		ID:      "grandpicsaintloup-tourisme",
		Name:    "Grand Pic Saint-Loup Tourisme",
		Engine:  "gpxlinks-session-redirect",
		Domains: []string{"grandpicsaintloup-tourisme.fr"},
		Status:  "implemented",
	}
}

func (p *Provider) Match(source string) importer.Match {
	parsed, ok := providerkit.ParseHTTPURL(source)
	if !ok || !providerkit.HostMatches(parsed.Hostname(), []string{"grandpicsaintloup-tourisme.fr"}) {
		return importer.Match{}
	}
	return importer.Match{OK: true, Score: 85, Reason: "Grand Pic Saint-Loup itinerary shell"}
}

func (p *Provider) Resolve(ctx context.Context, spec importer.Spec) (*importer.ResolvedTrail, error) {
	source := strings.TrimSpace(spec.Source)
	resolvedSource, err := p.resolveItineraryURL(ctx, source)
	if err != nil {
		return nil, err
	}
	spec.Source = resolvedSource
	return p.fallback.Resolve(ctx, spec)
}

func (p *Provider) resolveItineraryURL(ctx context.Context, source string) (string, error) {
	parsed, ok := providerkit.ParseHTTPURL(source)
	if !ok {
		return "", fmt.Errorf("%s requires an HTTP URL", p.Name())
	}
	if !strings.Contains(parsed.Path, "/_pages/") {
		return source, nil
	}
	initial, err := providerkit.GET(ctx, p.httpClient, source)
	if err != nil {
		return "", err
	}
	_ = initial.Body.Close()
	form := url.Values{}
	form.Set("ajax_target", "interface")
	form.Set("ajax_data[device_sw]", "1470")
	form.Set("ajax_data[device_sh]", "956")
	form.Set("ajax_data[is_touchable]", "0")
	form.Set("ajax_data[init_window_sw]", "1470")
	form.Set("ajax_data[init_window_sh]", "784")
	form.Set("ajax_data[window_sw]", "1470")
	form.Set("ajax_data[window_sw_min]", "1470")
	form.Set("ajax_data[window_sh]", "784")
	form.Set("ajax_data[scrollbar_w]", "0")
	form.Set("ajax_data[scrollbar_h]", "0")
	form.Set("ajax_data[orientation]", "0")
	form.Set("ajax_data[is_redirect]", "1")
	form.Set("ajax_data[class]", "site")
	form.Set("ajax_data[function]", "update_viewport_session")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, source, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", providerkit.UserAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", source)
	res, err := p.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return "", err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return "", fmt.Errorf("Grand Pic Saint-Loup redirect handshake failed with %d", res.StatusCode)
	}
	if redirected := redirectFromHandshake(data); redirected != "" {
		return redirected, nil
	}
	return fallbackItineraryURL(parsed), nil
}

func redirectFromHandshake(data []byte) string {
	var payload struct {
		JSData string `json:"js_data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	match := redirectURLPattern.FindStringSubmatch(payload.JSData)
	if len(match) != 2 {
		return ""
	}
	return strings.ReplaceAll(match[1], `\/`, `/`)
}

func fallbackItineraryURL(parsed *url.URL) string {
	clone := *parsed
	clone.Path = strings.Replace(clone.Path, "/_pages", "", 1)
	return clone.String()
}
