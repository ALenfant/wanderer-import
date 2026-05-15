package session

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadNetscapeCookies(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cookies.txt")
	data := "# Netscape HTTP Cookie File\n" +
		".example.com\tTRUE\t/\tTRUE\t1893456000\tsession\tabc123\n" +
		"#HttpOnly_.example.com\tTRUE\t/\tFALSE\t1893456000\tflag\tyes\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := LoadNetscapeCookies(jar, path); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodGet, "https://www.example.com/path", nil)
	if err != nil {
		t.Fatal(err)
	}
	cookies := jar.Cookies(req.URL)
	if len(cookies) != 2 {
		t.Fatalf("expected 2 cookies, got %d: %#v", len(cookies), cookies)
	}
	if cookies[0].Name != "session" || cookies[0].Value != "abc123" {
		t.Fatalf("unexpected first cookie: %#v", cookies[0])
	}
}

func TestHeaderTransportOverridesHeaders(t *testing.T) {
	var gotUserAgent string
	var gotReferer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserAgent = r.Header.Get("User-Agent")
		gotReferer = r.Header.Get("Referer")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := NewHTTPClient(context.Background(), Options{
		UserAgent: "custom-agent",
		Referer:   "https://example.com/source",
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "provider-agent")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()

	if gotUserAgent != "custom-agent" {
		t.Fatalf("expected overridden user-agent, got %q", gotUserAgent)
	}
	if gotReferer != "https://example.com/source" {
		t.Fatalf("expected referer, got %q", gotReferer)
	}
}

func TestCookiesFromBrowserRejectsUnsupportedBrowser(t *testing.T) {
	_, err := NewHTTPClient(context.Background(), Options{CookiesFromBrowser: "netscape-navigator"})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "supported: all, brave, chrome") {
		t.Fatalf("expected actionable error, got %q", got)
	}
}

func TestParseBrowserSpec(t *testing.T) {
	request, err := parseBrowserSpec("firefox:default-release")
	if err != nil {
		t.Fatal(err)
	}
	if request.browser != "firefox" {
		t.Fatalf("unexpected browser %q", request.browser)
	}
	if request.profile != "default-release" {
		t.Fatalf("unexpected profile %q", request.profile)
	}
}

func TestFirefoxCookiesSelectFirefoxImpersonation(t *testing.T) {
	headers, err := headersForOptions(Options{CookiesFromBrowser: "firefox"})
	if err != nil {
		t.Fatal(err)
	}
	if got := headers.Get("User-Agent"); !strings.Contains(got, "Firefox/") {
		t.Fatalf("expected Firefox user-agent, got %q", got)
	}
}

func TestExplicitFirefoxImpersonation(t *testing.T) {
	headers, err := headersForOptions(Options{Impersonate: "firefox"})
	if err != nil {
		t.Fatal(err)
	}
	if got := headers.Get("User-Agent"); !strings.Contains(got, "Firefox/") {
		t.Fatalf("expected Firefox user-agent, got %q", got)
	}
}
