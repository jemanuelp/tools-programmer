package service

import (
	"math"
	"sort"

	"tools-programmer/internal/core"
)

type reviewTimeGroupKey struct {
	reviewType string
	issue      int
	hasIssue   bool
	pr         int
	hasPR      bool
	sha        string
}

func addReviewDuration(total, duration int64) (int64, error) {
	if duration > math.MaxInt64-total {
		return total, core.Fail("review_time_overflow", "review-time total exceeds the supported integer range")
	}
	return total + duration, nil
}

func copyReviewTimeNumber(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// ReviewTimeReport summarizes only explicit manual declarations in T1.
// Automatic agent effort and elapsed review time remain unknown, not zero.
func (s *Service) ReviewTimeReport(project string, issueNumber, pr *int) (core.ReviewTimeReport, error) {
	var report core.ReviewTimeReport
	if !core.ValidName(project) {
		return report, core.Fail("invalid_input", "registered project required")
	}
	if issueNumber != nil && !core.ValidPR(*issueNumber) {
		return report, core.Fail("invalid_input", "issue number filter must be positive")
	}
	if pr != nil && !core.ValidPR(*pr) {
		return report, core.Fail("invalid_input", "PR filter must be positive")
	}
	entries, err := s.Store.ReviewTimeEntries(project)
	if err != nil {
		return report, err
	}

	report = core.ReviewTimeReport{
		Project:           project,
		AutomaticCoverage: "pending",
		Basis:             "manual declarations only; automatic OpenCode capture pending; elapsed time is not inferred",
		Buckets:           []core.ReviewTimeBucket{},
	}
	groups := make(map[reviewTimeGroupKey]core.ReviewTimeBucket)
	for _, entry := range entries {
		if issueNumber != nil && (entry.IssueNumber == nil || *entry.IssueNumber != *issueNumber) {
			continue
		}
		if pr != nil && (entry.PR == nil || *entry.PR != *pr) {
			continue
		}
		reviewType := "local"
		if entry.PR != nil {
			reviewType = "pr"
		}
		sha := entry.SHA
		if sha == "" {
			sha = "unknown"
		}
		key := reviewTimeGroupKey{reviewType: reviewType, sha: sha}
		if entry.IssueNumber != nil {
			key.issue, key.hasIssue = *entry.IssueNumber, true
		}
		if entry.PR != nil {
			key.pr, key.hasPR = *entry.PR, true
		}
		bucket, ok := groups[key]
		if !ok {
			bucket = core.ReviewTimeBucket{
				ReviewType:  reviewType,
				IssueNumber: copyReviewTimeNumber(entry.IssueNumber),
				PR:          copyReviewTimeNumber(entry.PR),
				SHA:         sha,
			}
		}
		bucket.HumanReviewMS, err = addReviewDuration(bucket.HumanReviewMS, entry.DurationMS)
		if err != nil {
			return core.ReviewTimeReport{}, err
		}
		bucket.EntryCount++
		groups[key] = bucket
		report.HumanReviewMS, err = addReviewDuration(report.HumanReviewMS, entry.DurationMS)
		if err != nil {
			return core.ReviewTimeReport{}, err
		}
		if reviewType == "pr" {
			report.PRHumanReviewMS, err = addReviewDuration(report.PRHumanReviewMS, entry.DurationMS)
		} else {
			report.LocalHumanReviewMS, err = addReviewDuration(report.LocalHumanReviewMS, entry.DurationMS)
		}
		if err != nil {
			return core.ReviewTimeReport{}, err
		}
	}
	for _, bucket := range groups {
		report.Buckets = append(report.Buckets, bucket)
	}
	sort.Slice(report.Buckets, func(i, j int) bool {
		a, b := report.Buckets[i], report.Buckets[j]
		if a.ReviewType != b.ReviewType {
			return a.ReviewType < b.ReviewType
		}
		if a.IssueNumber == nil || b.IssueNumber == nil {
			if a.IssueNumber != nil || b.IssueNumber != nil {
				return a.IssueNumber == nil
			}
		} else if *a.IssueNumber != *b.IssueNumber {
			return *a.IssueNumber < *b.IssueNumber
		}
		if a.PR == nil || b.PR == nil {
			if a.PR != nil || b.PR != nil {
				return a.PR == nil
			}
		} else if *a.PR != *b.PR {
			return *a.PR < *b.PR
		}
		return a.SHA < b.SHA
	})
	return report, nil
}
