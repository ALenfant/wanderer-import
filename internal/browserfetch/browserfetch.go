package browserfetch

import "context"

// Fetcher retrieves a request URL from inside a loaded browser page context.
// This is useful for providers whose APIs require same-origin browser state
// that cannot be reproduced with a plain HTTP client.
type Fetcher interface {
	Fetch(ctx context.Context, pageURL, requestURL string, opts RequestOptions) ([]byte, error)
}

type RequestOptions struct {
	Headers map[string]string
	Cookies map[string]string
}

type Closeable interface {
	Close() error
}
