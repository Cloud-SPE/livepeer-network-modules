package main

import (
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
	"net/http"
)

type servicePrincipalKey struct{}

func servicePrincipal(r *http.Request) serviceauth.Credential {
	principal, _ := r.Context().Value(servicePrincipalKey{}).(serviceauth.Credential)
	return principal
}

// Default deny: a new endpoint is operator-only until its exact method/path is
// deliberately assigned to a machine role. Reporting never grants approval.
func controllerServiceRoles(r *http.Request) []string {
	roles := []string{"pool-admin"}
	switch r.Method + " " + r.URL.Path {
	case "GET /admin/v1/backend-selection-snapshot":
		return append(roles, "broker")
	case "POST /admin/v1/work-receipts", "POST /admin/v1/backend-outcomes":
		return append(roles, "broker")
	case "GET /admin/v1/regional-accounting", "GET /admin/v1/work-receipts", "GET /admin/v1/round-receipts", "GET /admin/v1/settlement-windows", "GET /admin/v1/regional-terms", "GET /admin/v1/revenue-sources":
		return append(roles, "reconciler")
	case "POST /admin/v1/round-close", "POST /admin/v1/settlement-windows/close":
		return append(roles, "reconciler")
	case "GET /admin/v1/payout-alerts", "POST /admin/v1/payout-intents/requeue", "GET /admin/v1/payout-intents", "POST /admin/v1/payout-intents/claim", "POST /admin/v1/payout-intents/renew", "POST /admin/v1/payout-intents/release", "POST /admin/v1/payout-intents/status":
		return append(roles, "payout-executor")
	}
	return roles
}
