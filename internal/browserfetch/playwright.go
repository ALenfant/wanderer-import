package browserfetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/playwright-community/playwright-go"
)

type PlaywrightOptions struct {
	Browser  string
	Headless bool
}

type PlaywrightFetcher struct {
	opts PlaywrightOptions

	mu      sync.Mutex
	pw      *playwright.Playwright
	browser playwright.Browser
}

func NewPlaywrightFetcher(opts PlaywrightOptions) (*PlaywrightFetcher, error) {
	browser := normalizeBrowser(opts.Browser)
	if browser == "" {
		browser = "chromium"
	}
	switch browser {
	case "chromium", "chrome", "firefox", "webkit":
	default:
		return nil, fmt.Errorf("unsupported browser fetcher %q; supported values: chromium, chrome, firefox, webkit", opts.Browser)
	}
	opts.Browser = browser
	return &PlaywrightFetcher{opts: opts}, nil
}

func (f *PlaywrightFetcher) Fetch(ctx context.Context, pageURL, requestURL string, opts RequestOptions) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.ensureBrowser(); err != nil {
		return nil, err
	}

	contextOptions := playwright.BrowserNewContextOptions{
		Locale:    playwright.String("en-US"),
		UserAgent: playwright.String("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"),
	}
	browserContext, err := f.browser.NewContext(contextOptions)
	if err != nil {
		return nil, err
	}
	defer browserContext.Close()

	page, err := browserContext.NewPage()
	if err != nil {
		return nil, err
	}
	defer page.Close()

	// Mask automation markers
	_ = page.AddInitScript(playwright.Script{
		Content: playwright.String(`
			Object.defineProperty(navigator, 'webdriver', { get: () => undefined });
			window.chrome = { runtime: {} };
			Object.defineProperty(navigator, 'languages', { get: () => ['en-US', 'en'] });
			Object.defineProperty(navigator, 'plugins', { get: () => [1, 2, 3, 4, 5] });
		`),
	})

	var capturedResponse []byte
	var capturedErr error
	var mu sync.Mutex

	if requestURL != "" && requestURL != pageURL {
		page.OnResponse(func(res playwright.Response) {
			if strings.Contains(res.URL(), requestURL) {
				mu.Lock()
				defer mu.Unlock()
				if capturedResponse != nil {
					return
				}
				body, err := res.Body()
				if err != nil {
					capturedErr = err
					return
				}
				capturedResponse = body
			}
		})
	}

	timeout := timeoutMillis(ctx, 30*time.Second)
	if _, err := page.Goto(pageURL, playwright.PageGotoOptions{
		Timeout:   playwright.Float(timeout),
		WaitUntil: playwright.WaitUntilStateLoad,
	}); err != nil {
		return nil, fmt.Errorf("browser navigate %s: %w", pageURL, err)
	}
	_ = page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State:   playwright.LoadStateNetworkidle,
		Timeout: playwright.Float(settleTimeoutMillis(ctx, 5*time.Second)),
	})
	page.WaitForTimeout(settleTimeoutMillis(ctx, 2*time.Second))

	if requestURL == "" || requestURL == pageURL {
		if opts.Script != "" {
			result, err := page.Evaluate(opts.Script, nil)
			if err != nil {
				return nil, fmt.Errorf("browser evaluate script: %w", err)
			}
			switch typed := result.(type) {
			case string:
				return []byte(typed), nil
			case []byte:
				return typed, nil
			default:
				data, err := json.Marshal(result)
				if err != nil {
					return nil, fmt.Errorf("browser evaluate script returned non-serializable %T", result)
				}
				return data, nil
			}
		}
		content, err := page.Content()
		if err != nil {
			return nil, fmt.Errorf("browser get content %s: %w", pageURL, err)
		}
		return []byte(content), nil
	}

	if requestURL != "" && requestURL != pageURL {
		mu.Lock()
		if capturedResponse != nil {
			mu.Unlock()
			return capturedResponse, nil
		}
		if capturedErr != nil {
			mu.Unlock()
			return nil, capturedErr
		}
		mu.Unlock()

		headerArg := make(map[string]any, len(opts.Headers))
		for key, value := range opts.Headers {
			headerArg[key] = value
		}
		cookieArg := make(map[string]any, len(opts.Cookies))
		for key, value := range opts.Cookies {
			cookieArg[key] = value
		}
		result, err := page.Evaluate(`async ({ url, headers, cookies }) => {
			for (const [name, value] of Object.entries(cookies || {})) {
				if (!document.cookie.includes(name + "=")) {
					document.cookie = name + "=" + encodeURIComponent(value) + "; path=/; SameSite=Lax";
				}
			}
			const response = await fetch(url, {
				credentials: "same-origin",
				cache: "no-store",
				headers: headers || {},
			});
			const body = await response.text();
			return {
				status: response.status,
				ok: response.ok,
				body,
			};
		}`, map[string]any{"url": requestURL, "headers": headerArg, "cookies": cookieArg})
		if err != nil {
			return nil, fmt.Errorf("browser fetch %s: %w", requestURL, err)
		}

		payload, ok := result.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("browser fetch returned unexpected payload %T", result)
		}
		status, ok := numericStatus(payload["status"])
		if !ok {
			return nil, fmt.Errorf("browser fetch returned payload without numeric status")
		}
		body, ok := payload["body"].(string)
		if !ok {
			return nil, fmt.Errorf("browser fetch returned payload without string body")
		}
		if status < 200 || status > 299 {
			return nil, fmt.Errorf("browser fetch %s returned %d", requestURL, status)
		}
		return []byte(body), nil
	}
	return nil, nil // Should not reach here if pageURL was handled
}

func (f *PlaywrightFetcher) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	var errs []error
	if f.browser != nil {
		if err := f.browser.Close(); err != nil {
			errs = append(errs, err)
		}
		f.browser = nil
	}
	if f.pw != nil {
		if err := f.pw.Stop(); err != nil {
			errs = append(errs, err)
		}
		f.pw = nil
	}
	return errors.Join(errs...)
}

func (f *PlaywrightFetcher) ensureBrowser() error {
	if f.browser != nil {
		return nil
	}

	pw, err := playwright.Run()
	if err != nil {
		return fmt.Errorf("start Playwright: %w", err)
	}
	f.pw = pw

	options := playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(f.opts.Headless),
		Args: []string{
			"--disable-blink-features=AutomationControlled",
		},
	}
	var browser playwright.Browser
	switch f.opts.Browser {
	case "firefox":
		browser, err = pw.Firefox.Launch(options)
	case "webkit":
		browser, err = pw.WebKit.Launch(options)
	case "chrome":
		options.Channel = playwright.String("chrome")
		browser, err = pw.Chromium.Launch(options)
	default:
		browser, err = pw.Chromium.Launch(options)
	}
	if err != nil {
		_ = pw.Stop()
		f.pw = nil
		return fmt.Errorf("launch Playwright %s: %w", f.opts.Browser, err)
	}
	f.browser = browser
	return nil
}

func normalizeBrowser(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "playwright":
		return "chromium"
	case "chrome":
		return "chrome"
	case "chromium":
		return "chromium"
	case "firefox":
		return "firefox"
	case "webkit", "safari":
		return "webkit"
	default:
		return value
	}
}

func timeoutMillis(ctx context.Context, fallback time.Duration) float64 {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 {
			return float64(remaining.Milliseconds())
		}
		return 1
	}
	return float64(fallback.Milliseconds())
}

func settleTimeoutMillis(ctx context.Context, fallback time.Duration) float64 {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= time.Second {
			return 0
		}
		if remaining < fallback {
			return float64((remaining / 2).Milliseconds())
		}
	}
	return float64(fallback.Milliseconds())
}

func numericStatus(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}
