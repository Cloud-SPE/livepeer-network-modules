package member

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// Member identity can inspect/manage its own enrollment, but is deliberately
// never used for agent desired-state application or stopped-execution reports.
func authorizeMemberEnrollment(deps Deps, r *http.Request) (types.HostEnrollment, bool) {
	if enrollment, ok := authorizeEnrollment(deps, r); ok {
		return enrollment, true
	}
	wallet, ok := memberIDFromRequest(deps.Sessions, r)
	if !ok {
		return types.HostEnrollment{}, false
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		origin, err := url.Parse(r.Header.Get("Origin"))
		if err != nil || origin.Host != r.Host || (origin.Scheme != "https" && origin.Scheme != "http") {
			return types.HostEnrollment{}, false
		}
		member, err := deps.Repo.GetPoolMember(wallet)
		if err != nil || member.Status != types.MemberStatusActive {
			return types.HostEnrollment{}, false
		}
	}
	enrollment, err := deps.Repo.GetHostEnrollment(r.PathValue("id"))
	if err != nil || !strings.EqualFold(enrollment.MemberEthAddress, wallet) {
		return types.HostEnrollment{}, false
	}
	return enrollment, true
}
