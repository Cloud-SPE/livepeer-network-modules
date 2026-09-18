package server

import (
	"net/http"
	"strings"
)

func brokerServiceRoles(r *http.Request) []string {
	roles := []string{"pool-admin"}
	switch r.Method + " " + r.URL.Path {
	case "GET /admin/v1/runners", "GET /admin/v1/offers", "GET /admin/v1/certification":
		return append(roles, "controller", "coordinator")
	case "POST /admin/v1/devices/drain", "GET /admin/v1/terms-policy", "POST /admin/v1/terms-policy", "PUT /admin/v1/offers", "PUT /admin/v1/credentials", "POST /admin/v1/source/drain", "POST /admin/v1/source/freeze":
		return append(roles, "controller")
	}
	if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/admin/v1/offers/") && strings.HasSuffix(r.URL.Path, "/accept-shape") {
		return append(roles, "coordinator")
	}
	return roles
}
