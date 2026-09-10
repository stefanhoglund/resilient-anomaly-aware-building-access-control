package model

import (
	"encoding/json"
	"fmt"
	"time"
)

// SpecVersion is the current version of the event payload formats. A
// producer stamps it on every envelope. A consumer that sees a higher
// value than it was built for logs and skips the message rather than
// guessing at a format it does not know.
const SpecVersion = 1

// Envelope is the single wrapper for every message on the event bus
// (ADR 0003). The payload lives in Data as raw JSON so the bus can route
// and store envelopes without knowing any payload type.
type Envelope struct {
	// ID is a ULID, unique per event. It is the de-duplication key: a
	// consumer that has already handled an ID ignores any redelivery.
	ID string `json:"id"`
	// Type is the dotted lower-case event type, e.g. "badge.read".
	Type string `json:"type"`
	// Source is the logical name of the producing service.
	Source string `json:"source"`
	// OccurredAt is event time in UTC — when the thing happened, not when
	// the message was sent. A delayed message keeps its original value,
	// which is how a consumer recognises it as stale.
	OccurredAt time.Time `json:"occurred_at"`
	// SpecVersion is the payload format version (see SpecVersion const).
	SpecVersion int `json:"spec_version"`
	// Data is the type-specific payload.
	Data json.RawMessage `json:"data"`
}

// NewEnvelope builds an envelope around payload, marshalling it to JSON,
// minting a ULID for the current time and stamping the current
// SpecVersion. occurredAt is the event time and is stored in UTC.
func NewEnvelope(source, eventType string, occurredAt time.Time, payload any) (Envelope, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("model: marshal %s payload: %w", eventType, err)
	}
	return Envelope{
		ID:          NewID(),
		Type:        eventType,
		Source:      source,
		OccurredAt:  occurredAt.UTC(),
		SpecVersion: SpecVersion,
		Data:        data,
	}, nil
}

// Validate checks the envelope is structurally sound. It does not check
// the payload — that is the job of the typed Decode below.
func (e Envelope) Validate() error {
	switch {
	case !ValidULID(e.ID):
		return fmt.Errorf("model: envelope id %q is not a valid ULID", e.ID)
	case e.Type == "":
		return fmt.Errorf("model: envelope has empty type")
	case e.Source == "":
		return fmt.Errorf("model: envelope %s has empty source", e.ID)
	case e.OccurredAt.IsZero():
		return fmt.Errorf("model: envelope %s has zero occurred_at", e.ID)
	case e.SpecVersion <= 0:
		return fmt.Errorf("model: envelope %s has non-positive spec_version %d", e.ID, e.SpecVersion)
	case len(e.Data) == 0:
		return fmt.Errorf("model: envelope %s has empty data", e.ID)
	}
	return nil
}

// Understandable reports whether a consumer built for the current
// SpecVersion should attempt to process this envelope. A newer
// spec_version or an unknown type is not an error — the caller logs it
// and moves on (ADR 0003).
func (e Envelope) Understandable() bool {
	return e.SpecVersion <= SpecVersion && KnownType(e.Type)
}

// Decode unmarshals an envelope's payload into T. It is generic so each
// consumer asks for exactly the type it expects:
//
//	br, err := model.Decode[model.BadgeRead](env)
func Decode[T any](e Envelope) (T, error) {
	var v T
	if err := json.Unmarshal(e.Data, &v); err != nil {
		return v, fmt.Errorf("model: decode %s payload as %T: %w", e.Type, v, err)
	}
	return v, nil
}
