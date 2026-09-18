package config

import (
	"strings"
	"testing"
)

func TestRegionalFleetValidation(t *testing.T) {
	b := Bootstrap{MemberAgentImage: "example/agent@sha256:" + strings.Repeat("a", 64), Brokers: []Broker{{PublicURL: "https://transcode"}, {PublicURL: "https://audio"}, {PublicURL: "https://llm"}}}
	if err := validateRegionalFleet(b); err != nil || len(b.PublicBrokerURLs()) != 3 {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Bootstrap){func(b *Bootstrap) { b.MemberAgentImage = "agent:latest" }, func(b *Bootstrap) { b.Brokers[0].PublicURL = "" }, func(b *Bootstrap) { b.Brokers[0].PublicURL = "http://transcode" }, func(b *Bootstrap) { b.Brokers[0].PublicURL = "https://audio" }, func(b *Bootstrap) { b.PublicBrokerQUICAddr = "a:8443" }} {
		copy := b
		copy.Brokers = append([]Broker(nil), b.Brokers...)
		mutate(&copy)
		if validateRegionalFleet(copy) == nil {
			t.Fatalf("accepted %+v", copy)
		}
	}
}
