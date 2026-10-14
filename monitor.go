package main

import "time"

const (
	autoQuietPeriod = 20 * time.Second
	autoRetryPeriod = time.Minute
)

// autoMonitor schedules work only; its caller serializes observations and completion.
type autoMonitor struct {
	quietPeriod      time.Duration
	interval         time.Duration
	lastSuccess      time.Time
	initialized      bool
	worldKey         string
	running          bool
	pending          bool
	inflight         bool
	quiet            bool
	quietSince       time.Time
	retryAfter       time.Time
	generation       uint64
	flightGeneration uint64
}

func (m *autoMonitor) observe(now time.Time, running bool, probeErr error, worldKey string) bool {
	if !m.initialized {
		m.initialized = true
		m.worldKey = worldKey
		m.pending = true
		m.generation++
	} else if worldKey != m.worldKey {
		m.worldKey = worldKey
		m.pending = true
		m.generation++
		m.quiet = false
	}
	if probeErr != nil {
		m.quiet = false
		return false
	}
	if running {
		if !m.running {
			m.pending = true
			m.generation++
		}
		m.running = true
		m.quiet = false
		return false
	}
	m.running = false
	if !m.quiet {
		m.quietSince = now
		m.quiet = true
	}
	quietPeriod := m.quietPeriod
	if quietPeriod <= 0 {
		quietPeriod = autoQuietPeriod
	}
	if m.interval > 0 {
		if !m.lastSuccess.IsZero() && now.Before(m.lastSuccess.Add(m.interval)) {
			return false
		}
		m.pending = true
	}
	if !m.pending || m.inflight || now.Before(m.retryAfter) || now.Sub(m.quietSince) < quietPeriod {
		return false
	}
	m.inflight = true
	m.flightGeneration = m.generation
	return true
}

func (m *autoMonitor) finish(now time.Time, err error) {
	if !m.inflight {
		return
	}
	m.inflight = false
	if err != nil {
		m.pending = true
		m.retryAfter = now.Add(autoRetryPeriod)
		return
	}
	// A later game session or world change still needs its own completed backup.
	m.pending = m.generation != m.flightGeneration
	m.retryAfter = time.Time{}
	m.lastSuccess = now
}

func (m *autoMonitor) configure(interval time.Duration, lastSuccess time.Time) {
	if interval < 0 {
		interval = 0
	}
	m.interval, m.lastSuccess = interval, lastSuccess
}

func (m *autoMonitor) reset() {
	*m = autoMonitor{quietPeriod: m.quietPeriod}
}
