package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/payment"
)

func TestTicketParamsHandler_HappyPath(t *testing.T) {
	reqBody := ticketParamsRequestJSON{
		SenderETHAddress:    "0x1111111111111111111111111111111111111111",
		RecipientETHAddress: "0x2222222222222222222222222222222222222222",
		FaceValueWei:        "1000",
		Capability:          "openai:chat-completions",
		Offering:            "model-a",
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/payment/ticket-params", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	ticketParamsHandler(payment.NewMock()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}

	var out ticketParamsResponseJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.TicketParams.Recipient != reqBody.RecipientETHAddress {
		t.Fatalf("recipient = %s; want %s", out.TicketParams.Recipient, reqBody.RecipientETHAddress)
	}
	if out.TicketParams.FaceValue != reqBody.FaceValueWei {
		t.Fatalf("face_value = %s; want %s", out.TicketParams.FaceValue, reqBody.FaceValueWei)
	}
	if out.TicketParams.RecipientRandHash == "" || out.TicketParams.RecipientRandHash == "0x" {
		t.Fatalf("recipient_rand_hash should be populated")
	}
}

func TestTicketParamsHandler_InvalidBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/payment/ticket-params", bytes.NewBufferString(`{"sender_eth_address":"x"}`))
	rec := httptest.NewRecorder()

	ticketParamsHandler(payment.NewMock()).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want %d", rec.Code, http.StatusBadRequest)
	}
}
