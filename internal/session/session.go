package session

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/browserutils/kooky"
	_ "github.com/browserutils/kooky/browser/all"
)

const (
	chromeUserAgent  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	firefoxUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:126.0) Gecko/20100101 Firefox/126.0"
)

// Options describes source-site HTTP session behavior. It is intentionally
// separate from Wanderer authentication; these credentials are only used when
// resolving provider URLs.
type Options struct {
	CookiesFile        string
	CookiesFromBrowser string
	UserAgent          string
	Referer            string
	Impersonate        string
}

func (o Options) Empty() bool {
	return strings.TrimSpace(o.CookiesFile) == "" &&
		strings.TrimSpace(o.CookiesFromBrowser) == "" &&
		strings.TrimSpace(o.UserAgent) == "" &&
		strings.TrimSpace(o.Referer) == "" &&
		strings.TrimSpace(o.Impersonate) == ""
}

func NewHTTPClient(ctx context.Context, opts Options) (*http.Client, error) {
	if opts.Empty() {
		return nil, nil
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(opts.CookiesFile) != "" {
		if err := LoadNetscapeCookies(jar, opts.CookiesFile); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(opts.CookiesFromBrowser) != "" {
		if err := LoadBrowserCookies(ctx, jar, opts.CookiesFromBrowser); err != nil {
			return nil, err
		}
	}

	headers, err := headersForOptions(opts)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Jar: jar,
		Transport: &HeaderTransport{
			Base:    http.DefaultTransport,
			Headers: headers,
		},
	}, nil
}

func LoadBrowserCookies(ctx context.Context, jar *cookiejar.Jar, spec string) error {
	request, err := parseBrowserSpec(spec)
	if err != nil {
		return err
	}
	cookies, readErr := kooky.ReadCookies(ctx, browserCookieFilters(request)...)
	if readErr != nil && len(cookies) == 0 {
		return fmt.Errorf("read cookies from browser %q: %w", request.browser, readErr)
	}
	if len(cookies) == 0 {
		return fmt.Errorf("no cookies found for browser %q; close the browser or export cookies with --cookies if the store is locked", request.browser)
	}
	for _, cookie := range cookies {
		addBrowserCookie(jar, cookie)
	}
	return nil
}

type browserCookieRequest struct {
	browser string
	profile string
}

func parseBrowserSpec(spec string) (browserCookieRequest, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return browserCookieRequest{}, fmt.Errorf("--cookies-from-browser requires a browser name")
	}
	parts := strings.SplitN(spec, ":", 2)
	request := browserCookieRequest{
		browser: strings.ToLower(strings.TrimSpace(parts[0])),
	}
	if len(parts) == 2 {
		request.profile = strings.TrimSpace(parts[1])
	}
	switch request.browser {
	case "all", "brave", "chrome", "chromium", "edge", "firefox", "opera", "safari":
		return request, nil
	default:
		return browserCookieRequest{}, fmt.Errorf("unsupported --cookies-from-browser browser %q; supported: all, brave, chrome, chromium, edge, firefox, opera, safari", request.browser)
	}
}

func browserCookieFilters(request browserCookieRequest) []kooky.Filter {
	filters := []kooky.Filter{kooky.Valid}
	if request.browser != "all" {
		filters = append(filters, kooky.FilterFunc(func(cookie *kooky.Cookie) bool {
			return cookie != nil &&
				cookie.Browser != nil &&
				strings.EqualFold(cookie.Browser.Browser(), request.browser)
		}))
	}
	if request.profile != "" {
		filters = append(filters, kooky.FilterFunc(func(cookie *kooky.Cookie) bool {
			return cookie != nil &&
				cookie.Browser != nil &&
				strings.EqualFold(cookie.Browser.Profile(), request.profile)
		}))
	}
	return filters
}

func addBrowserCookie(jar *cookiejar.Jar, cookie *kooky.Cookie) {
	if cookie == nil || cookie.Name == "" || cookie.Domain == "" {
		return
	}
	scheme := "http"
	if cookie.Secure {
		scheme = "https"
	}
	host := strings.TrimPrefix(cookie.Domain, ".")
	path := cookie.Path
	if path == "" {
		path = "/"
	}
	cookieURL := &url.URL{Scheme: scheme, Host: host, Path: path}
	httpCookie := cookie.Cookie
	if httpCookie.Path == "" {
		httpCookie.Path = path
	}
	jar.SetCookies(cookieURL, []*http.Cookie{&httpCookie})
}

func headersForOptions(opts Options) (http.Header, error) {
	headers := http.Header{}
	userAgent := strings.TrimSpace(opts.UserAgent)
	impersonate := strings.ToLower(strings.TrimSpace(opts.Impersonate))
	if impersonate == "" {
		if request, err := parseBrowserSpec(opts.CookiesFromBrowser); err == nil {
			switch request.browser {
			case "brave", "chrome", "chromium", "edge", "opera":
				impersonate = "chrome"
			case "firefox":
				impersonate = "firefox"
			case "safari":
				impersonate = "safari"
			}
		}
	}
	switch impersonate {
	case "":
	case "chrome", "chromium":
		if userAgent == "" {
			userAgent = chromeUserAgent
		}
		headers.Set("Accept-Language", "en-US,en;q=0.9,fr;q=0.8")
		headers.Set("Sec-Ch-Ua", `"Chromium";v="124", "Google Chrome";v="124", "Not-A.Brand";v="99"`)
		headers.Set("Sec-Ch-Ua-Mobile", "?0")
		headers.Set("Sec-Ch-Ua-Platform", `"macOS"`)
		headers.Set("Sec-Fetch-Dest", "document")
		headers.Set("Sec-Fetch-Mode", "navigate")
		headers.Set("Sec-Fetch-Site", "none")
		headers.Set("Upgrade-Insecure-Requests", "1")
	case "firefox":
		if userAgent == "" {
			userAgent = firefoxUserAgent
		}
		headers.Set("Accept-Language", "en-US,en;q=0.9,fr;q=0.8")
		headers.Set("Upgrade-Insecure-Requests", "1")
	case "safari":
		if userAgent == "" {
			userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15"
		}
		headers.Set("Accept-Language", "en-US,en;q=0.9,fr;q=0.8")
	default:
		return nil, fmt.Errorf("unsupported --impersonate value %q; supported values: chrome, firefox, safari", opts.Impersonate)
	}
	if userAgent != "" {
		headers.Set("User-Agent", userAgent)
	}
	if referer := strings.TrimSpace(opts.Referer); referer != "" {
		headers.Set("Referer", referer)
	}
	return headers, nil
}

type HeaderTransport struct {
	Base    http.RoundTripper
	Headers http.Header
}

func (t *HeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if len(t.Headers) == 0 {
		return base.RoundTrip(req)
	}
	clone := req.Clone(req.Context())
	for key, values := range t.Headers {
		clone.Header.Del(key)
		for _, value := range values {
			clone.Header.Add(key, value)
		}
	}
	return base.RoundTrip(clone)
}

func LoadNetscapeCookies(jar *cookiejar.Jar, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || (strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "#HttpOnly_")) {
			continue
		}
		cookie, cookieURL, err := parseNetscapeCookie(line)
		if err != nil {
			return fmt.Errorf("%s:%d: %w", path, lineNumber, err)
		}
		jar.SetCookies(cookieURL, []*http.Cookie{cookie})
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func parseNetscapeCookie(line string) (*http.Cookie, *url.URL, error) {
	httpOnly := false
	if strings.HasPrefix(line, "#HttpOnly_") {
		httpOnly = true
		line = strings.TrimPrefix(line, "#HttpOnly_")
	}
	fields := strings.Split(line, "\t")
	if len(fields) != 7 {
		fields = strings.Fields(line)
	}
	if len(fields) != 7 {
		return nil, nil, fmt.Errorf("invalid Netscape cookie line: expected 7 fields")
	}

	domain := strings.TrimSpace(fields[0])
	path := strings.TrimSpace(fields[2])
	secure := strings.EqualFold(fields[3], "TRUE")
	expiresUnix, err := strconv.ParseInt(strings.TrimSpace(fields[4]), 10, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid cookie expiry %q", fields[4])
	}
	name := strings.TrimSpace(fields[5])
	value := fields[6]
	if domain == "" || name == "" {
		return nil, nil, fmt.Errorf("cookie domain and name are required")
	}
	if path == "" {
		path = "/"
	}

	scheme := "http"
	if secure {
		scheme = "https"
	}
	host := strings.TrimPrefix(domain, ".")
	cookieURL := &url.URL{Scheme: scheme, Host: host, Path: path}
	cookie := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		Domain:   domain,
		Secure:   secure,
		HttpOnly: httpOnly,
	}
	if expiresUnix > 0 {
		cookie.Expires = time.Unix(expiresUnix, 0)
	}
	return cookie, cookieURL, nil
}
