package main

import (
	"time"
)

// errorInjector computes the effective failure probability for a given moment,
// allowing demos to simulate static error rates, linear drift, or brief spikes.
type errorInjector struct {
	startTime time.Time

	rateStart   float64
	rateEnd     float64
	rampSeconds float64

	spikeAtSeconds       float64
	spikeDurationSeconds float64
	spikeRate            float64
}

func newErrorInjector(cfg errorInjectorConfig) *errorInjector {
	return &errorInjector{
		startTime:            time.Now(),
		rateStart:            cfg.RateStart,
		rateEnd:              cfg.RateEnd,
		rampSeconds:          cfg.RampSeconds,
		spikeAtSeconds:       cfg.SpikeAtSeconds,
		spikeDurationSeconds: cfg.SpikeDurationSeconds,
		spikeRate:            cfg.SpikeRate,
	}
}

type errorInjectorConfig struct {
	RateStart            float64
	RateEnd              float64
	RampSeconds          float64
	SpikeAtSeconds       float64
	SpikeDurationSeconds float64
	SpikeRate            float64
}

// currentRate returns the failure probability at time t given the injector
// configuration. The ordering is: spike window > linear ramp > static rate.
func (e *errorInjector) currentRate(t time.Time) float64 {
	elapsed := t.Sub(e.startTime).Seconds()

	if e.spikeAtSeconds > 0 && e.spikeDurationSeconds > 0 {
		spikeEnd := e.spikeAtSeconds + e.spikeDurationSeconds
		if elapsed >= e.spikeAtSeconds && elapsed < spikeEnd {
			return e.spikeRate
		}
	}

	if e.rampSeconds <= 0 {
		return e.rateStart
	}

	progress := elapsed / e.rampSeconds
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	return e.rateStart + (e.rateEnd-e.rateStart)*progress
}
