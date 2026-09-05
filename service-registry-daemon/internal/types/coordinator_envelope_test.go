package types

import "testing"

// The estimator must survive the envelope→manifest projection.
//
// It is the same class of bug as the dropped capability `extra`: a field
// parsed at one end, modelled nowhere in the middle, and silently absent
// at the other. Here the consequence is a consumer that cannot compute a
// funding ceiling for a multipart upload and has to guess one — and a
// guessed reservation and the seller's bill are two different numbers.
func TestCoordinatorEnvelopeCarriesWorkUnitEstimator(t *testing.T) {
	wu := CoordinatorWorkUnit{
		Name: "seconds",
		Estimator: &CoordinatorEstimator{
			ID:        "multipart-audio-duration/v1",
			Rounding:  "ceil-to-whole-seconds",
			Exactness: "exact-or-reject",
			Package:   "example.invalid/some-client-lib", // optional field; pass-through only
			Fixtures:  "livepeer-network-protocol/extractors/fixtures/multipart-audio-duration-v1",
		},
	}
	got := cloneEstimator(wu.Estimator)
	if got == nil {
		t.Fatal("estimator dropped in projection")
	}
	if got.ID != "multipart-audio-duration/v1" {
		t.Fatalf("id = %q", got.ID)
	}
	if got.Rounding != "ceil-to-whole-seconds" || got.Exactness != "exact-or-reject" {
		t.Fatalf("rounding/exactness lost: %+v", got)
	}
	if got.Package == "" || got.Fixtures == "" {
		t.Fatal("a consumer told to reproduce a measurement needs the implementation and " +
			"the fixtures that pin it")
	}
	// A capability without one stays without one: most ceilings come
	// from the caller's own request and advertising an estimator there
	// would invite a client to reproduce something it does not need to.
	if cloneEstimator(nil) != nil {
		t.Fatal("nil estimator became non-nil")
	}
}
