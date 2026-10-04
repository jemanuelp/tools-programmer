package core

const ReviewTimeSourceManual = "manual"

// ReviewTimeEntry is an explicit human duration declaration. It is not a
// verdict, approval, agent interval, or measurement of elapsed wall time.
type ReviewTimeEntry struct {
	ID          string `json:"entry_id"`
	Project     string `json:"project"`
	IssueNumber *int   `json:"issue_number"`
	PR          *int   `json:"pr"`
	SHA         string `json:"sha"`
	Reviewer    string `json:"reviewer"`
	DurationMS  int64  `json:"duration_ms"`
	Source      string `json:"source"`
	RecordedAt  string `json:"recorded_at"`
}

func (e ReviewTimeEntry) Validate() error {
	if !ValidActivityIdentifier(e.ID) || !ValidName(e.Project) || !ValidText(e.Reviewer, 200) || e.DurationMS <= 0 || e.Source != ReviewTimeSourceManual {
		return Fail("invalid_input", "manual review time requires a bounded entry ID, registered project, reviewer, positive duration and manual provenance")
	}
	if e.IssueNumber != nil && !ValidPR(*e.IssueNumber) {
		return Fail("invalid_input", "issue number must be positive")
	}
	if e.PR != nil && !ValidPR(*e.PR) {
		return Fail("invalid_input", "PR number must be positive")
	}
	if e.SHA != "" && !ValidSHA(e.SHA) {
		return Fail("invalid_input", "reviewed SHA must be a lowercase full 40-character SHA")
	}
	return nil
}

type ReviewTimeBucket struct {
	ReviewType    string `json:"review_type"`
	IssueNumber   *int   `json:"issue_number"`
	PR            *int   `json:"pr"`
	SHA           string `json:"sha"`
	HumanReviewMS int64  `json:"human_review_ms"`
	EntryCount    int    `json:"entry_count"`
}

// ReviewTimeReport keeps declared human durations separate from agent effort
// and elapsed review time, which are unknown until automatic coverage exists.
type ReviewTimeReport struct {
	Project            string             `json:"project"`
	HumanReviewMS      int64              `json:"human_review_ms"`
	LocalHumanReviewMS int64              `json:"local_human_review_ms"`
	PRHumanReviewMS    int64              `json:"pr_human_review_ms"`
	AgentReviewMS      *int64             `json:"agent_review_ms"`
	ElapsedReviewMS    *int64             `json:"elapsed_review_ms"`
	AutomaticCoverage  string             `json:"automatic_coverage"`
	Basis              string             `json:"basis"`
	Buckets            []ReviewTimeBucket `json:"buckets"`
}
