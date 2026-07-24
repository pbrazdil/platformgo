package testkit

// Faults exposes explicitly armed semantic failpoints without timing or
// process-global state.
type Faults struct {
	armed map[string]struct{}
}

// NewFaults creates a fault set containing the named semantic boundaries.
func NewFaults(names ...string) *Faults {
	armed := make(map[string]struct{}, len(names))
	for _, name := range names {
		armed[name] = struct{}{}
	}
	return &Faults{armed: armed}
}

// Reached reports whether the named semantic boundary is armed.
func (faults *Faults) Reached(name string) bool {
	if faults == nil {
		return false
	}
	_, ok := faults.armed[name]
	return ok
}
