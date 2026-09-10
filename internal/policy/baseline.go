package policy

import (
	"fmt"
	"time"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/model"
)

// UnlockTTL is how long an "allow" leaves the door unlocked before it
// relocks on its own. Short, because access is per badge-read.
const UnlockTTL = 5 * time.Second

// PolicyRule names identify which truth-table row produced a decision.
// They are recorded in DecisionEvidence.PolicyRule.
const (
	RulePermittedInWindow   = "permitted-in-window"
	RuleOutsideWindow       = "permitted-outside-window"
	RuleNotPermitted        = "not-permitted"
	RuleRestrictedViolation = "restricted-room-violation"
)

// Request is the input to one baseline decision.
type Request struct {
	// CardID, DoorID and RoomID identify the badge read. RoomID is the
	// room the door controls entry to.
	CardID string
	DoorID string
	RoomID string
	// OccurredAt is the event time of the badge read; the access window
	// is checked against it.
	OccurredAt time.Time
	// Occupancy is the room's most recent occupancy observation, if the
	// caller has one. It is recorded as evidence but does not change the
	// phase-1 decision (the occupancy-contradiction check in ADR 0003 is
	// deferred).
	Occupancy *model.OccupancyObserved
}

// Validate checks the request is complete enough to decide on.
func (r Request) Validate() error {
	switch {
	case r.CardID == "":
		return fmt.Errorf("policy: request has no card id")
	case r.DoorID == "":
		return fmt.Errorf("policy: request has no door id")
	case r.RoomID == "":
		return fmt.Errorf("policy: request has no room id")
	case r.OccurredAt.IsZero():
		return fmt.Errorf("policy: request has zero occurred_at")
	}
	return nil
}

// Decide applies the deterministic baseline policy (ADR 0003).
//
// It returns the access decision and the door command that decision
// implies. DecisionID and CommandID are left empty: the caller mints
// them (it already mints envelope ids) and links the command to the
// decision. Decide never errors — an unknown card or room simply resolves
// to "not permitted".
//
// Truth table:
//
//	permitted && in-window                -> allow    (unlock, TTL)
//	permitted && !in-window               -> restrict (lock)
//	!permitted && room restricted         -> alert    (lock)
//	!permitted && room not restricted     -> restrict (lock)
//
// A card that is permitted for a restricted room within its window is
// still allowed: restriction only escalates a *violation* to an alert.
func Decide(cfg model.PolicyConfig, req Request) (model.AccessDecision, model.DoorCommand) {
	permitted := cfg.CardPermitted(req.CardID, req.RoomID)
	inWindow := cfg.WithinWindow(req.RoomID, req.OccurredAt)
	restricted := cfg.RoomRestricted(req.RoomID)

	ev := model.DecisionEvidence{
		CardPermitted:  permitted,
		WithinWindow:   inWindow,
		RoomRestricted: restricted,
	}
	if req.Occupancy != nil {
		present := req.Occupancy.Present
		ev.ObservedPresent = &present
	}

	var (
		action model.AccessAction
		reason string
		lock   model.LockState
		ttl    int
	)

	switch {
	case permitted && inWindow:
		action, reason = model.ActionAllow, "card permitted and within access window"
		lock, ttl = model.LockUnlocked, int(UnlockTTL.Seconds())
		ev.PolicyRule = RulePermittedInWindow
	case permitted && !inWindow:
		action, reason = model.ActionRestrict, "card permitted but outside access window"
		lock = model.LockLocked
		ev.PolicyRule = RuleOutsideWindow
	case !permitted && restricted:
		action, reason = model.ActionAlert, "no permission for a restricted room"
		lock = model.LockLocked
		ev.PolicyRule = RuleRestrictedViolation
	default: // !permitted && !restricted
		action, reason = model.ActionRestrict, "card not permitted for room"
		lock = model.LockLocked
		ev.PolicyRule = RuleNotPermitted
	}

	decision := model.AccessDecision{
		CardID:         req.CardID,
		DoorID:         req.DoorID,
		RoomID:         req.RoomID,
		Action:         action,
		Reason:         reason,
		Evidence:       ev,
		BaselineAction: action, // phase 1: baseline is the decision
	}
	command := model.DoorCommand{
		DoorID:      req.DoorID,
		DesiredLock: lock,
		TTLSeconds:  ttl,
	}
	return decision, command
}
