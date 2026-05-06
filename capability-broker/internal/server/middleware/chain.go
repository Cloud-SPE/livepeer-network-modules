// Package middleware provides the HTTP middleware chain wrapping paid routes.
//
// Order matters. The chain is composed outside-in via Chain(...): the first
// middleware passed wraps the handler last (i.e., it sees the request first).
// Canonical order for paid routes:
//
//	Recover -> RequestID -> Headers -> Payment -> handler
//
// Recover catches panics, RequestID propagates correlation, Headers validates
// Livepeer-* request headers, Payment manages the OpenSession/Debit/Reconcile
// /CloseSession lifecycle around the handler.
package middleware

import "net/http"

// Middleware decorates an http.Handler.
type Middleware func(http.Handler) http.Handler

// Chain composes middlewares into a single decorator that wraps from outside
// in: Chain(A, B, C)(handler) === A(B(C(handler))).
func Chain(mw ...Middleware) Middleware {
	return func(h http.Handler) http.Handler {
		for i := len(mw) - 1; i >= 0; i-- {
			h = mw[i](h)
		}
		return h
	}
}
