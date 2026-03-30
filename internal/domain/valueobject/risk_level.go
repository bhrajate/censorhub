package valueobject

import "fmt"

// RiskLevel 风险等级值对象
type RiskLevel int

const (
	RiskLow      RiskLevel = 1
	RiskMedium   RiskLevel = 2
	RiskHigh     RiskLevel = 3
	RiskCritical RiskLevel = 4
)

func (r RiskLevel) IsValid() bool {
	return r >= RiskLow && r <= RiskCritical
}

func (r RiskLevel) String() string {
	switch r {
	case RiskLow:
		return "low"
	case RiskMedium:
		return "medium"
	case RiskHigh:
		return "high"
	case RiskCritical:
		return "critical"
	default:
		return "unknown"
	}
}

func ParseRiskLevel(level int) (RiskLevel, error) {
	r := RiskLevel(level)
	if !r.IsValid() {
		return 0, fmt.Errorf("invalid risk level: %d, must be 1-4", level)
	}
	return r, nil
}
