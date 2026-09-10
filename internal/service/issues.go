package service

import "tools-programmer/internal/core"

type IssueSummary struct {
	core.IssueHistory
	Activity core.ActivityReport    `json:"activity"`
	Segments []core.ActivitySegment `json:"segments"`
}

func (s *Service) IssueSummary(project string, number int) (IssueSummary, error) {
	var result IssueSummary
	if !core.ValidName(project) || !core.ValidPR(number) {
		return result, core.Fail("invalid_input", "registered project and positive issue number required")
	}
	h, events, segments, err := s.Store.IssueSnapshot(project, number)
	if err != nil {
		return result, err
	}
	return IssueSummary{IssueHistory: h, Activity: core.CalculateActivity(events, segments), Segments: segments}, nil
}
