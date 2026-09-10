package buildsim

// Health is the payload of GET /healthz.
type Health struct {
	Status string `json:"status"`
}

// OK reports whether BuildSim considers itself healthy.
func (h Health) OK() bool { return h.Status == "ok" }

// Level is one storey of the building.
type Level struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Building is the payload of GET /api/building.
type Building struct {
	Name   string  `json:"name"`
	Levels []Level `json:"levels"`
}
