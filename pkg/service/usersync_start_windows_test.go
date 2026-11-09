//go:build windows

package service

import "testing"

// TestUserSessions: enumeration works for any caller, SYSTEM or not, and
// never returns session 0, which no helper may start in.
func TestUserSessions(t *testing.T) {
	sessions, err := userSessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if s == 0 {
			t.Errorf("sessions %v include session 0", sessions)
		}
	}
}
