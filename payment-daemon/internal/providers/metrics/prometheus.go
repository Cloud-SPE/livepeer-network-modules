package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "livepeer_payment"

// Prometheus is the production Recorder. All metrics live in a single
// dedicated registry we own — never the package-global default — so
// noisy consumer libs don't pollute the daemon's exposition output.
type Prometheus struct {
	reg *prometheus.Registry

	// gRPC
	grpcRequests *prometheus.CounterVec
	grpcDuration *prometheus.HistogramVec
	grpcInFlight *prometheus.GaugeVec

	// Receiver domain
	sessionEvents    *prometheus.CounterVec
	tickets          *prometheus.CounterVec
	ticketsRejected  *prometheus.CounterVec
	winningTickets   prometheus.Counter
	creditedEVGwei   prometheus.Counter
	debits           *prometheus.CounterVec
	workUnitsDebited prometheus.Counter

	// Settlement
	redemptions     *prometheus.CounterVec
	redemptionDur   prometheus.Histogram
	redemptionQueue prometheus.Gauge
	redemptionTx    *prometheus.CounterVec
	gasPriceWei     prometheus.Gauge
	currentRound    prometheus.Gauge

	// Escrow
	escrowPendingFloat prometheus.Gauge
	trackedSenders     prometheus.Gauge
	escrowRebuilds     prometheus.Counter

	// Chain provider
	chainReads       *prometheus.CounterVec
	chainReadDur     *prometheus.HistogramVec
	chainWrites      *prometheus.CounterVec
	chainLastSuccess prometheus.Gauge

	// Sender domain
	paymentsCreated   *prometheus.CounterVec
	ticketsSigned     prometheus.Counter
	ticketParamsFetch *prometheus.CounterVec
	ticketParamsDur   prometheus.Histogram
	senderSessions    prometheus.Gauge
	senderDeposit     prometheus.Gauge
	senderReserve     prometheus.Gauge

	// Daemon-level
	uptimeSeconds prometheus.Gauge
	buildInfo     *prometheus.GaugeVec

	// chain-commons series, registered by name on first emission.
	dyn dynamicVecs
}

// NewPrometheus builds the production Recorder. It installs the standard
// Go runtime + process collectors so /metrics surfaces go_* and process_*
// for free.
func NewPrometheus() *Prometheus {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	p := &Prometheus{reg: reg, dyn: dynamicVecs{
		counters:   map[string]*dynamicCounter{},
		gauges:     map[string]*dynamicGauge{},
		histograms: map[string]*dynamicHistogram{},
	}}

	counterVec := func(name, help string, labels ...string) *prometheus.CounterVec {
		v := prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: namespace, Name: name, Help: help}, labels)
		reg.MustRegister(v)
		return v
	}
	counter := func(name, help string) prometheus.Counter {
		c := prometheus.NewCounter(prometheus.CounterOpts{Namespace: namespace, Name: name, Help: help})
		reg.MustRegister(c)
		return c
	}
	gauge := func(name, help string) prometheus.Gauge {
		g := prometheus.NewGauge(prometheus.GaugeOpts{Namespace: namespace, Name: name, Help: help})
		reg.MustRegister(g)
		return g
	}
	gaugeVec := func(name, help string, labels ...string) *prometheus.GaugeVec {
		v := prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: namespace, Name: name, Help: help}, labels)
		reg.MustRegister(v)
		return v
	}
	histVec := func(name, help string, buckets []float64, labels ...string) *prometheus.HistogramVec {
		v := prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: namespace, Name: name, Help: help, Buckets: buckets}, labels)
		reg.MustRegister(v)
		return v
	}
	hist := func(name, help string, buckets []float64) prometheus.Histogram {
		h := prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: namespace, Name: name, Help: help, Buckets: buckets})
		reg.MustRegister(h)
		return h
	}

	// gRPC
	p.grpcRequests = counterVec("grpc_requests_total", "Total completed gRPC requests, labeled by role, method, and status code.", "role", "method", "code")
	p.grpcDuration = histVec("grpc_request_duration_seconds", "Unary gRPC handler latency, labeled by role and method.", prometheus.DefBuckets, "role", "method")
	p.grpcInFlight = gaugeVec("grpc_in_flight_requests", "In-flight gRPC requests, labeled by role and method.", "role", "method")

	// Receiver domain
	p.sessionEvents = counterVec("sessions_total", "Session lifecycle transitions, labeled by event.", "event")
	p.tickets = counterVec("tickets_total", "Processed tickets, labeled by result (accepted/rejected).", "result")
	p.ticketsRejected = counterVec("tickets_rejected_total", "Rejected tickets, labeled by reason.", "reason")
	p.winningTickets = counter("winning_tickets_total", "Winning tickets queued for redemption.")
	p.creditedEVGwei = counter("credited_ev_gwei_total", "Cumulative credited expected value in gwei.")
	p.debits = counterVec("debits_total", "DebitBalance calls, labeled by result.", "result")
	p.workUnitsDebited = counter("work_units_debited_total", "Cumulative work units debited.")

	// Settlement
	p.redemptions = counterVec("redemptions_total", "Redemption attempt outcomes, labeled by result.", "result")
	p.redemptionDur = hist("redemption_duration_seconds", "Redemption attempt latency.", prometheus.DefBuckets)
	p.redemptionQueue = gauge("redemption_queue_depth", "Pending winning-ticket redemptions queued locally.")
	p.redemptionTx = counterVec("redemption_tx_total", "Redemption transaction phases, labeled by result.", "result")
	p.gasPriceWei = gauge("gas_price_wei", "Most-recent gas price (wei) observed by the settlement loop.")
	p.currentRound = gauge("current_round", "Last-initialized Livepeer protocol round seen by the daemon.")

	// Escrow
	p.escrowPendingFloat = gauge("escrow_pending_float_wei", "Total pending escrow float across all tracked senders (wei).")
	p.trackedSenders = gauge("tracked_senders", "Number of senders escrow is currently tracking.")
	p.escrowRebuilds = counter("escrow_rebuilds_total", "Escrow rebuilds from the persisted store.")

	// Chain provider
	p.chainReads = counterVec("chain_reads_total", "Chain read calls, labeled by method and result.", "method", "result")
	p.chainReadDur = histVec("chain_read_duration_seconds", "Chain read latency, labeled by method.", prometheus.DefBuckets, "method")
	p.chainWrites = counterVec("chain_writes_total", "Chain write calls, labeled by method and result.", "method", "result")
	p.chainLastSuccess = gauge("chain_last_success_timestamp_seconds", "Unix timestamp of the most recent successful chain interaction.")

	// Sender domain
	p.paymentsCreated = counterVec("payments_created_total", "CreatePayment outcomes, labeled by result.", "result")
	p.ticketsSigned = counter("tickets_signed_total", "Tickets signed by the sender.")
	p.ticketParamsFetch = counterVec("ticketparams_fetches_total", "Ticket-params HTTP fetches, labeled by result.", "result")
	p.ticketParamsDur = hist("ticketparams_fetch_duration_seconds", "Ticket-params HTTP fetch latency.", prometheus.DefBuckets)
	p.senderSessions = gauge("sender_sessions", "Cached sender sessions.")
	p.senderDeposit = gauge("sender_deposit_wei", "On-chain sender deposit (wei).")
	p.senderReserve = gauge("sender_reserve_wei", "On-chain sender reserve remaining (wei).")

	// Daemon-level
	p.uptimeSeconds = gauge("uptime_seconds", "Seconds since the daemon started.")
	p.buildInfo = gaugeVec("build_info", "Build metadata; value is always 1.", "version", "mode", "go_version")

	return p
}

// Registry returns the underlying registry (exposed for tests).
func (p *Prometheus) Registry() *prometheus.Registry { return p.reg }

// Handler returns a promhttp handler over the private registry.
func (p *Prometheus) Handler() http.Handler {
	return promhttp.HandlerFor(p.reg, promhttp.HandlerOpts{})
}

// ----- Recorder implementation -----

func (p *Prometheus) IncGRPCRequest(role, method, code string) {
	p.grpcRequests.WithLabelValues(unset(role), unset(method), unset(code)).Inc()
}
func (p *Prometheus) ObserveGRPC(role, method string, d time.Duration) {
	p.grpcDuration.WithLabelValues(unset(role), unset(method)).Observe(d.Seconds())
}
func (p *Prometheus) SetGRPCInFlight(role, method string, n int) {
	p.grpcInFlight.WithLabelValues(unset(role), unset(method)).Set(float64(n))
}

func (p *Prometheus) IncSessionEvent(event string) {
	p.sessionEvents.WithLabelValues(unset(event)).Inc()
}
func (p *Prometheus) IncTicket(result string) { p.tickets.WithLabelValues(unset(result)).Inc() }
func (p *Prometheus) IncTicketRejected(reason string) {
	p.ticketsRejected.WithLabelValues(unset(reason)).Inc()
}
func (p *Prometheus) IncWinningTicket() { p.winningTickets.Inc() }
func (p *Prometheus) AddCreditedEVGwei(gwei float64) {
	if gwei > 0 {
		p.creditedEVGwei.Add(gwei)
	}
}
func (p *Prometheus) IncDebit(result string) { p.debits.WithLabelValues(unset(result)).Inc() }
func (p *Prometheus) AddWorkUnitsDebited(units float64) {
	if units > 0 {
		p.workUnitsDebited.Add(units)
	}
}

func (p *Prometheus) IncRedemption(result string) { p.redemptions.WithLabelValues(unset(result)).Inc() }
func (p *Prometheus) ObserveRedemption(d time.Duration) {
	p.redemptionDur.Observe(d.Seconds())
}
func (p *Prometheus) SetRedemptionQueueDepth(n int) { p.redemptionQueue.Set(float64(n)) }
func (p *Prometheus) IncRedemptionTx(result string) {
	p.redemptionTx.WithLabelValues(unset(result)).Inc()
}
func (p *Prometheus) SetGasPriceWei(wei float64)  { p.gasPriceWei.Set(wei) }
func (p *Prometheus) SetCurrentRound(round int64) { p.currentRound.Set(float64(round)) }

func (p *Prometheus) SetEscrowPendingFloatWei(wei float64) { p.escrowPendingFloat.Set(wei) }
func (p *Prometheus) SetTrackedSenders(n int)              { p.trackedSenders.Set(float64(n)) }
func (p *Prometheus) IncEscrowRebuild()                    { p.escrowRebuilds.Inc() }

func (p *Prometheus) IncChainRead(method, result string) {
	p.chainReads.WithLabelValues(unset(method), unset(result)).Inc()
}
func (p *Prometheus) ObserveChainRead(method string, d time.Duration) {
	p.chainReadDur.WithLabelValues(unset(method)).Observe(d.Seconds())
}
func (p *Prometheus) IncChainWrite(method, result string) {
	p.chainWrites.WithLabelValues(unset(method), unset(result)).Inc()
}
func (p *Prometheus) SetChainLastSuccess(t time.Time) {
	p.chainLastSuccess.Set(float64(t.UTC().Unix()))
}

func (p *Prometheus) IncPaymentCreated(result string) {
	p.paymentsCreated.WithLabelValues(unset(result)).Inc()
}
func (p *Prometheus) IncTicketSigned() { p.ticketsSigned.Inc() }
func (p *Prometheus) IncTicketParamsFetch(result string) {
	p.ticketParamsFetch.WithLabelValues(unset(result)).Inc()
}
func (p *Prometheus) ObserveTicketParamsFetch(d time.Duration) {
	p.ticketParamsDur.Observe(d.Seconds())
}
func (p *Prometheus) SetSenderSessions(n int)         { p.senderSessions.Set(float64(n)) }
func (p *Prometheus) SetSenderDepositWei(wei float64) { p.senderDeposit.Set(wei) }
func (p *Prometheus) SetSenderReserveWei(wei float64) { p.senderReserve.Set(wei) }

func (p *Prometheus) SetUptimeSeconds(s float64) { p.uptimeSeconds.Set(s) }
func (p *Prometheus) SetBuildInfo(version, mode, goVersion string) {
	p.buildInfo.WithLabelValues(unset(version), unset(mode), unset(goVersion)).Set(1)
}

// Compile-time interface check.
var _ Recorder = (*Prometheus)(nil)
