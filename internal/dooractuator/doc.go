// Package dooractuator applies door commands to BuildSim.
//
// It is a pure command executor: it consumes model.DoorCommand, sets the
// door equipment's lock actuator in BuildSim, and publishes a
// model.DoorState reporting what happened — including applied=false with
// an error when BuildSim cannot be written after retries. It never
// decides anything; the decision service does that.
//
// In phase 1 (ADR 0003) this is the only writer to BuildSim, so it keeps
// the door equipment's full state in memory and does a whole-object PUT
// on each change (BuildSim has no field-level update). Temporal policy —
// relocking a door after an allow's TTL expires — is not done here; it
// belongs with the decision service and is deferred.
//
// Handle and EnsureReady are called from a single goroutine (the event
// subscription loop), so the type carries no locks.
package dooractuator
