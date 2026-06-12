package web

import "github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/diff"

type pageView struct {
	Title           string
	ActivePage      string
	ContentTemplate string
	Actor           string
	SignerAddress   string
	AssetVersion    string
}

type overviewPageView struct {
	pageView
	ProtocolConfigured  bool
	ProtocolReachable   bool
	HasCandidate        bool
	CandidateLoadedAt   string
	CandidateSource     string
	CandidateSeq        uint64
	CandidateEthAddress string
	CandidateCanonHash  string
	LastSignedPath      string
	HasLastSigned       bool
	LastSignedSummary   envelopeSummary
	CycleStage          string
	CycleTitle          string
	CycleNote           string
	CycleEvents         []cycleEventView
	ReconcileSteps      []checkpointStepView
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
	TreasuryProposal       *treasuryProposalView
	OperationalConfig      *operationalConfigView
	OperationalConfigError string
}

// treasuryProposalView is the pre-vote safety snapshot shown before a
// CastVote: proposal state, voting window, whether the daemon wallet has
// already voted, and its voting power at the snapshot.
type treasuryProposalView struct {
	Query  string
	Result *treasuryProposalResultView
	Error  string
}

type treasuryProposalResultView struct {
	Rows [][2]string
}

// operationalConfigView mirrors protocol.OperationalConfig for the config
// form (string amounts, hex addresses).
type operationalConfigView struct {
	RoundInitEnabled      bool
	RewardEnabled         bool
	RewardBeforeTransfer  bool
	TransferBondEnabled   bool
	TransferBondReceiver  string
	TransferBondMinRetain string
	WithdrawFeesEnabled   bool
	WithdrawFeesReceiver  string
	WithdrawFeesThreshold string
}

type manifestsPageView struct {
	pageView
	LastSignedPath    string
	HasLastSigned     bool
	LastSignedSummary envelopeSummary
	Held              *heldItemView
	Candidate         *candidateView
	CandidateError    string
	ReviewState       string
	ReviewTitle       string
	ReviewMessage     string
	CycleStage        string
	CycleTitle        string
	CycleNote         string
	CycleEvents       []cycleEventView
	ReconcileSteps    []checkpointStepView
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
	AuthEnabled  bool
	Error        string
	AssetVersion string
}

// heldItemView renders the agent's held-for-operator candidate
// (plan 0042 §8 "Pending changes").
type heldItemView struct {
	ETag           string
	Class          string
	HeldAt         string
	PublicationSeq uint64
	CanonHash      string
	WouldAutoSign  bool
	Findings       []heldFindingView
}

type heldFindingView struct {
	Class  string
	Code   string
	Tuple  string
	Detail string
}

type candidateView struct {
	LoadedAt       string
	SourceName     string
	PublicationSeq uint64
	EthAddress     string
	CanonHash      string
	Diff           *diff.Result
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

type cycleEventView struct {
	Anchor string
	Kind   string
	At     string
	Actor  string
	Note   string
}

type checkpointStepView struct {
	Label  string
	Status string
	Note   string
	Href   string
}
