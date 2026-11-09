package installer

import (
	"slices"
	"testing"
)

func TestCancelsForgetARunWhenItEnds(t *testing.T) {
	c := NewCancels()
	c.Cancel("Withdrawn")
	c.act("Acted")

	// A new request during the run lifts the withdrawal but not the refusal:
	// the installer for "Acted" is still running.
	c.Reset("Acted")
	if c.Cancel("Acted") {
		t.Fatal("Cancel accepted an item whose installer is running")
	}

	c.EndRun()
	// An admin-required item must not stay skipped after the run it was
	// withdrawn from.
	if c.withdrawn("Withdrawn") {
		t.Fatal("a withdrawal outlived its run")
	}
	if !c.Cancel("Acted") {
		t.Fatal("Cancel refused an item no run is acting on")
	}
	c.Reset("Acted")
	if c.withdrawn("Acted") {
		t.Fatal("a new request did not lift the withdrawal")
	}
}

func TestNilCancelsWithdrawNothing(t *testing.T) {
	var c *Cancels
	if c.Cancel("x") || c.withdrawn("x") || !c.act("x") {
		t.Fatal("a nil Cancels must leave every item to run")
	}
}

// TestCancelsWithdrawnListsTheRunsCancels verifies Withdrawn reports the items
// canceled in the current run, which the user plan holds, and forgets them at
// EndRun and when a new request resets one.
func TestCancelsWithdrawnListsTheRunsCancels(t *testing.T) {
	c := NewCancels()
	c.Cancel("B")
	c.Cancel("A")
	c.Cancel("Renewed")
	c.Reset("Renewed")
	c.act("Acted")
	c.Cancel("Acted") // refused: its installer is running

	if got := c.Withdrawn(); !slices.Equal(got, []string{"A", "B"}) {
		t.Fatalf("Withdrawn() = %v, want [A B]", got)
	}
	c.EndRun()
	if got := c.Withdrawn(); len(got) != 0 {
		t.Fatalf("Withdrawn() after EndRun = %v, want none", got)
	}
	var none *Cancels
	if got := none.Withdrawn(); len(got) != 0 {
		t.Fatalf("nil Withdrawn() = %v, want none", got)
	}
}
