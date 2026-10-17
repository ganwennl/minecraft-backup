package main

import (
	"errors"
	"testing"
	"time"
)

func monitorTestTime() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) }

func TestAutoMonitorInitialIdleAndNoRepeatedBackup(t *testing.T) {
	var monitor autoMonitor
	now := monitorTestTime()
	if monitor.observe(now, false, nil, "world1") {
		t.Fatal("initial idle observation was due immediately")
	}
	if monitor.observe(now.Add(19*time.Second), false, nil, "world1") {
		t.Fatal("backup was due before twenty quiet seconds")
	}
	if !monitor.observe(now.Add(20*time.Second), false, nil, "world1") {
		t.Fatal("initial idle backup was not due after twenty quiet seconds")
	}
	if monitor.observe(now.Add(21*time.Second), false, nil, "world1") {
		t.Fatal("inflight backup was scheduled again")
	}
	monitor.finish(now.Add(22*time.Second), nil)
	if monitor.observe(now.Add(time.Hour), false, nil, "world1") {
		t.Fatal("unchanged idle worlds were backed up repeatedly")
	}
}

func TestAutoMonitorAcceptsZeroTimeAndKeepsPendingAcrossBusyGap(t *testing.T) {
	var monitor autoMonitor
	now := time.Time{}
	monitor.observe(now, false, nil, "world1")
	// The UI may skip observations while a manual operation owns the worker.
	if !monitor.observe(now.Add(time.Minute), false, nil, "world1") {
		t.Fatal("pending initial backup was lost during a manual operation")
	}
}

func TestAutoMonitorSchedulesAfterEachGameExit(t *testing.T) {
	monitor := autoMonitor{quietPeriod: time.Second}
	now := monitorTestTime()
	monitor.observe(now, false, nil, "world1")
	if !monitor.observe(now.Add(time.Second), false, nil, "world1") {
		t.Fatal("initial backup was not due")
	}
	monitor.finish(now.Add(time.Second), nil)
	for _, start := range []time.Duration{10 * time.Second, 20 * time.Second} {
		if monitor.observe(now.Add(start), true, nil, "world1") {
			t.Fatal("backup was due while the game was running")
		}
		if monitor.observe(now.Add(start+time.Second), false, nil, "world1") {
			t.Fatal("game exit skipped the quiet period")
		}
		if !monitor.observe(now.Add(start+2*time.Second), false, nil, "world1") {
			t.Fatal("game exit did not schedule another backup")
		}
		monitor.finish(now.Add(start+2*time.Second), nil)
	}
}

func TestAutoMonitorGameRestartResetsQuietPeriod(t *testing.T) {
	var monitor autoMonitor
	now := monitorTestTime()
	monitor.observe(now, false, nil, "world1")
	if monitor.observe(now.Add(19*time.Second), true, nil, "world1") {
		t.Fatal("game restart triggered a backup")
	}
	monitor.observe(now.Add(20*time.Second), false, nil, "world1")
	if monitor.observe(now.Add(39*time.Second), false, nil, "world1") {
		t.Fatal("quiet time from before the restart was retained")
	}
	if !monitor.observe(now.Add(40*time.Second), false, nil, "world1") {
		t.Fatal("backup was not due after the restarted game stopped")
	}
}

func TestAutoMonitorUnknownProbeBreaksQuietPeriod(t *testing.T) {
	var monitor autoMonitor
	now := monitorTestTime()
	monitor.observe(now, false, nil, "world1")
	if monitor.observe(now.Add(20*time.Second), false, errors.New("probe failed"), "world1") {
		t.Fatal("unknown game state triggered a backup")
	}
	monitor.observe(now.Add(21*time.Second), false, nil, "world1")
	if monitor.observe(now.Add(40*time.Second), false, nil, "world1") {
		t.Fatal("quiet time from before the failed probe was retained")
	}
	if !monitor.observe(now.Add(41*time.Second), false, nil, "world1") {
		t.Fatal("backup was not due after game probes recovered")
	}
}

func TestAutoMonitorWorldChangesRequireStableQuietPeriod(t *testing.T) {
	var monitor autoMonitor
	now := monitorTestTime()
	monitor.observe(now, false, nil, "world1")
	monitor.observe(now.Add(20*time.Second), false, nil, "world1")
	monitor.finish(now.Add(20*time.Second), nil)
	if monitor.observe(now.Add(30*time.Second), false, nil, "world1,world2") {
		t.Fatal("new world skipped its quiet period")
	}
	if monitor.observe(now.Add(49*time.Second), false, nil, "world1,world2") {
		t.Fatal("new world was due too soon")
	}
	if !monitor.observe(now.Add(50*time.Second), false, nil, "world1,world2") {
		t.Fatal("new world did not trigger a backup")
	}
	monitor.finish(now.Add(50*time.Second), nil)
	monitor.observe(now.Add(60*time.Second), true, nil, "world1,world2,world3")
	monitor.observe(now.Add(70*time.Second), false, nil, "world1,world2,world3")
	if !monitor.observe(now.Add(90*time.Second), false, nil, "world1,world2,world3") {
		t.Fatal("world discovered during a running game was forgotten")
	}
}

func TestAutoMonitorFailureRetriesAfterOneMinute(t *testing.T) {
	var monitor autoMonitor
	now := monitorTestTime()
	monitor.observe(now, false, nil, "world1")
	monitor.observe(now.Add(20*time.Second), false, nil, "world1")
	monitor.finish(now.Add(21*time.Second), errors.New("backup failed"))
	if monitor.observe(now.Add(80*time.Second), false, nil, "world1") {
		t.Fatal("failed backup retried before one minute")
	}
	if !monitor.observe(now.Add(81*time.Second), false, nil, "world1") {
		t.Fatal("failed backup was not retried after one minute")
	}
	monitor.finish(now.Add(82*time.Second), nil)
	if monitor.observe(now.Add(time.Hour), false, nil, "world1") {
		t.Fatal("successful retry did not clear pending work")
	}
}

func TestAutoMonitorResetSchedulesNewIdleCheck(t *testing.T) {
	monitor := autoMonitor{quietPeriod: time.Second}
	now := monitorTestTime()
	monitor.observe(now, false, nil, "world1")
	monitor.observe(now.Add(time.Second), false, nil, "world1")
	monitor.finish(now.Add(time.Second), nil)
	monitor.reset()
	if monitor.observe(now.Add(2*time.Second), false, nil, "world1") {
		t.Fatal("reset skipped the quiet period")
	}
	if !monitor.observe(now.Add(3*time.Second), false, nil, "world1") {
		t.Fatal("reset did not schedule a fresh idle check")
	}
}

func TestAutoMonitorCompletionKeepsChangesObservedInflight(t *testing.T) {
	monitor := autoMonitor{quietPeriod: time.Second}
	now := monitorTestTime()
	monitor.observe(now, false, nil, "world1")
	monitor.observe(now.Add(time.Second), false, nil, "world1")
	if monitor.observe(now.Add(2*time.Second), true, nil, "world1,world2") {
		t.Fatal("another backup was scheduled while one was inflight")
	}
	monitor.finish(now.Add(3*time.Second), nil)
	monitor.observe(now.Add(4*time.Second), false, nil, "world1,world2")
	if !monitor.observe(now.Add(5*time.Second), false, nil, "world1,world2") {
		t.Fatal("successful completion cleared a newer game/world change")
	}
}

func TestAutoMonitorResetIgnoresPreviousCompletion(t *testing.T) {
	monitor := autoMonitor{quietPeriod: time.Second}
	now := monitorTestTime()
	monitor.observe(now, false, nil, "world1")
	monitor.observe(now.Add(time.Second), false, nil, "world1")
	monitor.reset()
	monitor.observe(now.Add(2*time.Second), false, nil, "world1")
	monitor.finish(now.Add(2*time.Second), nil)
	if !monitor.observe(now.Add(3*time.Second), false, nil, "world1") {
		t.Fatal("completion from before reset cleared new pending work")
	}
}

func TestAutoMonitorIntervalFirstRunCreatesBaseline(t *testing.T) {
	var monitor autoMonitor
	monitor.configure(12*time.Hour, time.Time{})
	now := monitorTestTime()
	if monitor.observe(now, false, nil, "world1") {
		t.Fatal("first periodic backup skipped the quiet period")
	}
	if !monitor.observe(now.Add(20*time.Second), false, nil, "world1") {
		t.Fatal("first periodic backup did not create a baseline")
	}
}

func TestAutoMonitorIntervalUsesPersistedSuccessAfterRestart(t *testing.T) {
	for _, interval := range []time.Duration{12 * time.Hour, 24 * time.Hour} {
		t.Run(interval.String(), func(t *testing.T) {
			now := monitorTestTime()
			lastSuccess := now.Add(-time.Hour)
			var monitor autoMonitor
			monitor.configure(interval, lastSuccess)
			monitor.observe(now, false, nil, "world1")
			if monitor.observe(lastSuccess.Add(interval-time.Second), false, nil, "world1") {
				t.Fatal("restart ignored the previous successful backup time")
			}
			if !monitor.observe(lastSuccess.Add(interval), false, nil, "world1") {
				t.Fatal("periodic backup was not due at the configured interval")
			}
		})
	}
}

func TestAutoMonitorIntervalDoesNotScanEarlyForGameExitOrNewWorld(t *testing.T) {
	now := monitorTestTime()
	var monitor autoMonitor
	monitor.configure(12*time.Hour, now)
	monitor.observe(now.Add(time.Hour), true, nil, "world1")
	monitor.observe(now.Add(2*time.Hour), false, nil, "world1,world2")
	if monitor.observe(now.Add(2*time.Hour+20*time.Second), false, nil, "world1,world2") {
		t.Fatal("game exit or a new world bypassed the interval")
	}
	if !monitor.observe(now.Add(12*time.Hour), false, nil, "world1,world2") {
		t.Fatal("pending world change was not scheduled when the interval elapsed")
	}
}

func TestAutoMonitorIntervalWaitsForGameExitAndQuietPeriod(t *testing.T) {
	now := monitorTestTime()
	var monitor autoMonitor
	monitor.configure(12*time.Hour, now)
	if monitor.observe(now.Add(12*time.Hour), true, nil, "world1") {
		t.Fatal("interval triggered a backup while the game was running")
	}
	stopped := now.Add(13 * time.Hour)
	if monitor.observe(stopped, false, nil, "world1") {
		t.Fatal("overdue backup skipped the quiet period after game exit")
	}
	if monitor.observe(stopped.Add(19*time.Second), false, nil, "world1") {
		t.Fatal("overdue backup did not wait for twenty quiet seconds")
	}
	if !monitor.observe(stopped.Add(20*time.Second), false, nil, "world1") {
		t.Fatal("overdue backup did not run after the game stopped")
	}
}

func TestAutoMonitorIntervalAdvancesAfterUnchangedSuccess(t *testing.T) {
	now := monitorTestTime()
	var monitor autoMonitor
	monitor.configure(24*time.Hour, now.Add(-24*time.Hour))
	monitor.observe(now, false, nil, "world1")
	due := now.Add(20 * time.Second)
	if !monitor.observe(due, false, nil, "world1") {
		t.Fatal("overdue periodic backup was not scheduled")
	}
	// An unchanged backup is still a successful content check.
	completed := due.Add(time.Second)
	monitor.finish(completed, nil)
	if !monitor.lastSuccess.Equal(completed) {
		t.Fatal("unchanged success did not advance the interval baseline")
	}
	if monitor.observe(completed.Add(24*time.Hour-time.Second), false, nil, "world1") {
		t.Fatal("unchanged worlds were rechecked before the new interval")
	}
	if !monitor.observe(completed.Add(24*time.Hour), false, nil, "world1") {
		t.Fatal("unchanged worlds were not checked again after the interval")
	}
}

func TestAutoMonitorIntervalFailureRetriesWithoutAdvancingSuccess(t *testing.T) {
	now := monitorTestTime()
	lastSuccess := now.Add(-12 * time.Hour)
	var monitor autoMonitor
	monitor.configure(12*time.Hour, lastSuccess)
	monitor.observe(now, false, nil, "world1")
	monitor.observe(now.Add(20*time.Second), false, nil, "world1")
	monitor.finish(now.Add(21*time.Second), errors.New("backup failed"))
	if !monitor.lastSuccess.Equal(lastSuccess) {
		t.Fatal("failed backup advanced the interval baseline")
	}
	if monitor.observe(now.Add(80*time.Second), false, nil, "world1") {
		t.Fatal("periodic backup retried before one minute")
	}
	if !monitor.observe(now.Add(81*time.Second), false, nil, "world1") {
		t.Fatal("periodic backup was not retried after one minute")
	}
}

func TestAutoMonitorIntervalCanBeReconfiguredAfterReset(t *testing.T) {
	now := monitorTestTime()
	var monitor autoMonitor
	monitor.configure(12*time.Hour, now.Add(-12*time.Hour))
	monitor.observe(now, false, nil, "world1")
	monitor.observe(now.Add(20*time.Second), false, nil, "world1")
	monitor.reset()
	monitor.configure(24*time.Hour, now)
	monitor.observe(now.Add(time.Hour), false, nil, "world1")
	if monitor.observe(now.Add(12*time.Hour), false, nil, "world1") {
		t.Fatal("previous interval survived reset/reconfiguration")
	}
	if !monitor.observe(now.Add(24*time.Hour), false, nil, "world1") {
		t.Fatal("reconfigured interval was not scheduled")
	}
}
