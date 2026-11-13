package service

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/report"
)

// A profile or GPO that arrives after the service started must reach the
// next run without a restart, and one that goes away must stop applying.
// Every run reads the managed settings and the write roots at its start;
// requests between runs read nothing and see the last run's settings.
func TestEveryRunRefreshesManagedPolicy(t *testing.T) {
	var mu sync.Mutex
	managedURL := ""
	rootReads := 0
	origConfig, origRoots := refreshConfig, refreshRoots
	t.Cleanup(func() { refreshConfig, refreshRoots = origConfig, origRoots })
	refreshConfig = func(cfg config.Configuration) config.Configuration {
		mu.Lock()
		defer mu.Unlock()
		cfg.URL = "https://file.example/"
		if managedURL != "" {
			cfg.URL = managedURL
		}
		return cfg
	}
	refreshRoots = func() placement.Roots {
		mu.Lock()
		defer mu.Unlock()
		rootReads++
		return placement.Roots{}
	}
	setPolicy := func(url string) {
		mu.Lock()
		defer mu.Unlock()
		managedURL = url
	}

	ran := make(chan string, 8)
	sr, ctx := startTestRunner(t, testServiceConfig(t), func(cfg config.Configuration, _ installer.ProgressFn, _ *installer.Cancels) (*report.Report, error) {
		ran <- cfg.URL
		return nil, nil
	})
	run := func(action string) string {
		t.Helper()
		if _, err := sr.submit(ctx, Command{Action: action}); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		return <-ran
	}

	if got := <-ran; got != "https://file.example/" {
		t.Fatalf("startup run used %q", got)
	}
	setPolicy("https://mdm.example/")
	if _, err := sr.submit(ctx, Command{Action: actionGetBranding}); err != nil {
		t.Fatal(err)
	}
	if got := sr.config().URL; got != "https://file.example/" {
		t.Fatalf("a request between runs read the policy: %q", got)
	}
	if got := run(actionRun); got != "https://mdm.example/" {
		t.Fatalf("run after the policy was written used %q", got)
	}
	setPolicy("")
	if got := run(actionRun); got != "https://file.example/" {
		t.Fatalf("run after the policy was removed used %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if rootReads != 3 {
		t.Fatalf("write roots read %d times for 3 runs", rootReads)
	}
}

// A managed service_interval the service cannot use must not stop it or its
// schedule: the run keeps the last valid one.
func TestRefreshKeepsTheIntervalWhenTheNewOneIsInvalid(t *testing.T) {
	origConfig, origRoots := refreshConfig, refreshRoots
	t.Cleanup(func() { refreshConfig, refreshRoots = origConfig, origRoots })
	refreshRoots = func() placement.Roots { return placement.Roots{} }
	next := "15m"
	refreshConfig = func(cfg config.Configuration) config.Configuration {
		cfg.ServiceInterval = next
		return cfg
	}
	sr := newServiceRunner(config.Configuration{ServiceInterval: "1h"}, noopRun)
	sr.refresh()
	if got := sr.config().ServiceInterval; got != "15m" {
		t.Fatalf("interval = %q, want the managed 15m", got)
	}
	for _, bad := range []string{"soon", "-5m", "0s"} {
		next = bad
		sr.refresh()
		if got := sr.config().ServiceInterval; got != "15m" {
			t.Fatalf("interval after %q = %q, want 15m kept", bad, got)
		}
	}
}

// A managed service_interval applies to the schedule from the next scheduled
// run on, without a restart: started hourly, the service must not wait an
// hour to run again once policy shortens the interval.
func TestScheduleFollowsAManagedInterval(t *testing.T) {
	origConfig, origRoots := refreshConfig, refreshRoots
	t.Cleanup(func() { refreshConfig, refreshRoots = origConfig, origRoots })
	refreshRoots = func() placement.Roots { return placement.Roots{} }
	refreshConfig = func(cfg config.Configuration) config.Configuration {
		cfg.ServiceInterval = "20ms"
		return cfg
	}
	var runs atomic.Int32
	startTestRunner(t, testServiceConfig(t), countingRun(&runs, nil, nil))
	waitFor(t, "scheduled runs at the managed interval", func() bool { return runs.Load() >= 3 })
}
