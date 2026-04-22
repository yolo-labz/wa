package domain

import "testing"

func TestAvatarSize_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s    AvatarSize
		want string
	}{
		{AvatarPreview, "preview"},
		{AvatarFull, "full"},
		{AvatarSize(0), "unknown"},
		{AvatarSize(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("%d: got %q want %q", c.s, got, c.want)
		}
	}
}

func TestAvatarSize_IsValid(t *testing.T) {
	t.Parallel()
	if !AvatarPreview.IsValid() || !AvatarFull.IsValid() {
		t.Fatal("declared constants must be valid")
	}
	if AvatarSize(0).IsValid() || AvatarSize(99).IsValid() {
		t.Fatal("undeclared values must be invalid")
	}
}

func TestParseAvatarSize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want AvatarSize
		err  bool
	}{
		{"", AvatarPreview, false}, // default per socket-layer convention
		{"preview", AvatarPreview, false},
		{"full", AvatarFull, false},
		{"Preview", 0, true},
		{"thumb", 0, true},
	}
	for _, c := range cases {
		got, err := ParseAvatarSize(c.in)
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
	}
}
