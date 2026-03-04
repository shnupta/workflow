package models

import "time"

type WorkType string
type Tier string
type Direction string

const (
	// Work types
	WorkTypePRReview      WorkType = "pr_review"
	WorkTypeDeployment    WorkType = "deployment"
	WorkTypeDoc           WorkType = "doc"
	WorkTypeDesign        WorkType = "design"
	WorkTypeCoding        WorkType = "coding"
	WorkTypeTimeline      WorkType = "timeline"
	WorkTypeApproval      WorkType = "approval"
	WorkTypeChase         WorkType = "chase"
	WorkTypeMeeting       WorkType = "meeting"
	WorkTypeMisc          WorkType = "misc"

	// Urgency tiers
	TierToday    Tier = "today"
	TierThisWeek Tier = "this_week"
	TierBacklog  Tier = "backlog"

	// Who's blocked
	DirectionBlockedOnMe   Direction = "blocked_on_me"
	DirectionBlockedOnThem Direction = "blocked_on_them"
)

type Task struct {
	ID          string     `db:"id"`
	Title       string     `db:"title"`
	Description string     `db:"description"`
	WorkType    WorkType   `db:"work_type"`
	Tier        Tier       `db:"tier"`
	Direction   Direction  `db:"direction"`
	PRURL       string     `db:"pr_url"`
	PRSummary   string     `db:"pr_summary"`
	Link        string     `db:"link"`
	Done        bool       `db:"done"`
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at"`
	DoneAt      *time.Time `db:"done_at"`
}

func (t *Task) WorkTypeLabel() string {
	labels := map[WorkType]string{
		WorkTypePRReview:   "PR Review",
		WorkTypeDeployment: "Deployment",
		WorkTypeDoc:        "Doc",
		WorkTypeDesign:     "Design",
		WorkTypeCoding:     "Coding",
		WorkTypeTimeline:   "Timeline",
		WorkTypeApproval:   "Approval",
		WorkTypeChase:      "Chase",
		WorkTypeMeeting:    "Meeting",
		WorkTypeMisc:       "Misc",
	}
	if l, ok := labels[t.WorkType]; ok {
		return l
	}
	return string(t.WorkType)
}

func (t *Task) TierLabel() string {
	labels := map[Tier]string{
		TierToday:    "Today",
		TierThisWeek: "This Week",
		TierBacklog:  "Backlog",
	}
	if l, ok := labels[t.Tier]; ok {
		return l
	}
	return string(t.Tier)
}

func (t *Task) DirectionLabel() string {
	if t.Direction == DirectionBlockedOnMe {
		return "On me"
	}
	return "On them"
}

func (t *Task) IsDeep() bool {
	return t.WorkType == WorkTypePRReview ||
		t.WorkType == WorkTypeCoding ||
		t.WorkType == WorkTypeDesign ||
		t.WorkTypeDeployment()
}

func (t *Task) WorkTypeDeployment() bool {
	return t.WorkType == WorkTypeDeployment
}

func WorkTypeDepth(wt WorkType) string {
	switch wt {
	case WorkTypePRReview, WorkTypeCoding, WorkTypeDesign, WorkTypeDeployment:
		return "deep"
	case WorkTypeDoc, WorkTypeTimeline:
		return "medium"
	case WorkTypeApproval, WorkTypeChase, WorkTypeMeeting, WorkTypeMisc:
		return "shallow"
	}
	return "medium"
}

func AllWorkTypes() []WorkType {
	return []WorkType{
		WorkTypePRReview,
		WorkTypeDeployment,
		WorkTypeDoc,
		WorkTypeDesign,
		WorkTypeCoding,
		WorkTypeTimeline,
		WorkTypeApproval,
		WorkTypeChase,
		WorkTypeMeeting,
		WorkTypeMisc,
	}
}

func AllTiers() []Tier {
	return []Tier{TierToday, TierThisWeek, TierBacklog}
}
