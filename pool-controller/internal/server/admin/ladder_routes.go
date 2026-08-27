package admin

import (
	"net/http"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/ladder"
)

func registerLadderRoutes(mux *http.ServeMux, deps Deps, auth func(http.HandlerFunc) http.HandlerFunc) {
	// Running the ladder is idempotent — it seeds what is missing and
	// moves only what the evidence justifies — so an operator may run
	// it by hand without waiting for the worker's tick.
	mux.HandleFunc("POST /admin/v1/ladder/run", auth(func(w http.ResponseWriter, _ *http.Request) {
		if deps.Ladder == nil {
			http.Error(w, "ladder is not configured", http.StatusInternalServerError)
			return
		}
		summary, err := deps.Ladder.RunOnce()
		writeAdminJSON(w, summary, err)
	}))
}

// LadderRunner is the ladder as the admin surface needs it.
type LadderRunner interface {
	RunOnce() (ladder.Summary, error)
}

var _ LadderRunner = (*ladder.Service)(nil)
