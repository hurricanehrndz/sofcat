package installer

import "testing"

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
