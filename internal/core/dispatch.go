package core

import (
	"crypto/sha256"
	"encoding/hex"
)

// DispatchRecord identifies a before-hook observation, not the parent's current
// assignment and not a claim that the child completed or even started.
type DispatchRecord struct {
	Runtime string `json:"runtime"`
	Session string `json:"session"`
	Segment string `json:"segment"`
	CallID  string `json:"call_id"`
	At      int64  `json:"at"`
}

func DispatchMarker(runtime, session, call string) (string, error) {
	for _, id := range []string{runtime, session, call} {
		if !runtimeID.MatchString(id) {
			return "", Fail("invalid_input", "dispatch lookup requires bounded runtime, session and call identifiers")
		}
	}
	sum := sha256.Sum256([]byte(runtime + "\x00" + session + "\x00" + call))
	return "dispatch-" + hex.EncodeToString(sum[:]), nil
}
