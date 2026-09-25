package server

import (
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

func TestSessionRevisionRemainingDebitHTTPAndWS(t *testing.T) {
	var fixture struct {
		Price    string `json:"price_wei"`
		Max      uint64 `json:"successor_max_units"`
		Used     uint64 `json:"cumulative_units"`
		Runway   int64  `json:"requested_runway_units"`
		Reserved string `json:"expected_reservation_wei"`
		Billed   string `json:"expected_final_billed_wei"`
	}
	raw, err := os.ReadFile("../../../livepeer-network-protocol/conformance/fixtures/session-revision-recovery.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, transport := range []string{"http", "websocket"} {
		t.Run(transport, func(t *testing.T) {
			runner := &fakeSessionRunner{}
			srv, s := newSessionTestServerConfigured(t, runner.handler(), func(c *config.Config) {
				c.Offers[0].Price.AmountWei = fixture.Price
				c.Offers[0].SessionPolicy = &config.SessionPolicy{MinRunwayUnits: fixture.Runway}
			})
			prepare := func(path, body, id, previous string) *http.Request {
				req, _ := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
				req.Header.Set(livepeerheader.Capability, "meet:sfu-room")
				req.Header.Set(livepeerheader.Offering, "default")
				req.Header.Set(livepeerheader.Protocol, "paid-session/v1")
				req.Header.Set(livepeerheader.RequestID, id)
				setSessionTestAuthorization(t, req, "gws-1", previous)
				wire, _ := base64.StdEncoding.DecodeString(req.Header.Get(livepeerheader.Authorization))
				var auth pb.SpendAuthorization
				if err := proto.Unmarshal(wire, &auth); err != nil {
					t.Fatal(err)
				}
				price, _ := new(big.Int).SetString(fixture.Price, 10)
				auth.Payload.AcceptedPrice.PricePerUnitWei = &pb.BigUInt{Value: price.Bytes()}
				auth.Payload.MaxTotalUnits = fixture.Max
				auth.Payload.MaxDebitWei = &pb.BigUInt{Value: new(big.Int).Mul(price, new(big.Int).SetUint64(fixture.Max)).Bytes()}
				wire, err := proto.Marshal(&auth)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set(livepeerheader.Authorization, base64.StdEncoding.EncodeToString(wire))
				return req
			}
			response, err := http.DefaultClient.Do(prepare("/v1/session", `{"gateway_session_id":"gws-1","session_params":{}}`, "initial", ""))
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusCreated {
				t.Fatalf("open: %d %v", response.StatusCode, decode(t, response))
			}
			opened := decode(t, response)
			id := opened["session_id"].(string)
			credential := opened["credential"].(string)
			event, _ := json.Marshal(map[string]any{"event_id": "usage", "sequence": 1, "event_type": "session.usage.tick", "usage": map[string]any{"unit": "participant_minutes", "total": fixture.Used}})
			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/session/"+id+"/events", strings.NewReader(string(event)))
			req.Header.Set("Authorization", "Bearer "+runner.callbackToken)
			response, err = http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK {
				t.Fatalf("usage: %d %v", response.StatusCode, decode(t, response))
			}
			response.Body.Close()
			topup := prepare("/v1/session/"+id+"/topup", "", "revision", "auth-initial")
			topup.Header.Set("Authorization", "Bearer "+credential)
			if transport == "http" {
				response, err = http.DefaultClient.Do(topup)
				if err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != http.StatusOK {
					t.Fatalf("topup: %d %v", response.StatusCode, decode(t, response))
				}
				response.Body.Close()
			} else {
				conn, _ := wsDial(t, srv.URL, id, credential)
				if conn == nil {
					t.Fatal("websocket upgrade failed")
				}
				defer conn.Close()
				if err = conn.WriteJSON(map[string]any{"type": "session.topup", "body": map[string]any{"request_id": "revision", "authorization_header": topup.Header.Get(livepeerheader.Authorization)}}); err != nil {
					t.Fatal(err)
				}
				if frame := wsRead(t, conn); frame["type"] != "ack" {
					t.Fatalf("topup: %v", frame)
				}
			}
			rec, err := s.sessionStore.Get(id)
			if err != nil || rec.AuthorizationReservedWei != fixture.Reserved || rec.BilledWei != fixture.Billed || rec.AccountAuthorizationID != "auth-revision" {
				t.Fatalf("reserve=%s billed=%s authority=%s err=%v", rec.AuthorizationReservedWei, rec.BilledWei, rec.AccountAuthorizationID, err)
			}
		})
	}
}
