package web

import "github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/diff"

type indexView struct {
	SignerAddress     string
	LastSignedPath    string
	HasLastSigned     bool
	LastSignedSummary envelopeSummary
	Candidate         *candidateView
	CandidateError    string
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
