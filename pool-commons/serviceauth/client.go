package serviceauth

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// HTTPSClient binds credentials to one configured origin, verifies server
// identity, and rejects all redirects. TokenFile is read per request for rotation.
func HTTPSClient(baseURL, poolID, tokenFile string, roots *x509.CertPool) (*http.Client, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.Fragment != "" || poolID == "" || tokenFile == "" {
		return nil, errors.New("service client requires HTTPS origin, pool and token file")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	return &http.Client{
		Timeout:       30 * time.Second,
		Transport:     &scopedTransport{base: base, poolID: poolID, tokenFile: tokenFile, next: transport},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("service redirects forbidden") },
	}, nil
}

type scopedTransport struct {
	base              *url.URL
	poolID, tokenFile string
	next              http.RoundTripper
}

func (t *scopedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || r.URL.Host != t.base.Host || r.URL.User != nil {
		return nil, errors.New("service request origin mismatch")
	}
	secret, err := os.ReadFile(t.tokenFile)
	if err != nil {
		return nil, errors.New("service credential unavailable")
	}
	token := strings.TrimSpace(string(secret))
	if len(token) != 64 {
		return nil, errors.New("invalid service credential")
	}
	req := r.Clone(r.Context())
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(PoolHeader, t.poolID)
	return t.next.RoundTrip(req)
}

// HTTPSClientWithCAFile supports operator-managed private CAs without an
// insecure verification option. Empty caFile uses the system trust store.
func HTTPSClientWithCAFile(baseURL, poolID, tokenFile, caFile string) (*http.Client, error) {
	var roots *x509.CertPool
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, errors.New("service CA file unavailable")
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("invalid service CA certificate")
		}
	}
	return HTTPSClient(baseURL, poolID, tokenFile, roots)
}
