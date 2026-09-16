package member

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/desiredstate"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestAgentApplyResultsSurviveRestartAndReadIsScoped(t *testing.T) {
	dir := t.TempDir()
	st, err := repo.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	host := types.HostEnrollment{ID: "host", MemberEthAddress: "0x" + strings.Repeat("1", 40)}
	if err := st.PutHostEnrollment(host); err != nil {
		t.Fatal(err)
	}
	if err := st.PutHardwareUnit(types.HardwareUnit{ID: "gpu", EnrollmentID: "host", MemberEthAddress: host.MemberEthAddress, GPUUUID: "gpu-own"}); err != nil {
		t.Fatal(err)
	}
	assignment := types.TemplateAssignment{ID: "assignment", HostEnrollmentID: "host", HardwareUnitID: "gpu", TemplateID: "template", State: types.TemplateAssignmentPending}
	if err := st.PutTemplateAssignment(assignment); err != nil {
		t.Fatal(err)
	}
	d := Deps{Repo: st}
	if _, err := d.applyStatusReport(host, statusReport{Revision: "revision", Services: []serviceStatus{{Name: desiredstate.ServiceName(assignment.ID), Status: "failed", Detail: "GPU driver unavailable"}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = repo.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d.Repo = st
	view, err := d.hostStatus(host)
	if err != nil || view.Apply == nil || len(view.Apply.Services) != 1 || view.Apply.Services[0].Detail != "GPU driver unavailable" {
		t.Fatal(view, err)
	}
	mux := http.NewServeMux()
	Register(mux, d)
	r := httptest.NewRequest("GET", "https://region.example/member/v1/enrollments/host/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("unauthenticated apply report", w.Code)
	}
	raw, _ := json.Marshal(view)
	if !strings.Contains(string(raw), "GPU driver unavailable") {
		t.Fatal("apply detail lost")
	}
}
