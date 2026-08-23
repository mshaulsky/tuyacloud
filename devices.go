package tuyacloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
)

// Device describes one device registered to the cloud project. Sensors behind
// a gateway — Bluetooth or Zigbee — appear as ordinary devices with Sub set.
type Device struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	Online      bool   `json:"online"`
	Sub         bool   `json:"sub"` // reached through a gateway rather than directly
	LocalKey    string `json:"local_key"`
	IP          string `json:"ip"`
	TimeZone    string `json:"time_zone"`
	ActiveTime  int64  `json:"active_time"`
	UpdateTime  int64  `json:"update_time"`
	Status      Status `json:"status"`
}

// Status is a set of data points, the code/value pairs a device reports.
type Status []DataPoint

// DataPoint is one reported value. Tuya types values per product, so the value
// stays raw JSON until the caller asks for a concrete type.
type DataPoint struct {
	Code  string          `json:"code"`
	Value json.RawMessage `json:"value"`
}

// Specification lists the data points a product can report and accept, with
// the metadata needed to interpret them.
type Specification struct {
	Category  string       `json:"category"`
	Functions []Definition `json:"functions"` // writable, via SendCommands
	Status    []Definition `json:"status"`    // readable, via DeviceStatus
}

// Definition describes one data point of a product.
type Definition struct {
	Code string `json:"code"`
	Type string `json:"type"` // Integer, Boolean, Enum, String or Json
	// Values holds the type-specific constraints, itself encoded as a JSON
	// string. Numeric ones are easier to read through Range.
	Values string `json:"values"`
}

// Range is the constraint set of an Integer data point. Values are reported as
// integers scaled by a power of ten: a temperature of 23.5 °C arrives as 235
// with Scale 1.
type Range struct {
	Unit  string  `json:"unit"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Scale int     `json:"scale"`
	Step  float64 `json:"step"`
}

// Command is one instruction for a device.
type Command struct {
	Code  string `json:"code"`
	Value any    `json:"value"`
}

// Devices lists every device linked to the app account behind the cloud
// project. It is the discovery call: run it once to learn the device IDs, then
// poll those directly.
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	uid, err := c.ensureUID(ctx)
	if err != nil {
		return nil, err
	}
	return c.DevicesByUser(ctx, uid)
}

// DevicesByUser lists the devices of one app user, for projects where the user
// ID is known independently.
func (c *Client) DevicesByUser(ctx context.Context, uid string) ([]Device, error) {
	if uid == "" {
		return nil, errors.New("tuyacloud: user ID is required")
	}
	var devices []Device
	if err := c.Get(ctx, "/v1.0/users/"+url.PathEscape(uid)+"/devices", nil, &devices); err != nil {
		return nil, fmt.Errorf("tuyacloud: list devices of user %s: %w", uid, err)
	}
	return devices, nil
}

// Device fetches one device, including its current status.
func (c *Client) Device(ctx context.Context, deviceID string) (Device, error) {
	var d Device
	if err := c.Get(ctx, "/v1.0/devices/"+url.PathEscape(deviceID), nil, &d); err != nil {
		return Device{}, fmt.Errorf("tuyacloud: get device %s: %w", deviceID, err)
	}
	return d, nil
}

// DeviceStatus reads the current data points of a device. This is the call a
// polling loop repeats.
func (c *Client) DeviceStatus(ctx context.Context, deviceID string) (Status, error) {
	var s Status
	if err := c.Get(ctx, "/v1.0/devices/"+url.PathEscape(deviceID)+"/status", nil, &s); err != nil {
		return nil, fmt.Errorf("tuyacloud: get status of device %s: %w", deviceID, err)
	}
	return s, nil
}

// Specification describes what a device can report and accept. It rarely
// changes, so fetch it once and cache it: it carries the scale needed to turn
// raw integers into real units.
func (c *Client) Specification(ctx context.Context, deviceID string) (Specification, error) {
	var spec Specification
	if err := c.Get(ctx, "/v1.0/devices/"+url.PathEscape(deviceID)+"/specifications", nil, &spec); err != nil {
		return Specification{}, fmt.Errorf("tuyacloud: get specification of device %s: %w", deviceID, err)
	}
	return spec, nil
}

// SendCommands issues one or more instructions to a device.
func (c *Client) SendCommands(ctx context.Context, deviceID string, commands ...Command) error {
	if len(commands) == 0 {
		return errors.New("tuyacloud: at least one command is required")
	}
	body := struct {
		Commands []Command `json:"commands"`
	}{Commands: commands}
	if err := c.Post(ctx, "/v1.0/devices/"+url.PathEscape(deviceID)+"/commands", body, nil); err != nil {
		return fmt.Errorf("tuyacloud: send commands to device %s: %w", deviceID, err)
	}
	return nil
}

// Get returns the data point with the given code.
func (s Status) Get(code string) (DataPoint, bool) {
	for _, dp := range s {
		if dp.Code == code {
			return dp, true
		}
	}
	return DataPoint{}, false
}

// Int decodes an integer data point, the type most sensors report.
func (d DataPoint) Int() (int64, error) {
	var v int64
	if err := json.Unmarshal(d.Value, &v); err != nil {
		return 0, fmt.Errorf("data point %s: not an integer (%s): %w", d.Code, d.Value, err)
	}
	return v, nil
}

// Float decodes a numeric data point without applying any scale.
func (d DataPoint) Float() (float64, error) {
	var v float64
	if err := json.Unmarshal(d.Value, &v); err != nil {
		return 0, fmt.Errorf("data point %s: not a number (%s): %w", d.Code, d.Value, err)
	}
	return v, nil
}

// Bool decodes a boolean data point, such as a switch or an alarm flag.
func (d DataPoint) Bool() (bool, error) {
	var v bool
	if err := json.Unmarshal(d.Value, &v); err != nil {
		return false, fmt.Errorf("data point %s: not a boolean (%s): %w", d.Code, d.Value, err)
	}
	return v, nil
}

// Text decodes a string data point, such as an enum member.
func (d DataPoint) Text() (string, error) {
	var v string
	if err := json.Unmarshal(d.Value, &v); err != nil {
		return "", fmt.Errorf("data point %s: not a string (%s): %w", d.Code, d.Value, err)
	}
	return v, nil
}

// Get returns the definition with the given code, searching both the readable
// and the writable data points.
func (s Specification) Get(code string) (Definition, bool) {
	for _, list := range [][]Definition{s.Status, s.Functions} {
		for _, def := range list {
			if def.Code == code {
				return def, true
			}
		}
	}
	return Definition{}, false
}

// Range parses the constraints of an Integer data point.
func (d Definition) Range() (Range, error) {
	var r Range
	if err := json.Unmarshal([]byte(d.Values), &r); err != nil {
		return Range{}, fmt.Errorf("data point %s: parse values %q: %w", d.Code, d.Values, err)
	}
	return r, nil
}

// Scaled converts a raw reported integer into its real value, applying the
// power-of-ten scale: Scaled(235) is 23.5 when Scale is 1.
func (r Range) Scaled(raw int64) float64 {
	return float64(raw) / math.Pow10(r.Scale)
}

// ensureUID returns the app user ID that came with the access token, fetching
// a token first if necessary.
func (c *Client) ensureUID(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.uid == "" || !now().Before(c.tokenExp) {
		if _, err := c.fetchToken(ctx); err != nil {
			return "", err
		}
	}
	if c.uid == "" {
		return "", errors.New("tuyacloud: the access token carries no user ID; " +
			"link an app account to the cloud project, or call DevicesByUser with an explicit ID")
	}
	return c.uid, nil
}
