package config

import (
	"reflect"
	"testing"
)

func TestParseRPCURLs(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    []string
		wantErr string
	}{
		{"empty", "", nil, ""},
		{"whitespace only", "  ", nil, ""},
		{"single", "https://a", []string{"https://a"}, ""},
		{"list trims", " https://a , https://b ", []string{"https://a", "https://b"}, ""},
		{"blank middle entry", "https://a,,https://b", nil, "--chain-rpc-urls: entry 2 is empty"},
		{"trailing comma", "https://a,", nil, "--chain-rpc-urls: entry 2 is empty"},
		{"leading comma", ",https://a", nil, "--chain-rpc-urls: entry 1 is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRPCURLs(tc.in)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
