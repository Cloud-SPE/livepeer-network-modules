package server

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

type accountQueryJSON struct {
	PayerETHAddress string `json:"payer_eth_address"`
	AuthorizationID string `json:"authorization_id,omitempty"`
}

// handlePaymentAccountFund accepts value but no spend authority. A payer or
// clearinghouse can therefore restore aggregate runway while the workload
// caller remains directly connected to a long-lived session.
func (s *Server) handlePaymentAccountFund(w http.ResponseWriter, r *http.Request) {
	ac, ok := s.payment.(payment.AccountClient)
	if !ok {
		http.Error(w, "wholesale account protocol is not supported", http.StatusNotImplemented)
		return
	}
	capability, offering := r.Header.Get(livepeerheader.Capability), r.Header.Get(livepeerheader.Offering)
	group, found := s.groupFor(capability, offering)
	if !found || group.Published == nil {
		livepeerheader.WriteError(w, http.StatusNotFound, livepeerheader.ErrCapabilityNotServed, "offering is not served")
		return
	}
	if !supportsWholesaleAccounts(group.Published) {
		livepeerheader.WriteError(w, http.StatusHTTPVersionNotSupported, livepeerheader.ErrProtocolUnsupported, "offering does not advertise wholesale account support")
		return
	}
	encoded := r.Header.Get(livepeerheader.Payment)
	paymentBytes, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(paymentBytes) == 0 {
		livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid, "Livepeer-Payment is missing or invalid")
		return
	}
	spec, _ := s.lookupSpec(capability, offering)
	if err := middleware.ValidateExpectedPriceForRequest(paymentBytes, capability, offering, spec); err != nil {
		livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentEnvelopeMismatch, "expected price mismatch: "+err.Error())
		return
	}
	workID, owned := payment.DerivePayeeWorkID(paymentBytes)
	if !owned {
		livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid, "payment has no payee-owned work id")
		return
	}
	if _, err := s.payment.OpenSession(r.Context(), payment.OpenSessionRequest{WorkID: workID, Capability: capability, Offering: offering, PricePerWorkUnitWei: spec.PricePerWorkUnitWei, PerUnits: spec.PerUnits, WorkUnit: spec.WorkUnit}); err != nil {
		livepeerheader.WriteError(w, http.StatusBadGateway, livepeerheader.ErrInternalError, "open funding generation: "+err.Error())
		return
	}
	result, err := ac.FundWholesaleAccount(r.Context(), paymentBytes)
	if err != nil || result == nil || result.Account == nil {
		if err == nil {
			err = fmt.Errorf("empty funding result")
		}
		livepeerheader.WriteError(w, http.StatusPaymentRequired, livepeerheader.ErrPaymentInvalid, "fund wholesale account: "+err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"payer": "0x" + hex.EncodeToString(result.Account.Payer), "payee": "0x" + hex.EncodeToString(result.Account.Payee),
		"credited_value_wei": decimalString(result.Credited), "available_value_wei": decimalString(result.Account.Available),
		"account_version": result.Account.Version, "replayed": result.Replayed,
	})
}

func paymentAccountHandler(client payment.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ac, ok := client.(payment.AccountClient)
		if !ok {
			http.Error(w, "wholesale account protocol is not supported", http.StatusNotImplemented)
			return
		}
		var in accountQueryJSON
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTicketParamsBodyBytes))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		payer, err := parseHexAddress("payer_eth_address", in.PayerETHAddress)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if in.AuthorizationID != "" {
			status, err := ac.GetSpendAuthorization(r.Context(), payer, in.AuthorizationID)
			if err != nil {
				http.Error(w, "get spend authorization: "+err.Error(), http.StatusBadGateway)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"payer": "0x" + hex.EncodeToString(payer), "authorization_id": in.AuthorizationID, "state": authorizationStateJSON(status.State), "reserved_value_wei": decimalString(status.Reserved), "billed_value_wei": decimalString(status.Billed), "released_value_wei": decimalString(status.Released), "actual_units": status.ActualUnits, "settlement_seq": status.SettlementSeq, "observed_at": status.ObservedAt})
			return
		}
		account, err := ac.GetWholesaleAccount(r.Context(), payer)
		if err != nil {
			http.Error(w, "get wholesale account: "+err.Error(), http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"payer": "0x" + hex.EncodeToString(account.Payer), "payee": "0x" + hex.EncodeToString(account.Payee), "chain_id": account.ChainID, "denomination": account.Denomination, "credited_value_wei": decimalString(account.Credited), "reserved_value_wei": decimalString(account.Reserved), "debited_value_wei": decimalString(account.Debited), "available_value_wei": decimalString(account.Available), "version": account.Version, "observed_at": account.ObservedAt})
	}
}

func authorizationStateJSON(state int32) string {
	switch pb.SpendAuthorizationState(state) {
	case pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ISSUED:
		return "issued"
	case pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED:
		return "admitted"
	case pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED:
		return "settled"
	case pb.SpendAuthorizationState_SPEND_AUTHORIZATION_EXPIRED_UNUSED:
		return "expired_unused"
	case pb.SpendAuthorizationState_SPEND_AUTHORIZATION_OUTCOME_UNKNOWN:
		return "outcome_unknown"
	case pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SUPERSEDED:
		return "superseded"
	default:
		return "unspecified"
	}
}
