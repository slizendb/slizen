package service

import (
	"time"

	"github.com/slizendb/slizen/internal/cachepolicy"
	"github.com/slizendb/slizen/internal/config"
	"github.com/slizendb/slizen/internal/hotness"
	"github.com/slizendb/slizen/internal/privacy"
)

const (
	AuditSchemaVersion = "slizen.audit.v1"
	DefaultAuditLimit  = 100
	MaxAuditLimit      = 1000

	AuditRecommendationReviewForCache    = "review_for_cache"
	AuditRecommendationContinueObserving = "continue_observing"
	AuditRecommendationNoChange          = "no_change"
	AuditReasonEffectivePolicyObserve    = "effective_policy_observe"
	AuditReasonEffectivePolicyCache      = "effective_policy_cache"
	AuditReasonEffectivePolicyDeny       = "effective_policy_deny"
	AuditReasonHotnessStateHot           = "hotness_state_hot"
	AuditReasonHotnessStateNotHot        = "hotness_state_not_hot"
)

// AuditReport is a bounded, machine-readable view of hotness telemetry. It
// contains key identifiers and policy modes, but never Redis values or policy
// prefixes.
type AuditReport struct {
	SchemaVersion                string       `json:"schema_version"`
	GeneratedAt                  string       `json:"generated_at"`
	MeasurementWindow            string       `json:"measurement_window"`
	Mode                         string       `json:"mode"`
	KeyVisibility                string       `json:"key_visibility"`
	TrackedKeys                  int          `json:"tracked_keys"`
	TrackingCapacity             int          `json:"tracking_capacity"`
	TrackingEvictions            uint64       `json:"tracking_evictions"`
	OversizedObservationsDropped uint64       `json:"oversized_observations_dropped"`
	TelemetryComplete            bool         `json:"telemetry_complete"`
	ReturnedEntries              int          `json:"returned_entries"`
	Truncated                    bool         `json:"truncated"`
	Entries                      []AuditEntry `json:"entries"`
}

type AuditEntry struct {
	ID                  string   `json:"id"`
	RequestRate         float64  `json:"request_rate"`
	HotnessState        string   `json:"hotness_state"`
	EffectivePolicyMode string   `json:"effective_policy_mode"`
	Recommendation      string   `json:"recommendation"`
	ReasonCodes         []string `json:"reason_codes"`
}

func (s *Service) Audit(limit int) AuditReport {
	limit = normalizeAuditLimit(limit)
	view := s.tracker.AdvanceAndSnapshot(limit)
	s.handleTransitions(view.Transitions)
	visibility := config.EffectiveKeyVisibility(s.cfg)
	entries := make([]AuditEntry, 0, len(view.Snapshots))
	for _, snapshot := range view.Snapshots {
		policyMode := auditPolicyMode(s.policies.Match(snapshot.Key).Mode)
		recommendation, reasons := auditRecommendation(policyMode, snapshot.State)
		entries = append(entries, AuditEntry{
			ID:                  privacy.KeyIdentifier(snapshot.Key, s.cfg.Privacy.KeyHashSecret, visibility),
			RequestRate:         snapshot.RequestRate,
			HotnessState:        string(snapshot.State),
			EffectivePolicyMode: policyMode,
			Recommendation:      recommendation,
			ReasonCodes:         reasons,
		})
	}

	return AuditReport{
		SchemaVersion:                AuditSchemaVersion,
		GeneratedAt:                  s.clock.Now().UTC().Format(time.RFC3339Nano),
		MeasurementWindow:            s.cfg.Hotness.Window.String(),
		Mode:                         s.cfg.Mode,
		KeyVisibility:                visibility,
		TrackedKeys:                  view.Tracked,
		TrackingCapacity:             s.cfg.Hotness.MaxTrackedKeys,
		TrackingEvictions:            view.Evictions,
		OversizedObservationsDropped: view.OversizedObservationsDropped,
		TelemetryComplete:            view.Tracked <= len(entries) && view.Evictions == 0 && view.OversizedObservationsDropped == 0,
		ReturnedEntries:              len(entries),
		Truncated:                    view.Tracked > len(entries),
		Entries:                      entries,
	}
}

func normalizeAuditLimit(limit int) int {
	if limit <= 0 {
		return DefaultAuditLimit
	}
	if limit > MaxAuditLimit {
		return MaxAuditLimit
	}
	return limit
}

func auditPolicyMode(mode cachepolicy.Mode) string {
	switch mode {
	case cachepolicy.ModeObserve:
		return "observe"
	case cachepolicy.ModeCache:
		return "cache"
	default:
		return "deny"
	}
}

func auditRecommendation(policyMode string, state hotness.State) (string, []string) {
	switch policyMode {
	case "observe":
		if state == hotness.StateHot {
			return AuditRecommendationReviewForCache, []string{
				AuditReasonEffectivePolicyObserve,
				AuditReasonHotnessStateHot,
			}
		}
		return AuditRecommendationContinueObserving, []string{
			AuditReasonEffectivePolicyObserve,
			AuditReasonHotnessStateNotHot,
		}
	case "cache":
		reasons := []string{AuditReasonEffectivePolicyCache, AuditReasonHotnessStateNotHot}
		if state == hotness.StateHot {
			reasons[1] = AuditReasonHotnessStateHot
		}
		return AuditRecommendationNoChange, reasons
	default:
		return AuditRecommendationNoChange, []string{AuditReasonEffectivePolicyDeny}
	}
}
