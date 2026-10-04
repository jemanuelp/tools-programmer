package core

import (
	"strings"
	"testing"
)

func reviewTimeInt(value int) *int { return &value }

func TestReviewTimeEntryValidate(t *testing.T) {
	valid := ReviewTimeEntry{
		ID:         "entry-1",
		Project:    "demo",
		Reviewer:   "Declared reviewer",
		DurationMS: 60000,
		Source:     "manual",
	}
	knownPR := valid
	knownPR.ID = "entry-2"
	knownPR.IssueNumber = reviewTimeInt(42)
	knownPR.PR = reviewTimeInt(7)
	knownPR.SHA = strings.Repeat("a", 40)
	unknownPR := valid
	unknownPR.ID = "entry-3"
	unknownPR.PR = reviewTimeInt(7)

	for _, entry := range []ReviewTimeEntry{valid, knownPR, unknownPR} {
		if err := entry.Validate(); err != nil {
			t.Errorf("valid manual entry rejected: %+v: %v", entry, err)
		}
	}

	tests := []struct {
		name string
		edit func(*ReviewTimeEntry)
	}{
		{name: "missing identity", edit: func(e *ReviewTimeEntry) { e.ID = "" }},
		{name: "invalid identity", edit: func(e *ReviewTimeEntry) { e.ID = "entry with spaces" }},
		{name: "identity too long", edit: func(e *ReviewTimeEntry) { e.ID = strings.Repeat("a", 201) }},
		{name: "missing project", edit: func(e *ReviewTimeEntry) { e.Project = "" }},
		{name: "missing reviewer provenance", edit: func(e *ReviewTimeEntry) { e.Reviewer = " " }},
		{name: "reviewer too long", edit: func(e *ReviewTimeEntry) { e.Reviewer = strings.Repeat("a", 201) }},
		{name: "zero duration", edit: func(e *ReviewTimeEntry) { e.DurationMS = 0 }},
		{name: "negative duration", edit: func(e *ReviewTimeEntry) { e.DurationMS = -1 }},
		{name: "invalid issue number", edit: func(e *ReviewTimeEntry) { e.IssueNumber = reviewTimeInt(0) }},
		{name: "invalid PR", edit: func(e *ReviewTimeEntry) { e.PR = reviewTimeInt(-1) }},
		{name: "short SHA", edit: func(e *ReviewTimeEntry) { e.SHA = "abc123" }},
		{name: "uppercase SHA", edit: func(e *ReviewTimeEntry) { e.SHA = strings.Repeat("A", 40) }},
		{name: "non-manual source", edit: func(e *ReviewTimeEntry) { e.Source = "opencode" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := valid
			test.edit(&entry)
			if err := entry.Validate(); err == nil {
				t.Fatalf("invalid entry accepted: %+v", entry)
			}
		})
	}
}
