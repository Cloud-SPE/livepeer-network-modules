package policy

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/diff"
)

// Class grades a candidate against the last-signed manifest (plan
// 0042 §7). Ordering matters: a candidate's class is the highest
// class any single change hits.
type Class int

const (
	// ClassNoOp — content identical and the last-signed manifest
	// retains enough validity; nothing to do (§6 step 5). This is
	// what stops the publish→seq-advance→new-ETag loop from
	// re-signing forever.
	ClassNoOp Class = iota
	// ClassRenewal — content identical, but remaining validity is
	// below the renewal threshold; fresh window needs a signature.
	ClassRenewal
	// ClassBenign — every change fits the operator-authored bounds.
	ClassBenign
	// ClassCritical — at least one change exceeds the bounds; hold
	// for a discrete operator action.
	ClassCritical
	// ClassForbidden — identity (eth_address) changed; the candidate
	// is rejected outright, never held as signable. A signing key can
	// only ever sign for one orchestrator, so a changed address is
	// somebody else's manifest.
	ClassForbidden
)

func (c Class) String() string {
	switch c {
	case ClassNoOp:
		return "no_op"
	case ClassRenewal:
		return "renewal"
	case ClassBenign:
		return "benign"
	case ClassCritical:
		return "critical"
	case ClassForbidden:
		return "forbidden"
	default:
		return fmt.Sprintf("class(%d)", int(c))
	}
}

// Finding is one graded change, kept for the audit record and the
// held-queue classification report.
type Finding struct {
	Class        Class  `json:"-"`
	ClassName    string `json:"class"`
	Code         string `json:"code"`
	CapabilityID string `json:"capability_id,omitempty"`
	OfferingID   string `json:"offering_id,omitempty"`
	Detail       string `json:"detail"`
}

// Finding codes. Stable strings — they land in audit records.
const (
	CodeTupleAdded          = "tuple_added"
	CodeTupleRemoved        = "tuple_removed"
	CodePriceWithinBound    = "price_change_within_bound"
	CodePriceBeyondBound    = "price_change_beyond_bound"
	CodePriceUnparseable    = "price_unparseable"
	CodeWorkerURLAllowed    = "worker_url_within_allowlist"
	CodeWorkerURLDisallowed = "worker_url_outside_allowlist"
	CodeExtraChanged        = "extra_changed"
	CodeConstraintsChanged  = "constraints_changed"
	CodeProtocolChanged     = "protocol_changed"
	CodeJobAxesChanged      = "job_axes_changed"
	CodeSessionAxesChanged  = "session_axes_changed"
	CodeWorkUnitChanged     = "work_unit_changed"
	CodeUnknownFieldChanged = "unknown_field_changed"
	CodeEthAddressChanged   = "eth_address_changed"
	CodeSpecVersionChanged  = "spec_version_changed"
	CodeRenewalDue          = "renewal_due"
	CodeNoOp                = "no_op"
	CodeFirstSign           = "first_sign_cycle"
)

// Classification is the graded outcome.
type Classification struct {
	Class    Class
	Findings []Finding
}

// ClassifyInput carries the non-diff facts classification needs.
type ClassifyInput struct {
	Bounds BenignBounds
	// RemainingValidity is last-signed expires_at − now. Used only
	// when content is identical, to split no_op from renewal.
	RemainingValidity time.Duration
	// RenewalThreshold is RenewalThresholdFraction × manifest TTL,
	// resolved by the caller.
	RenewalThreshold time.Duration
	// FirstSign is true when there is no last-signed manifest yet.
	// The whole candidate is new content; it grades critical (hold
	// for the operator) — bootstrap is a human decision.
	FirstSign bool
}

// Classify grades a structural diff. Pure: same inputs, same grade.
func Classify(d *diff.Result, in ClassifyInput) Classification {
	if in.FirstSign {
		return Classification{Class: ClassCritical, Findings: []Finding{
			finding(ClassCritical, CodeFirstSign, "", "", "no last-signed manifest; bootstrap sign is an operator action"),
		}}
	}

	var findings []Finding

	// Forbidden header changes trump everything.
	if !d.Header.EthAddressStable {
		findings = append(findings, finding(ClassForbidden, CodeEthAddressChanged, "", "",
			fmt.Sprintf("orch.eth_address %q → %q", d.Header.BeforeEthAddress, d.Header.AfterEthAddress)))
	}
	if !d.Header.SpecVersionStable {
		// Critical, not forbidden (plan 0043 §3.7). The spec version now
		// has one source — the protocol module's VERSION, which the
		// broker stamps and the coordinator imports — so it changes when
		// the operator upgrades, which is a real thing they do. Refusing
		// it outright meant an upgrade could never be signed at all;
		// holding it for a deliberate gesture is the honest grade. The
		// console requires the new version to be typed before signing.
		findings = append(findings, finding(ClassCritical, CodeSpecVersionChanged, "", "",
			fmt.Sprintf("spec_version %q → %q", d.Header.BeforeSpecVersion, d.Header.AfterSpecVersion)))
	}

	for _, t := range d.Added {
		findings = append(findings, finding(ClassCritical, CodeTupleAdded, t.CapabilityID, t.OfferingID,
			"new (capability_id, offering_id) tuple"))
	}
	for _, t := range d.Removed {
		class, detail := ClassCritical, "tuple removed; allow_tuple_removal is false"
		if in.Bounds.AllowTupleRemoval {
			class, detail = ClassBenign, "tuple removed; removal reduces exposure"
		}
		findings = append(findings, finding(class, CodeTupleRemoved, t.CapabilityID, t.OfferingID, detail))
	}
	for _, t := range d.Changed {
		findings = append(findings, classifyChangedTuple(t, in.Bounds)...)
	}

	if len(findings) == 0 {
		// Content identical. Window/seq advances are not content.
		if in.RemainingValidity < in.RenewalThreshold {
			return Classification{Class: ClassRenewal, Findings: []Finding{
				finding(ClassRenewal, CodeRenewalDue, "", "",
					fmt.Sprintf("content identical; remaining validity %s below threshold %s", in.RemainingValidity, in.RenewalThreshold)),
			}}
		}
		return Classification{Class: ClassNoOp, Findings: []Finding{
			finding(ClassNoOp, CodeNoOp, "", "", "content identical; last-signed retains validity"),
		}}
	}

	highest := ClassNoOp
	for _, f := range findings {
		if f.Class > highest {
			highest = f.Class
		}
	}
	return Classification{Class: highest, Findings: findings}
}

func classifyChangedTuple(t diff.ChangedTuple, bounds BenignBounds) []Finding {
	var out []Finding
	keys := map[string]bool{}
	for k := range t.Before {
		keys[k] = true
	}
	for k := range t.After {
		keys[k] = true
	}
	for k := range keys {
		if jsonEqual(t.Before[k], t.After[k]) {
			continue
		}
		switch k {
		case "price_per_unit_wei":
			out = append(out, classifyPrice(t, bounds))
		case "worker_url":
			out = append(out, classifyWorkerURL(t, bounds))
		case "extra":
			out = append(out, finding(ClassCritical, CodeExtraChanged, t.CapabilityID, t.OfferingID, "extra changed"))
		case "constraints":
			out = append(out, finding(ClassCritical, CodeConstraintsChanged, t.CapabilityID, t.OfferingID, "constraints changed"))
		// protocol / job / session are the offering's declaration
		// (livepeer-network-protocol/protocols/offering-axes.md). They
		// replaced the single interaction-mode string, and they
		// inherit its grade: critical, every one of them.
		//
		// The declaration is what counterparties gate on.
		// session.descriptor_schema decides which gateways may open a
		// session at all; session.refill decides whether the
		// clearinghouse honours a top-up; session.metering decides who
		// counts the money; session.attachment decides whether the
		// data plane transits the broker; job.transports decides what
		// a broker accepts before payment. A silent edit to any of
		// them changes who can buy and how they are billed — exactly
		// the class of change the sign cycle exists to put in front of
		// a human. Splitting one mode string into a protocol tag plus
		// an axes object must not become a way to auto-sign the same
		// semantics.
		//
		// The advisory axes (heartbeat, lease, tolerance_band_pct,
		// runway_increment_units) ride in the same object and are
		// graded with it. Grading them benign would need
		// operator-authored bounds that BenignBounds does not have,
		// and absent a bound this classifier cannot call a change
		// benign — the same rule that sends unknown fields critical.
		case "protocol":
			out = append(out, finding(ClassCritical, CodeProtocolChanged, t.CapabilityID, t.OfferingID,
				fmt.Sprintf("protocol %v → %v", t.Before[k], t.After[k])))
		case "job":
			out = append(out, finding(ClassCritical, CodeJobAxesChanged, t.CapabilityID, t.OfferingID,
				axesDetail("job", t.Before[k], t.After[k])))
		case "session":
			out = append(out, finding(ClassCritical, CodeSessionAxesChanged, t.CapabilityID, t.OfferingID,
				axesDetail("session", t.Before[k], t.After[k])))
		case "work_unit":
			out = append(out, finding(ClassCritical, CodeWorkUnitChanged, t.CapabilityID, t.OfferingID, "work_unit changed"))
		default:
			// A field this classifier does not know is a field it
			// cannot grade benign.
			out = append(out, finding(ClassCritical, CodeUnknownFieldChanged, t.CapabilityID, t.OfferingID,
				fmt.Sprintf("field %q changed", k)))
		}
	}
	return out
}

// axesDetail names the axes that moved, so the held-queue entry tells
// the operator which knob turned without dumping both objects into the
// audit record. It reads key names only — never the values' meaning.
func axesDetail(field string, before, after any) string {
	b, okB := before.(map[string]any)
	a, okA := after.(map[string]any)
	if !okB || !okA {
		// One side absent (or not an object): the offering changed
		// shape, e.g. a paid-job offering became a paid-session one.
		return fmt.Sprintf("%s axes %s", field, presence(okB, okA))
	}
	seen := map[string]bool{}
	var keys []string
	for k := range b {
		seen[k] = true
		keys = append(keys, k)
	}
	for k := range a {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var moved []string
	for _, k := range keys {
		if !jsonEqual(b[k], a[k]) {
			moved = append(moved, k)
		}
	}
	if len(moved) == 0 {
		return fmt.Sprintf("%s axes changed", field)
	}
	return fmt.Sprintf("%s axes changed: %s", field, strings.Join(moved, ", "))
}

func presence(before, after bool) string {
	switch {
	case !before && after:
		return "declared"
	case before && !after:
		return "removed"
	default:
		return "changed"
	}
}

// classifyPrice bounds price changes in either direction by
// price_delta_max_pct (Q1 proposal: decreases are bounded too).
func classifyPrice(t diff.ChangedTuple, bounds BenignBounds) Finding {
	beforeStr, _ := t.Before["price_per_unit_wei"].(string)
	afterStr, _ := t.After["price_per_unit_wei"].(string)
	before, okB := new(big.Int).SetString(beforeStr, 10)
	after, okA := new(big.Int).SetString(afterStr, 10)
	if !okB || !okA || before.Sign() <= 0 || after.Sign() < 0 {
		return finding(ClassCritical, CodePriceUnparseable, t.CapabilityID, t.OfferingID,
			fmt.Sprintf("price %q → %q not comparable", beforeStr, afterStr))
	}
	delta := new(big.Int).Sub(after, before)
	delta.Abs(delta)
	// |Δ| / before <= pct / 100, in rationals — wei magnitudes
	// overflow nothing here and floats never touch the comparison.
	deltaRat := new(big.Rat).SetFrac(delta, before)
	boundRat := new(big.Rat).SetFloat64(bounds.PriceDeltaMaxPct / 100)
	if boundRat == nil {
		boundRat = new(big.Rat)
	}
	detail := fmt.Sprintf("price %s → %s (bound ±%v%%)", beforeStr, afterStr, bounds.PriceDeltaMaxPct)
	if deltaRat.Cmp(boundRat) <= 0 {
		return finding(ClassBenign, CodePriceWithinBound, t.CapabilityID, t.OfferingID, detail)
	}
	return finding(ClassCritical, CodePriceBeyondBound, t.CapabilityID, t.OfferingID, detail)
}

func classifyWorkerURL(t diff.ChangedTuple, bounds BenignBounds) Finding {
	afterURL, _ := t.After["worker_url"].(string)
	detail := fmt.Sprintf("worker_url %v → %v", t.Before["worker_url"], t.After["worker_url"])
	if hostAllowed(afterURL, bounds.WorkerURLDomainAllowlist) {
		return finding(ClassBenign, CodeWorkerURLAllowed, t.CapabilityID, t.OfferingID, detail)
	}
	return finding(ClassCritical, CodeWorkerURLDisallowed, t.CapabilityID, t.OfferingID, detail)
}

// hostAllowed reports whether the URL's host matches the explicit
// suffix list: equal to an entry, or ending in "."+entry. No
// wildcards (Q2 proposal).
func hostAllowed(rawURL string, allowlist []string) bool {
	if len(allowlist) == 0 {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	for _, entry := range allowlist {
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}

func finding(class Class, code, capID, offID, detail string) Finding {
	return Finding{Class: class, ClassName: class.String(), Code: code, CapabilityID: capID, OfferingID: offID, Detail: detail}
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

// Action is what the agent loop does with a classified candidate.
type Action int

const (
	// ActionSkip — record no_op, do nothing.
	ActionSkip Action = iota
	// ActionAutoSign — sign and push without an operator.
	ActionAutoSign
	// ActionHold — persist to the held queue, alert, wait for the
	// operator.
	ActionHold
	// ActionRefuse — forbidden-class; reject the candidate outright
	// and alert. Never enters the held queue as signable.
	ActionRefuse
)

func (a Action) String() string {
	switch a {
	case ActionSkip:
		return "skip"
	case ActionAutoSign:
		return "auto_sign"
	case ActionHold:
		return "hold"
	case ActionRefuse:
		return "refuse"
	default:
		return fmt.Sprintf("action(%d)", int(a))
	}
}

// Decision maps a classification through the policy dials.
type Decision struct {
	Class  Class
	Action Action
	// ShadowAutoSign marks a benign candidate held only because the
	// phase-1 dial (auto_sign.benign=false) says so; the agent
	// records a would_auto_sign shadow audit event for burn-in
	// calibration (§10).
	ShadowAutoSign bool
	Findings       []Finding
}

// Decide applies the policy dials to a classification. It does not
// consult the rate limiter or kill switches — the agent loop checks
// those before acting on ActionAutoSign.
func Decide(c Classification, p Policy) Decision {
	d := Decision{Class: c.Class, Findings: c.Findings}
	switch c.Class {
	case ClassNoOp:
		d.Action = ActionSkip
	case ClassRenewal:
		if p.AutoSign.Renewal {
			d.Action = ActionAutoSign
		} else {
			d.Action = ActionHold
		}
	case ClassBenign:
		if p.AutoSign.Benign {
			d.Action = ActionAutoSign
		} else {
			d.Action = ActionHold
			d.ShadowAutoSign = true
		}
	case ClassCritical:
		d.Action = ActionHold
	case ClassForbidden:
		d.Action = ActionRefuse
	default:
		// Unknown classes hold — fail closed.
		d.Action = ActionHold
	}
	return d
}
