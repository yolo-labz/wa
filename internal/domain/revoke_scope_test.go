package domain

import "testing"

func TestRevokeScopeParse(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		ok   bool
		want RevokeScope
	}{
		{"self", true, RevokeSelf},
		{"everyone", true, RevokeEveryone},
		{"Self", false, 0},
		{" self", false, 0},
		{"", false, 0},
		{"SELF", false, 0},
		{"nobody", false, 0},
	}
	for _, tc := range cases {
		got, err := ParseRevokeScope(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("%q: err=%v ok=%v", tc.in, err, tc.ok)
			continue
		}
		if tc.ok && got != tc.want {
			t.Errorf("%q: got %v want %v", tc.in, got, tc.want)
		}
	}
}

func TestRevokeScopeString(t *testing.T) {
	t.Parallel()
	if RevokeSelf.String() != "self" {
		t.Errorf("RevokeSelf.String() = %q", RevokeSelf.String())
	}
	if RevokeEveryone.String() != "everyone" {
		t.Errorf("RevokeEveryone.String() = %q", RevokeEveryone.String())
	}
	var zero RevokeScope
	if zero.IsValid() {
		t.Error("zero scope must be invalid")
	}
}
