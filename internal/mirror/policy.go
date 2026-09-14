package mirror

import "slices"

// PortRange is an inclusive range of TCP ports.
type PortRange struct {
	Start uint16 `json:"start"`
	End   uint16 `json:"end"`
}

// PortPolicy identifies ports that discovery should report but not mirror.
type PortPolicy struct {
	excluded []PortRange
}

// NewPortPolicy creates a policy from resolved excluded ranges.
func NewPortPolicy(excluded []PortRange) PortPolicy {
	return PortPolicy{excluded: slices.Clone(excluded)}
}

// ExcludedRanges returns the resolved ranges excluded by this policy.
func (p PortPolicy) ExcludedRanges() []PortRange {
	return slices.Clone(p.excluded)
}

// DefaultPortPolicy excludes the system port range.
func DefaultPortPolicy() PortPolicy {
	return NewPortPolicy([]PortRange{{Start: 1, End: 1023}})
}

// Excludes reports whether a port is excluded from mirroring.
func (p PortPolicy) Excludes(port uint16) bool {
	for _, portRange := range p.excluded {
		if port >= portRange.Start && port <= portRange.End {
			return true
		}
	}
	return false
}
