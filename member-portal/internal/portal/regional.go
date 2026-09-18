package portal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/memberauth"
)

type Region struct {
	PoolID string `yaml:"pool_id" json:"pool_id"`
	Name   string `yaml:"name" json:"name"`
	URL    string `yaml:"url" json:"-"`
	CAFile string `yaml:"ca_file" json:"-"`
}
type RegionalClient struct {
	Region     Region
	origin     *url.URL
	http       *http.Client
	signerPath string
}

func NewRegionalClient(region Region, signerPath string) (*RegionalClient, error) {
	u, err := url.Parse(region.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || region.PoolID == "" || region.Name == "" {
		return nil, errors.New("regional pool identity and HTTPS origin required")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if region.CAFile != "" {
		raw, err := os.ReadFile(region.CAFile)
		if err != nil {
			return nil, err
		}
		if !roots.AppendCertsFromPEM(raw) {
			return nil, errors.New("invalid regional CA")
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	return &RegionalClient{Region: region, origin: u, signerPath: signerPath, http: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("regional redirects forbidden") }}}, nil
}

var enrollmentAction = regexp.MustCompile(`^/member/v1/enrollments/[A-Za-z0-9_-]+/(status|earnings|rotate|retire|opt-outs)$`)
var optoutDelete = regexp.MustCompile(`^/member/v1/enrollments/[A-Za-z0-9_-]+/opt-outs/[A-Za-z0-9_-]+$`)
var transferAction = regexp.MustCompile(`^/member/v1/hardware/[A-Za-z0-9_-]+/transfer$`)

func allowedMemberPath(method, path string) bool {
	if strings.ContainsAny(path, "%?#\\") || strings.Contains(path, "..") {
		return false
	}
	switch path {
	case "/member/v1/membership", "/member/v1/device-transfers", "/member/v1/regional-report":
		return method == "GET"
	case "/member/v1/join":
		return method == "POST"
	case "/member/v1/enrollments":
		return method == "GET" || method == "POST"
	}
	if enrollmentAction.MatchString(path) {
		action := path[strings.LastIndex(path, "/")+1:]
		return method == "GET" && (action == "status" || action == "earnings" || action == "opt-outs") || method == "POST" && (action == "rotate" || action == "retire" || action == "opt-outs")
	}
	return method == "DELETE" && optoutDelete.MatchString(path) || method == "POST" && transferAction.MatchString(path)
}
func (c *RegionalClient) Do(ctx context.Context, session Session, method, path string, body io.Reader) (*http.Response, error) {
	if !allowedMemberPath(method, path) {
		return nil, errors.New("regional operation forbidden")
	}
	signer, err := memberauth.LoadSigner(c.signerPath)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if !now.Before(session.ExpiresAt) {
		return nil, errUnauthorized
	}
	ttl := memberauth.MaxTTL
	if remaining := session.ExpiresAt.Sub(now); remaining < ttl {
		ttl = remaining
	}
	token, err := signer.Sign(session.Wallet, c.Region.PoolID, session.ID, now, ttl)
	if err != nil {
		return nil, err
	}
	target := *c.origin
	target.Path = path
	target.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", c.origin.Scheme+"://"+c.origin.Host)
	req.Header.Set("Content-Type", "application/json")
	return c.http.Do(req)
}
