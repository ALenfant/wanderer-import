package wikiloc

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApiSigningTransport(t *testing.T) {
	expectedURL := "https://www.wikiloc.com/wikiloc/api2/trail/60586940/preview/open"

	// Calculate expected signature for the URL with no body and no token.
	mac := hmac.New(sha256.New, secretKey)
	mac.Write([]byte(expectedURL))
	expectedSig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != userAgentValue {
			t.Errorf("User-Agent = %q, want %q", got, userAgentValue)
		}
		if got := r.Header.Get("X-API-KEY"); got != apiKeyValue {
			t.Errorf("X-API-KEY = %q, want %q", got, apiKeyValue)
		}
		sig := r.Header.Get("X-SIGNATURE")
		if sig == "" {
			t.Error("missing X-SIGNATURE header")
		}
		if sig != expectedSig {
			t.Errorf("X-SIGNATURE = %q, want %q", sig, expectedSig)
		}
		w.WriteHeader(http.StatusOK)
	})

	req, err := http.NewRequest("GET", expectedURL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	transport := &ApiSigningTransport{
		Transport: &mockRoundTripper{handler: handler},
		Token:     "",
	}

	_, err = transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
}

type mockRoundTripper struct {
	handler http.Handler
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	m.handler.ServeHTTP(w, req)
	return w.Result(), nil
}
