package web

import "github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/diff"

type pageView struct {
	Title           string
	ActivePage      string
	ContentTemplate string
	Actor           string
	SignerAddress   string
}

type protocolStatusPageView struct {
	pageView
	ProtocolStatus *protocolStatusView
}

type protocolActionsPageView struct {
	pageView
	ProtocolStatus         *protocolStatusView
	ProtocolActionFeedback *protocolActionFeedbackView
	TxIntentLookup         *txIntentLookupView
}

type manifestsPageView struct {
	pageView
	LastSignedPath    string
	HasLastSigned     bool
	LastSignedSummary envelopeSummary
	Candidate         *candidateView
	CandidateError    string
}

type auditPageView struct {
	pageView
	AuditPath    string
	AuditEvents  []auditEventView
	AuditError   string
	HasOlder     bool
	NextCursor   string
	IsPaginated  bool
	NewestPath   string
	OlderPath    string
	CurrentCount int
}

type loginView struct {
	AuthEnabled bool
	Error       string
}

type candidateView struct {
	LoadedAt   string
	SourceName string
	CanonHash  string
	Diff       *diff.Result
}

type envelopeSummary struct {
	PublicationSeq  uint64
	EthAddress      string
	IssuedAt        string
	ExpiresAt       string
	CapabilityCount int
	Error           string
}

type protocolStatusView struct {
	Health            protocolFieldView
	Round             protocolFieldView
	Reward            protocolFieldView
	ServiceRegistry   protocolFieldView
	AIServiceRegistry protocolFieldView
	Wallet            protocolFieldView
	ConfirmAddress    string
}

type protocolActionFeedbackView struct {
	Action  string
	Result  string
	Message string
}

type protocolFieldView struct {
	Title         string
	Available     bool
	Unimplemented bool
	Error         string
	Rows          [][2]string
}

type txIntentLookupView struct {
	Query  string
	Result *txIntentResultView
	Error  string
}

type txIntentResultView struct {
	Rows [][2]string
}

type auditEventView struct {
	At             string
	Kind           string
	Actor          string
	EthAddress     string
	PublicationSeq string
	CanonHash      string
	Note           string
	Fields         string
}
