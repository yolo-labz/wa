package domain

import "testing"

func TestPrivacyKeyValueTuple(t *testing.T) {
	t.Parallel()

	t.Run("valid_tuple_validates", func(t *testing.T) {
		t.Parallel()
		tup := PrivacyTuple{Key: PrivacyKeyReadReceipts, Value: PrivacyValueEveryone}
		if err := tup.Validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("zero_key_rejected", func(t *testing.T) {
		t.Parallel()
		tup := PrivacyTuple{Key: 0, Value: PrivacyValueEveryone}
		if err := tup.Validate(); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("zero_value_rejected", func(t *testing.T) {
		t.Parallel()
		tup := PrivacyTuple{Key: PrivacyKeyGroups, Value: 0}
		if err := tup.Validate(); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("out_of_range_rejected", func(t *testing.T) {
		t.Parallel()
		tup := PrivacyTuple{Key: PrivacyKey(99), Value: PrivacyValue(99)}
		if err := tup.Validate(); err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestParsePrivacyKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want PrivacyKey
		err  bool
	}{
		{"groups", PrivacyKeyGroups, false},
		{"readReceipts", PrivacyKeyReadReceipts, false},
		{"lastSeen", PrivacyKeyLastSeen, false},
		{"profile", PrivacyKeyProfile, false},
		{"about", PrivacyKeyAbout, false},
		{"", 0, true},
		{"GROUPS", 0, true},
		{"bogus", 0, true},
	}
	for _, c := range cases {
		got, err := ParsePrivacyKey(c.in)
		if c.err {
			if err == nil {
				t.Errorf("%q: want error, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("%q: got %v want %v", c.in, got, c.want)
		}
		if got.String() != c.in {
			t.Errorf("%q: round-trip String() = %q", c.in, got.String())
		}
	}
}

func TestParsePrivacyValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want PrivacyValue
		err  bool
	}{
		{"everyone", PrivacyValueEveryone, false},
		{"contacts", PrivacyValueContacts, false},
		{"nobody", PrivacyValueNobody, false},
		{"", 0, true},
		{"ALL", 0, true},
	}
	for _, c := range cases {
		got, err := ParsePrivacyValue(c.in)
		if c.err {
			if err == nil {
				t.Errorf("%q: want error, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("%q: got %v want %v", c.in, got, c.want)
		}
		if got.String() != c.in {
			t.Errorf("%q: round-trip String() = %q", c.in, got.String())
		}
	}
}
