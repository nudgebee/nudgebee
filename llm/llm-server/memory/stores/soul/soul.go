package soul

import "time"

// Soul captures a user's stylistic profile. Curated, not inferred.
// One row per user (per tenant if users are tenant-scoped).
type Soul struct {
	TenantID  string    `db:"tenant_id" json:"tenant_id"`
	UserID    string    `db:"user_id" json:"user_id"`
	Version   int       `db:"version" json:"version"`
	Style     Style     `db:"-" json:"style"`
	StyleJSON []byte    `db:"style" json:"-"`
	Markdown  string    `db:"markdown" json:"markdown,omitempty"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// Style is the structured part of a soul: deterministic fields the renderer
// can output in a stable order. Freeform user prose lives in Soul.Markdown.
type Style struct {
	Tone               string `json:"tone,omitempty"`         // "terse" | "friendly" | "formal"
	Verbosity          string `json:"verbosity,omitempty"`    // "minimal" | "balanced" | "detailed"
	RiskPosture        string `json:"risk_posture,omitempty"` // "conservative" | "balanced" | "aggressive"
	ConfirmDestructive bool   `json:"confirm_destructive,omitempty"`
	PreferCLI          bool   `json:"prefer_cli,omitempty"`
	ExpertiseLevel     string `json:"expertise_level,omitempty"`  // "novice" | "intermediate" | "expert"
	DiagnosticStyle    string `json:"diagnostic_style,omitempty"` // "logs_first" | "metrics_first" | "traces_first"
}

// IsEmpty reports whether a Soul has any meaningful content.
func (s *Soul) IsEmpty() bool {
	if s == nil {
		return true
	}
	if s.Markdown != "" {
		return false
	}
	empty := Style{}
	return s.Style == empty
}
