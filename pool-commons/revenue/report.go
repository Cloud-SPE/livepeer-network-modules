// Package revenue defines the scoped receiver-source reporting envelope.
package revenue

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
)

type Report struct {
	PoolID               string    `json:"pool_id"`
	SourceID             string    `json:"source_id"` // immutable receiver settlement domain
	BrokerID             string    `json:"broker_id"` // receiving service resource
	ChainID              uint64    `json:"chain_id"`
	Payee                string    `json:"payee"`
	Round                int64     `json:"round"`
	RevenueWei           string    `json:"revenue_wei"`
	TicketCount          uint64    `json:"ticket_count"`
	Complete             bool      `json:"complete"`
	IncompleteReason     string    `json:"incomplete_reason,omitempty"`
	ObservedHead         uint64    `json:"observed_head"`
	ObservedHeadHash     string    `json:"observed_head_hash"`
	FinalizedBlock       uint64    `json:"finalized_block"`
	FinalizedBlockHash   string    `json:"finalized_block_hash"`
	CompleteThroughRound int64     `json:"complete_through_round"`
	ObservedAt           time.Time `json:"observed_at"`
	InclusionDigest      string    `json:"inclusion_digest"`
}

type Source struct {
	PoolID    string `json:"pool_id" yaml:"pool_id"`
	SourceID  string `json:"source_id" yaml:"source_id"`
	BrokerID  string `json:"broker_id" yaml:"broker_id"`
	ChainID   uint64 `json:"chain_id" yaml:"chain_id"`
	Payee     string `json:"payee" yaml:"payee"`
	URL       string `json:"url" yaml:"url"`
	TokenFile string `json:"-" yaml:"token_file"`
	CAFile    string `json:"-" yaml:"ca_file,omitempty"`
}

func (s Source) Validate() error {
	if s.PoolID == "" || s.BrokerID == "" || s.ChainID == 0 || !hexSize(s.SourceID, 32, true) || !hexSize(s.Payee, 20, true) {
		return fmt.Errorf("invalid revenue source identity")
	}
	u, err := url.Parse(s.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("revenue source requires an HTTPS origin")
	}
	return nil
}

func (r Report) Validate(source Source, round int64, now time.Time) error {
	if source.PoolID == "" || source.BrokerID == "" || source.ChainID == 0 || !hexSize(source.SourceID, 32, true) || !hexSize(source.Payee, 20, true) {
		return fmt.Errorf("invalid configured source identity")
	}
	if r.PoolID != source.PoolID || r.SourceID != source.SourceID || r.BrokerID != source.BrokerID || r.ChainID != source.ChainID || !strings.EqualFold(r.Payee, source.Payee) || r.Round != round {
		return fmt.Errorf("source report identity mismatch")
	}
	if !r.Complete || r.CompleteThroughRound < round {
		return fmt.Errorf("source incomplete: %s", r.IncompleteReason)
	}
	if r.ObservedAt.IsZero() || now.Sub(r.ObservedAt) > 2*time.Minute || r.ObservedAt.After(now.Add(time.Minute)) {
		return fmt.Errorf("source report stale")
	}
	amount, ok := new(big.Int).SetString(r.RevenueWei, 10)
	if !ok || amount.Sign() < 0 || r.ObservedHead < r.FinalizedBlock || !hexSize(r.InclusionDigest, 32, false) || !hexSize(r.FinalizedBlockHash, 32, true) || !hexSize(r.ObservedHeadHash, 32, true) {
		return fmt.Errorf("source report evidence invalid")
	}
	return nil
}

func Collect(ctx context.Context, source Source, round int64) (Report, error) {
	var result Report
	client, err := serviceauth.HTTPSClientWithCAFile(source.URL, source.PoolID, source.TokenFile, source.CAFile)
	if err != nil {
		return result, err
	}
	client.Timeout = 90 * time.Second
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(source.URL, "/")+"/reporting/v1/revenue/"+strconv.FormatInt(round, 10), nil)
	if err != nil {
		return result, err
	}
	response, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return result, fmt.Errorf("source %s returned HTTP %d", source.SourceID, response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return result, fmt.Errorf("invalid trailing report data")
	}
	return result, result.Validate(source, round, time.Now())
}

func hexSize(value string, n int, prefix bool) bool {
	if prefix {
		if !strings.HasPrefix(value, "0x") {
			return false
		}
		value = strings.TrimPrefix(value, "0x")
	}
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != n {
		return false
	}
	for _, b := range raw {
		if b != 0 {
			return true
		}
	}
	return false
}
