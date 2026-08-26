package risk

import (
	"sort"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
)

type Observation struct {
	Zone        string
	Kind        domain.ActivityKind
	At          time.Time
	Temperature float64
	HeatIndex   float64
}

type Decision struct {
	Level        domain.RiskLevel `json:"level"`
	Action       string           `json:"action"`
	MatchedRules []int64          `json:"matched_rule_ids"`
}

func Evaluate(rules []domain.RiskRule, observation Observation) Decision {
	matched := make([]domain.RiskRule, 0, len(rules))
	for _, rule := range rules {
		if rule.Applies(observation.Zone, observation.Kind, observation.At, observation.Temperature, observation.HeatIndex) {
			matched = append(matched, rule)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].Level.Rank() == matched[j].Level.Rank() {
			return matched[i].EffectiveFrom.After(matched[j].EffectiveFrom)
		}
		return matched[i].Level.Rank() > matched[j].Level.Rank()
	})
	decision := Decision{Level: domain.RiskLow, Action: "normal"}
	for _, rule := range matched {
		decision.MatchedRules = append(decision.MatchedRules, rule.ID)
	}
	if len(matched) > 0 {
		decision.Level = matched[0].Level
		decision.Action = matched[0].Action
	}
	return decision
}

func DepartureAllowed(decision Decision, hasHeatRestrictedParticipant bool) error {
	if decision.Level == domain.RiskExtreme {
		return domain.ErrRiskBlocked
	}
	if hasHeatRestrictedParticipant && decision.Level.Rank() >= domain.RiskHigh.Rank() {
		return domain.ErrRiskBlocked
	}
	return nil
}
