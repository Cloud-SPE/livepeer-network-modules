package config

import (
	"reflect"
	"testing"
)

// A pool's broker fleet can be stated three ways, and BrokerTargets is
// the single place that reconciles them — so every caller pushes to the
// same list whichever way the operator wrote it down.
func TestBrokerTargets(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bootstrap Bootstrap
		want      []Broker
	}{
		{
			// The explicit list supersedes the single-broker keys; a
			// pool that wrote both meant the list.
			name: "explicit list wins over the legacy single URL",
			bootstrap: Bootstrap{
				BrokerAdminURL: "https://legacy.example",
				Brokers: []Broker{
					{Name: "eu", AdminURL: "https://eu.example", TimeoutMS: 2000},
					{Name: "us", AdminURL: "https://us.example"},
				},
			},
			want: []Broker{
				{Name: "eu", AdminURL: "https://eu.example", TimeoutMS: 2000},
				{Name: "us", AdminURL: "https://us.example"},
			},
		},
		{
			// The URL is the identity; the name only labels logs and
			// status, so it is never required of the operator.
			name:      "a nameless entry is named by its URL",
			bootstrap: Bootstrap{Brokers: []Broker{{AdminURL: "https://solo.example"}}},
			want:      []Broker{{Name: "https://solo.example", AdminURL: "https://solo.example"}},
		},
		{
			name:      "a blank name is filled in like a missing one",
			bootstrap: Bootstrap{Brokers: []Broker{{Name: "   ", AdminURL: "https://solo.example"}}},
			want:      []Broker{{Name: "https://solo.example", AdminURL: "https://solo.example"}},
		},
		{
			// An entry with no URL names no broker. Keeping it would
			// push at the empty string and report a failure the
			// operator cannot act on.
			name: "an entry with no admin URL is skipped",
			bootstrap: Bootstrap{Brokers: []Broker{
				{Name: "placeholder", AdminURL: "  "},
				{Name: "real", AdminURL: "https://real.example"},
			}},
			want: []Broker{{Name: "real", AdminURL: "https://real.example"}},
		},
		{
			// A dev deployment is one broker and should not have to
			// learn a list to start.
			name: "the legacy single-broker keys become a one-element fleet",
			bootstrap: Bootstrap{
				BrokerAdminURL:       "https://only.example",
				BrokerAdminAuth:      AuthConfig{Method: "bearer", SecretRef: "env:BROKER_TOKEN"},
				BrokerAdminTimeoutMS: 1500,
			},
			want: []Broker{{
				Name:      "https://only.example",
				AdminURL:  "https://only.example",
				Auth:      AuthConfig{Method: "bearer", SecretRef: "env:BROKER_TOKEN"},
				TimeoutMS: 1500,
			}},
		},
		{
			// Pushing nowhere is a valid standalone deployment, not a
			// misconfiguration, so this is nil rather than an error.
			name:      "neither form configured is an empty fleet",
			bootstrap: Bootstrap{},
			want:      nil,
		},
		{
			name:      "a blank legacy URL is not a broker",
			bootstrap: Bootstrap{BrokerAdminURL: "   "},
			want:      nil,
		},
		{
			// The list was written, and every entry was unusable. The
			// fallback must not fire here: the operator's intent was
			// the list, and silently pushing to the legacy URL instead
			// would send offers somewhere they did not ask for.
			name: "a list of only unusable entries does not fall back",
			bootstrap: Bootstrap{
				BrokerAdminURL: "https://legacy.example",
				Brokers:        []Broker{{Name: "placeholder"}},
			},
			want: []Broker{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.bootstrap.BrokerTargets()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("BrokerTargets() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
