package risk

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
)

func TestEvaluateChoosesHighestApplicableRule(t *testing.T) {
	at := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	rules := []domain.RiskRule{
		{ID: 1, Zone: "river", ActivityKind: domain.ActivityRun, Level: domain.RiskGuarded, TemperatureMin: 30, HeatIndexMin: 34, EffectiveFrom: at.Add(-time.Hour), EffectiveTo: at.Add(time.Hour), Action: "extra_water"},
		{ID: 2, Zone: "river", ActivityKind: domain.ActivityRun, Level: domain.RiskExtreme, TemperatureMin: 36, HeatIndexMin: 43, EffectiveFrom: at.Add(-time.Hour), EffectiveTo: at.Add(time.Hour), Action: "cancel"},
		{ID: 3, Zone: "river", ActivityKind: domain.ActivityRun, Level: domain.RiskHigh, TemperatureMin: 34, HeatIndexMin: 40, EffectiveFrom: at.Add(-time.Hour), EffectiveTo: at.Add(time.Hour), Action: "shorten"},
		{ID: 4, Zone: "other", ActivityKind: domain.ActivityRun, Level: domain.RiskExtreme, TemperatureMin: 10, HeatIndexMin: 10, EffectiveFrom: at.Add(-time.Hour), EffectiveTo: at.Add(time.Hour), Action: "unrelated"},
	}
	decision := Evaluate(rules, Observation{Zone: "river", Kind: domain.ActivityRun, At: at, Temperature: 37, HeatIndex: 45})
	if decision.Level != domain.RiskExtreme || decision.Action != "cancel" {
		t.Fatalf("unexpected decision: %+v", decision)
	}
	if !reflect.DeepEqual(decision.MatchedRules, []int64{2, 3, 1}) {
		t.Fatalf("matched order = %v", decision.MatchedRules)
	}
}

func TestEvaluateUsesNewestRuleAtSameSeverity(t *testing.T) {
	at := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	rules := []domain.RiskRule{
		{ID: 1, Zone: "river", ActivityKind: domain.ActivityCycling, Level: domain.RiskHigh, EffectiveFrom: at.Add(-2 * time.Hour), EffectiveTo: at.Add(time.Hour), Action: "old"},
		{ID: 2, Zone: "river", ActivityKind: domain.ActivityCycling, Level: domain.RiskHigh, EffectiveFrom: at.Add(-time.Hour), EffectiveTo: at.Add(time.Hour), Action: "new"},
	}
	decision := Evaluate(rules, Observation{Zone: "river", Kind: domain.ActivityCycling, At: at, Temperature: 40, HeatIndex: 50})
	if decision.Action != "new" || decision.MatchedRules[0] != 2 {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestEvaluateDefaultsToLowRisk(t *testing.T) {
	decision := Evaluate(nil, Observation{})
	if decision.Level != domain.RiskLow || decision.Action != "normal" || len(decision.MatchedRules) != 0 {
		t.Fatalf("unexpected default: %+v", decision)
	}
}

func TestDepartureAllowed(t *testing.T) {
	tests := []struct {
		name       string
		level      domain.RiskLevel
		restricted bool
		blocked    bool
	}{
		{"low unrestricted", domain.RiskLow, false, false},
		{"guarded restricted", domain.RiskGuarded, true, false},
		{"high unrestricted", domain.RiskHigh, false, false},
		{"high restricted", domain.RiskHigh, true, true},
		{"extreme unrestricted", domain.RiskExtreme, false, true},
		{"extreme restricted", domain.RiskExtreme, true, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := DepartureAllowed(Decision{Level: test.level}, test.restricted)
			if test.blocked && !errors.Is(err, domain.ErrRiskBlocked) {
				t.Fatalf("expected risk block, got %v", err)
			}
			if !test.blocked && err != nil {
				t.Fatalf("unexpected block: %v", err)
			}
		})
	}
}
