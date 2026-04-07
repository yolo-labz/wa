package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewSession_Happy(t *testing.T) {
	t.Parallel()
	s, err := NewSession(testRecipient, 1, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !s.IsLoggedIn() {
		t.Error("IsLoggedIn false")
	}
	if s.DeviceID() != 1 {
		t.Errorf("DeviceID=%d", s.DeviceID())
	}
}

func TestNewSession_ZeroJID(t *testing.T) {
	t.Parallel()
	_, err := NewSession(JID{}, 1, time.Now())
	if !errors.Is(err, ErrInvalidJID) {
		t.Errorf("want ErrInvalidJID, got %v", err)
	}
}

func TestNewSession_ZeroDevice(t *testing.T) {
	t.Parallel()
	_, err := NewSession(testRecipient, 0, time.Now())
	if !errors.Is(err, ErrInvalidJID) {
		t.Errorf("want ErrInvalidJID, got %v", err)
	}
}

func TestSession_IsZero(t *testing.T) {
	t.Parallel()
	var s Session
	if !s.IsZero() || s.IsLoggedIn() {
		t.Error("zero session flags wrong")
	}
}
