package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestGroupPatchAllNilRefused(t *testing.T) {
	t.Parallel()
	p := GroupPatch{}
	if err := p.Validate(); err == nil {
		t.Fatal("all-nil patch must be refused")
	}
}

func TestGroupPatch_OneFieldAccepted(t *testing.T) {
	t.Parallel()
	sub := "New Subject"
	p := GroupPatch{Subject: &sub}
	if err := p.Validate(); err != nil {
		t.Fatalf("one-field patch must be valid: %v", err)
	}
}

func TestGroupPatch_SubjectTooLarge(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", 101)
	p := GroupPatch{Subject: &big}
	err := p.Validate()
	if err == nil {
		t.Fatal("oversized subject must be refused")
	}
	if !errors.Is(err, ErrMessageTooLarge) {
		t.Errorf("want ErrMessageTooLarge, got %v", err)
	}
}

func TestGroupPatch_DescriptionTooLarge(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("y", 513)
	p := GroupPatch{Description: &big}
	err := p.Validate()
	if err == nil {
		t.Fatal("oversized description must be refused")
	}
	if !errors.Is(err, ErrMessageTooLarge) {
		t.Errorf("want ErrMessageTooLarge, got %v", err)
	}
}

func TestGroupPatch_EmptyStringIsRemoval(t *testing.T) {
	t.Parallel()
	empty := ""
	p := GroupPatch{IconPath: &empty}
	if err := p.Validate(); err != nil {
		t.Fatalf("empty-string IconPath is the documented removal sentinel: %v", err)
	}
}

func TestGroupInviteLink_IsZero(t *testing.T) {
	t.Parallel()
	var z GroupInviteLink
	if !z.IsZero() {
		t.Fatal("zero value must be IsZero")
	}
	g := GroupInviteLink{URL: "https://chat.whatsapp.com/ABC123"}
	if g.IsZero() {
		t.Fatal("populated value must not be IsZero")
	}
}
