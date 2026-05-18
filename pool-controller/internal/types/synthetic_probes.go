package types

import "time"

type SyntheticProbeObservation struct {
	MemberEthAddress string
	BackendID        string
	CapabilityID     string
	OfferingID       string
	Success          bool
	Result           string
	ObservedAt       time.Time
}
