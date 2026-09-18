package config

import (
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
	"net/http"
	"time"
)

// ServiceClient preserves standalone auth and requires verified HTTPS for scoped regional calls.
func (a AuthConfig) ServiceClient(baseURL string, timeout time.Duration) (*http.Client, error) {
	if a.Method != "scoped" {
		if a.PoolID != "" || a.TokenFile != "" || a.CAFile != "" {
			return nil, fmt.Errorf("regional credential fields require scoped auth")
		}
		return &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
	}
	if a.SecretRef != "" {
		return nil, fmt.Errorf("scoped auth cannot use legacy secret_ref")
	}
	client, err := serviceauth.HTTPSClientWithCAFile(baseURL, a.PoolID, a.TokenFile, a.CAFile)
	if err != nil {
		return nil, err
	}
	client.Timeout = timeout
	return client, nil
}
