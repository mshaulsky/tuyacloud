package tuyacloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
)

// The wire models — Device, DataPoint, Specification, Definition, Command,
// Token and the page wrappers — are generated into types_gen.go from
// openapi.yaml, the package's specification of the Tuya endpoints it uses.
// This file adds the behaviour on top of them.

// Status is a set of data points, the code/value pairs a device reports.
type Status []DataPoint

// maxStatusBatch is the largest number of devices the platform reads in one
// batch status request.
const maxStatusBatch = 20

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

// Devices lists every device of every app account linked to the cloud project.
// It is the discovery call: run it once to learn the device IDs, then poll
// those directly.
//
// The legacy endpoint for this (/v1.0/users/{uid}/devices) is closed to newly
// created projects — it answers 1106 "permission deny" even with every service
// authorised — so this walks the associated-users listing instead, the same
// route the platform still grants.
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	var all []Device
	query := url.Values{"size": {"50"}}
	for {
		var page AssociatedDevicesPage
		if err := c.Get(ctx, "/v1.0/iot-01/associated-users/devices", query, &page); err != nil {
			return nil, fmt.Errorf("tuyacloud: list devices: %w", err)
		}
		all = append(all, page.Devices...)
		if !page.HasMore || page.LastRowKey == "" {
			return all, nil
		}
		// A cursor that does not advance would loop forever on a buggy
		// backend, bounded only by the caller's context.
		if page.LastRowKey == query.Get("last_row_key") {
			return nil, fmt.Errorf("tuyacloud: list devices: the platform repeated page cursor %q", page.LastRowKey)
		}
		query.Set("last_row_key", page.LastRowKey)
	}
}

// DevicesByUser lists the devices of one app user, for projects with several
// linked accounts. The user ID is shown next to the linked account in the
// platform console, and Devices reports it in each Device's UID; the listing
// this method uses does not itself fill UID in.
func (c *Client) DevicesByUser(ctx context.Context, uid string) ([]Device, error) {
	if uid == "" {
		return nil, errors.New("tuyacloud: user ID is required")
	}
	var all []Device
	query := url.Values{
		"page_size":   {"75"},
		"source_type": {"tuyaUser"},
		"source_id":   {uid},
	}
	for {
		var page UserDevicesPage
		if err := c.Get(ctx, "/v1.3/iot-03/devices", query, &page); err != nil {
			return nil, fmt.Errorf("tuyacloud: list devices of user %s: %w", uid, err)
		}
		all = append(all, page.List...)
		if !page.HasMore || page.LastRowKey == "" {
			return all, nil
		}
		if page.LastRowKey == query.Get("last_row_key") {
			return nil, fmt.Errorf("tuyacloud: list devices of user %s: the platform repeated page cursor %q", uid, page.LastRowKey)
		}
		query.Set("last_row_key", page.LastRowKey)
	}
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

// DevicesStatus reads the current data points of several devices in one round
// trip, keyed by device ID — the preferred call for a polling loop, where it
// replaces a request per device with one per dashboard. The platform caps a
// request at 20 devices, so longer lists are fetched in chunks transparently.
//
// An ID the platform does not recognise is simply absent from the map rather
// than an error, so callers should mind the second return value of a lookup.
func (c *Client) DevicesStatus(ctx context.Context, deviceIDs ...string) (map[string]Status, error) {
	if len(deviceIDs) == 0 {
		return nil, errors.New("tuyacloud: at least one device ID is required")
	}
	all := make(map[string]Status, len(deviceIDs))
	for start := 0; start < len(deviceIDs); start += maxStatusBatch {
		chunk := deviceIDs[start:min(start+maxStatusBatch, len(deviceIDs))]
		var entries []DeviceStatusEntry
		query := url.Values{"device_ids": {strings.Join(chunk, ",")}}
		if err := c.Get(ctx, "/v1.0/iot-03/devices/status", query, &entries); err != nil {
			return nil, fmt.Errorf("tuyacloud: get status of %d device(s): %w", len(chunk), err)
		}
		for _, e := range entries {
			all[e.ID] = e.Status
		}
	}
	return all, nil
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
	body := CommandsRequest{Commands: commands}
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

// Get returns the definition with the given code, searching the readable data
// points first and the writable ones second.
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
