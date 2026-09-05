package config

import (
	"strings"
	"testing"
)

// The extra{} grammar below is keyed off the capability id and is now
// reached through validateOffers -> validateExtraGrammar, so these
// cases are expressed as offers.
//
// CAVEAT: the "required" half of that grammar (extra.openai,
// extra.provider, extra.audio/video/vtuber) is in direct conflict with
// the offer grammar itself, which sources model identity from the
// runner's attach document via match{} and merges it into the
// advertised extra only at freeze time. The offer cases in
// offers_test.go and examples/host-config.offers.example.yaml assert
// the opposite and currently fail. See the handoff note — the fix
// belongs in offers.go, not here.

// offerWithExtra builds a minimal valid job offer carrying the
// operator-authored extra{} block under test.
func offerWithExtra(capabilityID string, extra map[string]any) Offer {
	return Offer{
		OfferingID: "default",
		Capability: capabilityID,
		Protocol:   "paid-job/v1",
		Price:      Price{AmountWei: "1", PerUnits: 1},
		Extra:      extra,
	}
}

func TestValidateAdminAuth(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("openai:chat-completions", map[string]any{
		"openai":   map[string]any{"model": "llama-3-70b"},
		"provider": "vllm",
	}))
	cfg.AdminAuth = AuthConfig{Method: "bearer", SecretRef: "env://BROKER_ADMIN_TOKEN"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	// Only env:// is resolvable by the broker; anything else would load
	// clean and then fail to authenticate at run time.
	cfg.AdminAuth.SecretRef = "vault://not-supported-here"
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "admin_auth.secret_ref must use env://") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateDefaultsPoolSnapshotPollingConfig(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("openai:chat-completions", map[string]any{
		"openai":   map[string]any{"model": "llama-3-70b"},
		"provider": "vllm",
	}))
	cfg.PoolSnapshot = PoolSnapshot{URL: "http://pool-controller:8080"}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.PoolSnapshot.TimeoutMS != 1500 {
		t.Fatalf("pool_snapshot.timeout_ms = %d; want 1500", cfg.PoolSnapshot.TimeoutMS)
	}
	if cfg.PoolSnapshot.PollIntervalMS != 5000 {
		t.Fatalf("pool_snapshot.poll_interval_ms = %d; want 5000", cfg.PoolSnapshot.PollIntervalMS)
	}
	if cfg.PoolSnapshot.StaleAfterMS != 15000 {
		t.Fatalf("pool_snapshot.stale_after_ms = %d; want 15000", cfg.PoolSnapshot.StaleAfterMS)
	}
	if cfg.PoolSnapshot.ExpireAfterMS != 60000 {
		t.Fatalf("pool_snapshot.expire_after_ms = %d; want 60000", cfg.PoolSnapshot.ExpireAfterMS)
	}
}

// Tuning knobs without a url are a config the operator believes is
// polling and which silently polls nothing.
func TestValidateRejectsPoolSnapshotWithoutURL(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("openai:chat-completions", map[string]any{
		"openai":   map[string]any{"model": "llama-3-70b"},
		"provider": "vllm",
	}))
	cfg.PoolSnapshot = PoolSnapshot{TimeoutMS: 1000}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "pool_snapshot.url is required") {
		t.Fatalf("Validate() error = %v", err)
	}
}

// The model belongs in extra.openai.model, never appended to the
// capability id: a gateway matches on the id, so the deprecated form
// splits one capability into an unmatchable family of ids.
func TestValidateRejectsDeprecatedOpenAICapabilityIDSyntax(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("openai:chat-completions:llama-3-70b", map[string]any{
		"openai":   map[string]any{"model": "llama-3-70b"},
		"provider": "vllm",
	}))

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want deprecated capability syntax rejection")
	}
	if !strings.Contains(err.Error(), "deprecated model-in-id syntax") {
		t.Fatalf("Validate() error = %v", err)
	}
}

// An offer names what it sells and what it costs. Which model serves it
// is a fact the runner declares at attach and the offer selects on, so
// an offer with no extra.openai is not incomplete — it is the normal
// case, and requiring it would mean the operator had to restate the one
// thing the runner exists to tell it.
func TestValidateAllowsOpenAIOfferWithoutOpenAIExtra(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("openai:chat-completions", map[string]any{
		"provider": "vllm",
	}))

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

// Same reason: the model arrives with the runner, not with the offer.
func TestValidateAllowsOpenAIOfferWithoutModel(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("openai:chat-completions", map[string]any{
		"openai":   map[string]any{},
		"provider": "vllm",
	}))

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidateAllowsOpenAIOfferWithoutProvider(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("openai:chat-completions", map[string]any{
		"openai": map[string]any{"model": "llama-3-70b"},
	}))

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidateRejectsNonMapOpenAIExtra(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("openai:chat-completions", map[string]any{
		"openai":   "llama-3-70b",
		"provider": "vllm",
	}))

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "extra.openai must be a map") {
		t.Fatalf("Validate() error = %v", err)
	}
}

// Feature flags are advertised verbatim; a string "true" reaches the
// gateway as a truthy-looking value it cannot reason about.
func TestValidateRejectsNonBooleanFeatureFlags(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("openai:chat-completions", map[string]any{
		"openai":   map[string]any{"model": "llama-3-70b"},
		"provider": "vllm",
		"features": map[string]any{"streaming": "true"},
	}))

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "extra.features.streaming must be a boolean") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsOpenAIExtraShape(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("openai:chat-completions", map[string]any{
		"openai":            map[string]any{"model": "llama-3-70b"},
		"provider":          "vllm",
		"served_model_name": "Llama 3 70B",
		"backend_model":     "meta-llama/Llama-3-70b",
		"features": map[string]any{
			"streaming": true,
			"tools":     true,
			"json_mode": true,
		},
	}))

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsReceiptSink(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("openai:chat-completions", map[string]any{
		"openai":   map[string]any{"model": "llama-3-70b"},
		"provider": "vllm",
	}))
	cfg.ReceiptSink = ReceiptSink{
		URL:       "http://pool-controller:8080",
		Auth:      AuthConfig{Method: "bearer", SecretRef: "env://POOL_CONTROLLER_TOKEN"},
		TimeoutMS: 1500,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsReceiptSinkBearerWithoutSecret(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("openai:chat-completions", map[string]any{
		"openai":   map[string]any{"model": "llama-3-70b"},
		"provider": "vllm",
	}))
	cfg.ReceiptSink = ReceiptSink{
		URL:  "http://pool-controller:8080",
		Auth: AuthConfig{Method: "bearer"},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "receipt_sink.auth.secret_ref is required") {
		t.Fatalf("Validate() error = %v", err)
	}
}

// The task is a runner-declared fact the offer selects on, so an
// offer that omits extra.audio is complete as written.
func TestValidateAllowsAudioOfferWithoutAudioExtra(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("openai:audio-transcriptions", map[string]any{
		"openai":   map[string]any{"model": "whisper-large-v3"},
		"provider": "openai-audio-runner",
	}))

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

// The task is pinned by the capability id; a mismatch means the offer
// advertises one kind of work and the runner is asked for another.
func TestValidateRejectsAudioCapabilityWithWrongTask(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("openai:audio-speech", map[string]any{
		"openai":   map[string]any{"model": "kokoro"},
		"provider": "openai-tts-runner",
		"audio":    map[string]any{"task": "transcription"},
	}))

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "extra.audio.task") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsAudioExtraShape(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("openai:audio-speech", map[string]any{
		"openai":   map[string]any{"model": "kokoro"},
		"provider": "openai-tts-runner",
		"audio":    map[string]any{"task": "speech"},
	}))

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

// The task is a runner-declared fact the offer selects on, so an
// offer that omits extra.video is complete as written.
func TestValidateAllowsVideoOfferWithoutVideoExtra(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("video:transcode.vod", map[string]any{
		"provider": "transcode-runner",
	}))

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidateRejectsVideoCapabilityWithWrongTask(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("video:transcode.abr", map[string]any{
		"provider": "abr-runner",
		"video":    map[string]any{"task": "transcode"},
	}))

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "extra.video.task") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsVideoExtraShape(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("video:transcode.abr", map[string]any{
		"provider": "abr-runner",
		"video":    map[string]any{"task": "abr-transcode"},
	}))

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

// The task is a runner-declared fact the offer selects on, so an
// offer that omits extra.vtuber is complete as written.
func TestValidateAllowsVTuberOfferWithoutVTuberExtra(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("livepeer:vtuber-session", map[string]any{
		"provider": "vtuber-runner",
	}))

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidateRejectsVTuberCapabilityWithWrongTask(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("livepeer:vtuber-session", map[string]any{
		"provider": "vtuber-runner",
		"vtuber":   map[string]any{"task": "avatar"},
	}))

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "extra.vtuber.task") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsVTuberExtraShape(t *testing.T) {
	cfg := baseOfferConfig(offerWithExtra("livepeer:vtuber-session", map[string]any{
		"provider": "vtuber-runner",
		"vtuber":   map[string]any{"task": "session"},
	}))

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
