package server

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/payment"
)

const maxTicketParamsBodyBytes = 8 << 10

type ticketParamsRequestJSON struct {
	SenderETHAddress    string `json:"sender_eth_address"`
	RecipientETHAddress string `json:"recipient_eth_address"`
	FaceValueWei        string `json:"face_value_wei"`
	Capability          string `json:"capability"`
	Offering            string `json:"offering"`
}

type ticketParamsResponseJSON struct {
	TicketParams ticketParamsJSON `json:"ticket_params"`
}

type ticketParamsJSON struct {
	Recipient         string                     `json:"recipient"`
	FaceValue         string                     `json:"face_value"`
	WinProb           string                     `json:"win_prob"`
	RecipientRandHash string                     `json:"recipient_rand_hash"`
	Seed              string                     `json:"seed"`
	ExpirationBlock   string                     `json:"expiration_block"`
	ExpirationParams  ticketExpirationParamsJSON `json:"expiration_params"`
}

type ticketExpirationParamsJSON struct {
	CreationRound          int64  `json:"creation_round"`
	CreationRoundBlockHash string `json:"creation_round_block_hash"`
}

func ticketParamsHandler(client payment.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()

		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTicketParamsBodyBytes))
		dec.DisallowUnknownFields()

		var req ticketParamsRequestJSON
		if err := dec.Decode(&req); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := ensureSingleJSONDocument(dec); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		daemonReq, err := parseTicketParamsRequest(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		params, err := client.GetTicketParams(r.Context(), daemonReq)
		if err != nil {
			http.Error(w, "get ticket params: "+err.Error(), http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ticketParamsResponseJSON{
			TicketParams: renderTicketParamsJSON(params),
		})
	}
}

func ensureSingleJSONDocument(dec *json.Decoder) error {
	var tail struct{}
	if err := dec.Decode(&tail); err == nil {
		return fmt.Errorf("request body must contain exactly one JSON object")
	} else if err == io.EOF {
		return nil
	} else {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
}

func parseTicketParamsRequest(in ticketParamsRequestJSON) (payment.GetTicketParamsRequest, error) {
	sender, err := parseHexAddress("sender_eth_address", in.SenderETHAddress)
	if err != nil {
		return payment.GetTicketParamsRequest{}, err
	}
	recipient, err := parseHexAddress("recipient_eth_address", in.RecipientETHAddress)
	if err != nil {
		return payment.GetTicketParamsRequest{}, err
	}
	faceValue, ok := new(big.Int).SetString(strings.TrimSpace(in.FaceValueWei), 10)
	if !ok {
		return payment.GetTicketParamsRequest{}, fmt.Errorf("face_value_wei must be a decimal integer")
	}
	if faceValue.Sign() <= 0 {
		return payment.GetTicketParamsRequest{}, fmt.Errorf("face_value_wei must be > 0")
	}
	if strings.TrimSpace(in.Capability) == "" {
		return payment.GetTicketParamsRequest{}, fmt.Errorf("capability is required")
	}
	if strings.TrimSpace(in.Offering) == "" {
		return payment.GetTicketParamsRequest{}, fmt.Errorf("offering is required")
	}
	return payment.GetTicketParamsRequest{
		Sender:     sender,
		Recipient:  recipient,
		FaceValue:  faceValue,
		Capability: strings.TrimSpace(in.Capability),
		Offering:   strings.TrimSpace(in.Offering),
	}, nil
}

func parseHexAddress(field, raw string) ([]byte, error) {
	s := strings.TrimSpace(raw)
	if len(s) != 42 || !strings.HasPrefix(strings.ToLower(s), "0x") {
		return nil, fmt.Errorf("%s must be a 0x-prefixed 20-byte hex address", field)
	}
	out, err := hex.DecodeString(s[2:])
	if err != nil {
		return nil, fmt.Errorf("%s must be hex: %w", field, err)
	}
	if len(out) != 20 {
		return nil, fmt.Errorf("%s must be 20 bytes", field)
	}
	return out, nil
}

func renderTicketParamsJSON(in *payment.TicketParams) ticketParamsJSON {
	out := ticketParamsJSON{
		Recipient:         "0x" + hex.EncodeToString(in.Recipient),
		FaceValue:         decimalString(in.FaceValue),
		WinProb:           decimalString(in.WinProb),
		RecipientRandHash: "0x" + hex.EncodeToString(in.RecipientRandHash),
		Seed:              "0x" + hex.EncodeToString(in.Seed),
		ExpirationBlock:   decimalString(in.ExpirationBlock),
	}
	if in.ExpirationParams != nil {
		out.ExpirationParams = ticketExpirationParamsJSON{
			CreationRound:          in.ExpirationParams.CreationRound,
			CreationRoundBlockHash: "0x" + hex.EncodeToString(in.ExpirationParams.CreationRoundBlockHash),
		}
	}
	return out
}

func decimalString(n *big.Int) string {
	if n == nil {
		return "0"
	}
	return n.String()
}
