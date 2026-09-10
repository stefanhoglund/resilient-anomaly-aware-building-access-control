package eventbus

import (
	"testing"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/model"
)

func env(id string) model.Envelope {
	return model.Envelope{ID: id, Type: model.TypeBadgeRead}
}

func TestRing_EvictsOldest(t *testing.T) {
	r := newRing(3)
	for _, id := range []string{"01A", "01B", "01C", "01D"} {
		r.add(env(id))
	}
	if got := r.oldestID(); got != "01B" {
		t.Fatalf("oldestID = %q, want 01B", got)
	}
	got := ids(r.after("01A"))
	want := []string{"01B", "01C", "01D"}
	if !equal(got, want) {
		t.Fatalf("after(01A) = %v, want %v", got, want)
	}
}

func TestRing_AfterFiltersById(t *testing.T) {
	r := newRing(10)
	for _, id := range []string{"01A", "01B", "01C"} {
		r.add(env(id))
	}
	if got := ids(r.after("01B")); !equal(got, []string{"01C"}) {
		t.Fatalf("after(01B) = %v, want [01C]", got)
	}
	if got := r.after(""); got != nil {
		t.Fatalf("after(\"\") = %v, want nil", got)
	}
	if got := ids(r.after("01Z")); got != nil {
		t.Fatalf("after(future id) = %v, want nil", got)
	}
}

func ids(evs []model.Envelope) []string {
	if evs == nil {
		return nil
	}
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
