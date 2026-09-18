package main

import "net/http"

// This route table is used only for authenticated broker tunnel dispatch. The
// public edge uses runner URLs directly and must never receive these headers.
type runnerRoute struct{ URL, Bearer string }
type runnerRoutes map[string]runnerRoute

// Return redirects to the broker without carrying a local runner credential to
// another origin. A runner's redirect is not authority to change the target.
var runnerHTTPClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
