package server

import (
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
)

func TestJobTransportsHoldCapacityUntilCompletion(t *testing.T) {
	for _, transport := range []string{"unary", "stream", "multipart"} {
		t.Run(transport, func(t *testing.T) {
			srv, s := newJobOfferBrokerBare(t, nil, "", func(c *config.Config) { c.Offers[0].Capacity.MaxInFlight = 1; c.Offers[0].Capacity.QueueLimit = 8 })
			var armed atomic.Bool
			entered := make(chan struct{}, 1)
			proceed := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(proceed) }) }
			defer unblock()
			attachJobRunnerResponder(t, s, srv, func(_, _ string, headers map[string][]string, _ []byte) (int, http.Header, []byte) {
				if armed.Load() {
					entered <- struct{}{}
					<-proceed
				}
				if strings.Contains(strings.Join(headers["Accept"], ","), "text/event-stream") {
					return 200, http.Header{"Content-Type": {"text/event-stream"}}, []byte("data: {\"usage\":{\"total_tokens\":1}}\n\ndata: [DONE]\n\n")
				}
				return 200, http.Header{"Content-Type": {"application/json"}}, []byte(`{"choices":[],"usage":{"total_tokens":1}}`)
			}, "unary", "stream", "multipart")
			armed.Store(true)
			request := func(id string) *http.Response {
				body := `{"model":"test-model","messages":[]}`
				if transport == "multipart" {
					body = "--x\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\ntest-model\r\n--x--\r\n"
				}
				req, _ := http.NewRequest("POST", srv.URL+"/v1/job", strings.NewReader(body))
				req.Header.Set(livepeerheader.Capability, "openai:chat-completions")
				req.Header.Set(livepeerheader.Offering, "default")
				req.Header.Set(livepeerheader.Protocol, "paid-job/v1")
				req.Header.Set(livepeerheader.RequestID, id)
				req.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString([]byte("stub-payment")))
				if transport == "stream" {
					req.Header.Set("Accept", "text/event-stream")
				}
				if transport == "multipart" {
					req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
				}
				setJobTestAuthorization(t, req, srv.URL)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Error(err)
					return nil
				}
				return resp
			}
			done := make(chan *http.Response, 1)
			go func() { done <- request("first") }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("job never dispatched")
			}
			if got := s.currentBackendInFlight("h1|chat"); got != 1 {
				t.Fatalf("in-flight=%d", got)
			}
			rejected := request("second")
			if rejected == nil {
				t.Fatal("request failed")
			}
			rejected.Body.Close()
			if rejected.StatusCode != 503 || rejected.Header.Get(livepeerheader.Backoff) == "" {
				t.Fatalf("full runner=%d headers=%v", rejected.StatusCode, rejected.Header)
			}
			unblock()
			resp := <-done
			if resp == nil {
				t.Fatal("job failed")
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != 200 {
				t.Fatalf("job=%d %s %v", resp.StatusCode, body, err)
			}
			if got := s.currentBackendInFlight("h1|chat"); got != 0 {
				t.Fatalf("finished occupancy=%d", got)
			}
		})
	}
}

func TestConcurrentJobAcquisitionUsesAllRunnersAndReleasesOnce(t *testing.T) {
	s := newRunnerSelectionServer(t, 1)
	group := selectionGroup(t, s)
	var wg sync.WaitGroup
	releases := make(chan func(), 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, release, err := s.reserveJobBackend(group)
			if err == nil {
				releases <- release
			}
		}()
	}
	wg.Wait()
	close(releases)
	if len(releases) != 2 {
		t.Fatalf("admitted=%d", len(releases))
	}
	for release := range releases {
		release()
		release()
	}
	c, release, err := s.reserveJobBackend(group)
	if err != nil {
		t.Fatal(err)
	}
	release()
	newer, ok := s.reserveBackend(c)
	if !ok {
		t.Fatal("released capacity not reusable")
	}
	release() // An old owner cannot decrement a new owner's slot.
	if s.currentBackendInFlight(c.Backend.ID) != 1 {
		t.Fatal("duplicate release stole new ownership")
	}
	newer()
}

func TestJobUsesOtherRunnerWhenSelectedSlotIsTaken(t *testing.T) {
	s := newRunnerSelectionServer(t, 1)
	group := selectionGroup(t, s)
	var competingRelease func()
	s.randIntn = func(int) int {
		var ok bool
		competingRelease, ok = s.reserveBackend(group.Backends[0])
		if !ok {
			t.Fatal("competing request could not take selected slot")
		}
		return 0
	}
	selected, release, err := s.reserveJobBackend(group)
	if competingRelease != nil {
		defer competingRelease()
	}
	if err != nil {
		t.Fatalf("rejected despite second runner being free: %v", err)
	}
	defer release()
	if selected.Backend.ID == group.Backends[0].Backend.ID {
		t.Fatal("dispatched to full runner")
	}
}
