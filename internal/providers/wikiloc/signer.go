package wikiloc

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"log"
	"net/http"
	"os"
)

var (
	apiKeyHeaderName = "X-API-KEY"
	apiKeyValue      = os.Getenv("WIKILOC_API_KEY")

	signatureHeaderName = "X-SIGNATURE"
	userAgentHeaderName = "User-Agent"
	userAgentValue      = "Wikiloc Android 3.58.15"

	secretKey = []byte(os.Getenv("WIKILOC_SECRET_KEY"))
)

// ApiSigningTransport implements http.RoundTripper and signs requests
// according to Wikiloc's mobile API rules.
// This is the Go equivalent of ApiSigningInterceptor, SignatureHeader,
// EncodedHeader, and ApiKeyHeader.
type ApiSigningTransport struct {
	Transport http.RoundTripper
	Token     string
}

func (t *ApiSigningTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())

	if clone.Header == nil {
		clone.Header = make(http.Header)
	}

	clone.Header.Set(userAgentHeaderName, userAgentValue)
	clone.Header.Set(apiKeyHeaderName, apiKeyValue)

	var bodyBytes []byte
	if clone.Body != nil {
		bodyBytes, _ = io.ReadAll(clone.Body)
		clone.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}

	fullURL := clone.URL.String()

	msg := make([]byte, 0, len(fullURL)+len(bodyBytes)+len(t.Token))
	msg = append(msg, fullURL...)
	msg = append(msg, bodyBytes...)
	if t.Token != "" {
		msg = append(msg, t.Token...)
	}

	mac := hmac.New(sha256.New, secretKey)
	mac.Write(msg)
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	clone.Header.Set(signatureHeaderName, sig)

	transport := t.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return transport.RoundTrip(clone)
}

func init() {
	if apiKeyValue == "" {
		log.Println("Warning: WIKILOC_API_KEY environment variable is missing; API-based extraction may fail")
	}
	if len(secretKey) == 0 {
		log.Println("Warning: WIKILOC_SECRET_KEY environment variable is missing; API-based extraction may fail")
	}
}
