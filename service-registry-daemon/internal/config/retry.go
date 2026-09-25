package config

import (
	"fmt"
	"strings"
	"time"
)

// Durations is an operator-configurable ascending retry schedule (flag.Value).
type Durations []time.Duration

func (d *Durations) String() string {
	parts := make([]string, len(*d))
	for i, v := range *d {
		parts[i] = v.String()
	}
	return strings.Join(parts, ",")
}
func (d *Durations) Set(raw string) error {
	var out Durations
	for _, part := range strings.Split(raw, ",") {
		v, err := time.ParseDuration(strings.TrimSpace(part))
		if err != nil {
			return err
		}
		out = append(out, v)
	}
	if err := validateSchedule(out); err != nil {
		return err
	}
	*d = out
	return nil
}
func validateSchedule(d Durations) error {
	if len(d) == 0 || len(d) > 32 {
		return fmt.Errorf("retry schedule requires 1–32 intervals")
	}
	for i, v := range d {
		if v <= 0 || v > 7*24*time.Hour || (i > 0 && v < d[i-1]) {
			return fmt.Errorf("retry intervals must be positive, nondecreasing and at most 168h")
		}
	}
	return nil
}

type RetryPolicy struct {
	Incompatible       Durations
	Compatible         Durations
	Unknown            Durations
	Rejected           Durations
	Jitter             float64
	SourcePollInterval time.Duration
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		Incompatible: Durations{5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour, 24 * time.Hour},
		Compatible:   Durations{5 * time.Second, 15 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute},
		Unknown:      Durations{15 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour},
		Rejected:     Durations{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour},
		Jitter:       .2, SourcePollInterval: time.Minute,
	}
}
func (r RetryPolicy) Validate() error {
	for _, d := range []Durations{r.Incompatible, r.Compatible, r.Unknown, r.Rejected} {
		if err := validateSchedule(d); err != nil {
			return err
		}
	}
	if !(r.Jitter >= 0 && r.Jitter <= .5) {
		return fmt.Errorf("retry jitter must be between 0 and 0.5")
	}
	if r.SourcePollInterval <= 0 {
		return fmt.Errorf("source poll interval must be positive")
	}
	return nil
}
