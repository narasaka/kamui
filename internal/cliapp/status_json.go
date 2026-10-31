package cliapp

import (
	"encoding/json"
	"io"

	"github.com/narasaka/kamui/internal/session"
)

// statusJSONFormat changes only when a field is removed or changes meaning.
// Adding fields does not change it.
const statusJSONFormat = 1

type statusJSON struct {
	Format   int           `json:"format"`
	Sessions []sessionJSON `json:"sessions"`
}

type sessionJSON struct {
	Destination string     `json:"destination"`
	State       string     `json:"state"`
	LastError   string     `json:"lastError,omitempty"`
	Browser     string     `json:"browser,omitempty"`
	Mirror      mirrorJSON `json:"mirror"`
}

type mirrorJSON struct {
	Enabled       bool           `json:"enabled"`
	MirroredPorts []uint16       `json:"mirroredPorts"`
	ExcludedPorts []uint16       `json:"excludedPorts"`
	Conflicts     []conflictJSON `json:"conflicts"`
	LastError     string         `json:"lastError,omitempty"`
}

type conflictJSON struct {
	Port  uint16 `json:"port"`
	Error string `json:"error,omitempty"`
}

func printStatusJSON(writer io.Writer, statuses []session.SessionStatus) error {
	document := statusJSON{Format: statusJSONFormat, Sessions: make([]sessionJSON, 0, len(statuses))}
	for _, status := range statuses {
		entry := sessionJSON{
			Destination: status.Destination.String(),
			State:       sessionState(status.State),
			Browser:     status.Browser,
			Mirror: mirrorJSON{
				Enabled:       status.Mirror.Enabled,
				MirroredPorts: append([]uint16{}, status.Mirror.MirroredPorts...),
				ExcludedPorts: append([]uint16{}, status.Mirror.ExcludedPorts...),
				Conflicts:     make([]conflictJSON, 0, len(status.Mirror.ConflictedPorts)),
			},
		}
		if status.LastError != nil {
			entry.LastError = status.LastError.Error()
		}
		if status.Mirror.LastError != nil {
			entry.Mirror.LastError = status.Mirror.LastError.Error()
		}
		for _, conflict := range status.Mirror.ConflictedPorts {
			entry.Mirror.Conflicts = append(entry.Mirror.Conflicts, conflictJSON{Port: conflict.Port, Error: conflict.Error})
		}
		document.Sessions = append(document.Sessions, entry)
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(document)
}
