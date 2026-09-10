package buildsim

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Sensor data types accepted by BuildSim (ADR 0002). There is no numeric
// type: carry numbers as Text with a Unit, or reduce to a Binary flag.
const (
	DataTypeBinary = "binary"
	DataTypeText   = "text"
)

// Sentinel errors for the status codes the equipment API uses to signal
// outcomes rather than failures.
var (
	ErrEquipmentNotFound = errors.New("buildsim: equipment not found")
	ErrEquipmentExists   = errors.New("buildsim: equipment already exists")
)

// Sensor is one reading channel on an Equipment.
type Sensor struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Type        string `json:"type,omitempty"`
	DataType    string `json:"data_type"`
	Value       string `json:"value"`        // used when DataType == DataTypeText
	BinaryValue bool   `json:"binary_value"` // used when DataType == DataTypeBinary
	Unit        string `json:"unit,omitempty"`
	// Timestamp is set by BuildSim on every write; ignored on input.
	Timestamp string `json:"timestamp,omitempty"`
}

// Actuator is one controllable output on an Equipment.
type Actuator struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
	// State is a free-form string whose meaning is the actuator's own.
	State string `json:"state"`
	// Timestamp is set by BuildSim on every write; ignored on input.
	Timestamp string `json:"timestamp,omitempty"`
}

// Equipment is a device attached to a room: a door, a badge reader, a
// motion sensor. It carries its sensors and actuators.
type Equipment struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Type     string `json:"type,omitempty"`
	Category string `json:"category,omitempty"`
	Status   string `json:"status,omitempty"`
	// Level and Room must both name real building elements and are
	// required on every create and replace.
	Level string `json:"level"`
	Room  string `json:"room"`
	// Version is BuildSim's global monotonic mutation counter at the time
	// this object was last written. Server-owned; ignored on input.
	Version   int        `json:"version,omitempty"`
	Sensors   []Sensor   `json:"sensors"`
	Actuators []Actuator `json:"actuators"`
}

// Sensor returns the sensor with the given id and whether it was found.
func (e Equipment) Sensor(id string) (Sensor, bool) {
	for _, s := range e.Sensors {
		if s.ID == id {
			return s, true
		}
	}
	return Sensor{}, false
}

// Actuator returns the actuator with the given id and whether it was found.
func (e Equipment) Actuator(id string) (Actuator, bool) {
	for _, a := range e.Actuators {
		if a.ID == id {
			return a, true
		}
	}
	return Actuator{}, false
}

func (e Equipment) validateForWrite() error {
	switch {
	case e.ID == "":
		return fmt.Errorf("buildsim: equipment has no id")
	case e.Level == "":
		return fmt.Errorf("buildsim: equipment %q has no level", e.ID)
	case e.Room == "":
		return fmt.Errorf("buildsim: equipment %q has no room", e.ID)
	}
	for _, s := range e.Sensors {
		if s.ID == "" {
			return fmt.Errorf("buildsim: equipment %q has a sensor with no id", e.ID)
		}
		if s.DataType != DataTypeBinary && s.DataType != DataTypeText {
			return fmt.Errorf("buildsim: sensor %q has invalid data_type %q", s.ID, s.DataType)
		}
	}
	for _, a := range e.Actuators {
		if a.ID == "" {
			return fmt.Errorf("buildsim: equipment %q has an actuator with no id", e.ID)
		}
	}
	return nil
}

// ListEquipment returns every equipment BuildSim currently holds.
func (c *Client) ListEquipment(ctx context.Context) ([]Equipment, error) {
	resp, err := c.request(ctx, http.MethodGet, "/api/equipment", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, http.MethodGet, "/api/equipment")
	}
	var out []Equipment
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("buildsim: decode equipment list: %w", err)
	}
	return out, nil
}

// GetEquipment fetches one equipment by id. A missing equipment returns
// an error wrapping ErrEquipmentNotFound.
func (c *Client) GetEquipment(ctx context.Context, id string) (Equipment, error) {
	path := "/api/equipment/" + url.PathEscape(id)
	resp, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return Equipment{}, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return decodeEquipment(resp.Body, id)
	case http.StatusNotFound:
		return Equipment{}, fmt.Errorf("%w: %s", ErrEquipmentNotFound, id)
	default:
		return Equipment{}, statusError(resp, http.MethodGet, path)
	}
}

// CreateEquipment POSTs a new equipment. An existing id returns an error
// wrapping ErrEquipmentExists.
func (c *Client) CreateEquipment(ctx context.Context, eq Equipment) (Equipment, error) {
	if err := eq.validateForWrite(); err != nil {
		return Equipment{}, err
	}
	resp, err := c.request(ctx, http.MethodPost, "/api/equipment", eq)
	if err != nil {
		return Equipment{}, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		return decodeEquipment(resp.Body, eq.ID)
	case http.StatusConflict:
		return Equipment{}, fmt.Errorf("%w: %s", ErrEquipmentExists, eq.ID)
	default:
		return Equipment{}, statusError(resp, http.MethodPost, "/api/equipment")
	}
}

// EnsureEquipment creates eq if it does not exist and otherwise leaves
// the existing equipment untouched. It tolerates a create/create race
// (409) by fetching the current state. Use it at startup to register a
// device without disturbing a value another process already set.
func (c *Client) EnsureEquipment(ctx context.Context, eq Equipment) (Equipment, error) {
	created, err := c.CreateEquipment(ctx, eq)
	if err == nil {
		return created, nil
	}
	if errors.Is(err, ErrEquipmentExists) {
		return c.GetEquipment(ctx, eq.ID)
	}
	return Equipment{}, err
}

// PutEquipment replaces the equipment identified by eq.ID with eq. This
// is a whole-object replace — BuildSim has no field-level update — so eq
// must carry the complete intended state, including Level and Room. The
// returned object has BuildSim's new global Version.
func (c *Client) PutEquipment(ctx context.Context, eq Equipment) (Equipment, error) {
	if err := eq.validateForWrite(); err != nil {
		return Equipment{}, err
	}
	path := "/api/equipment/" + url.PathEscape(eq.ID)
	resp, err := c.request(ctx, http.MethodPut, path, eq)
	if err != nil {
		return Equipment{}, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return decodeEquipment(resp.Body, eq.ID)
	case http.StatusNotFound:
		return Equipment{}, fmt.Errorf("%w: %s", ErrEquipmentNotFound, eq.ID)
	default:
		return Equipment{}, statusError(resp, http.MethodPut, path)
	}
}

// DeleteEquipment removes an equipment. A missing equipment is treated as
// success, so the call is idempotent.
func (c *Client) DeleteEquipment(ctx context.Context, id string) error {
	path := "/api/equipment/" + url.PathEscape(id)
	resp, err := c.request(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound:
		return nil
	default:
		return statusError(resp, http.MethodDelete, path)
	}
}

func decodeEquipment(r io.Reader, id string) (Equipment, error) {
	var eq Equipment
	if err := json.NewDecoder(r).Decode(&eq); err != nil {
		return Equipment{}, fmt.Errorf("buildsim: decode equipment %s: %w", id, err)
	}
	return eq, nil
}

// request performs one HTTP call to BuildSim, marshalling body to JSON
// when non-nil. The caller owns resp.Body.
func (c *Client) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("buildsim: marshal %s %s body: %w", method, path, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("buildsim: build %s %s: %w", method, path, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// Covers connection refused, DNS failure, TLS errors and
		// context deadline/cancellation.
		return nil, fmt.Errorf("buildsim: %s %s: %w", method, path, err)
	}
	return resp, nil
}

func statusError(resp *http.Response, method, path string) error {
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	return fmt.Errorf("buildsim: %s %s: unexpected status %s: %s",
		method, path, resp.Status, strings.TrimSpace(string(snippet)))
}
