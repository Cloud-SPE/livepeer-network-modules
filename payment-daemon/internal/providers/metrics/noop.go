package metrics

import (
	"net/http"
	"time"
)

// Noop is the default Recorder when --metrics-listen is unset. Every
// method is a zero-cost no-op. Handler returns 404 so a misconfigured
// scrape target is obvious rather than silently empty.
type Noop struct{}

// NewNoop returns the no-op Recorder.
func NewNoop() *Noop { return &Noop{} }

func (*Noop) IncGRPCRequest(string, string, string)     {}
func (*Noop) ObserveGRPC(string, string, time.Duration) {}
func (*Noop) SetGRPCInFlight(string, string, int)       {}

func (*Noop) IncSessionEvent(string)      {}
func (*Noop) IncTicket(string)            {}
func (*Noop) IncTicketRejected(string)    {}
func (*Noop) IncWinningTicket()           {}
func (*Noop) AddCreditedEVGwei(float64)   {}
func (*Noop) IncDebit(string)             {}
func (*Noop) AddWorkUnitsDebited(float64) {}

func (*Noop) IncRedemption(string)            {}
func (*Noop) ObserveRedemption(time.Duration) {}
func (*Noop) SetRedemptionQueueDepth(int)     {}
func (*Noop) IncRedemptionTx(string)          {}
func (*Noop) SetGasPriceWei(float64)          {}
func (*Noop) SetCurrentRound(int64)           {}

func (*Noop) SetEscrowPendingFloatWei(float64) {}
func (*Noop) SetTrackedSenders(int)            {}
func (*Noop) IncEscrowRebuild()                {}

func (*Noop) IncChainRead(string, string)            {}
func (*Noop) ObserveChainRead(string, time.Duration) {}
func (*Noop) IncChainWrite(string, string)           {}
func (*Noop) SetChainLastSuccess(time.Time)          {}

func (*Noop) IncPaymentCreated(string)               {}
func (*Noop) IncTicketSigned()                       {}
func (*Noop) IncTicketParamsFetch(string)            {}
func (*Noop) ObserveTicketParamsFetch(time.Duration) {}
func (*Noop) SetSenderSessions(int)                  {}
func (*Noop) SetSenderDepositWei(float64)            {}
func (*Noop) SetSenderReserveWei(float64)            {}

func (*Noop) SetUptimeSeconds(float64)            {}
func (*Noop) SetBuildInfo(string, string, string) {}

// Handler returns a 404 handler — there are no metrics to serve.
func (*Noop) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "metrics disabled (start the daemon with --metrics-listen)", http.StatusNotFound)
	})
}

// Compile-time interface check.
var _ Recorder = (*Noop)(nil)
