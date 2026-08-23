package tuyacloud

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestClientDevices(t *testing.T) {
	pinClock(t)

	tests := []struct {
		name      string
		platform  *platform
		want      []Device
		wantCalls []call
		wantErr   string
	}{
		{
			name: "lists the devices of the linked account",
			platform: &platform{
				tokenUID: "user42",
				handler: jsonHandler(`{"success":true,"result":[
					{"id":"dev1","name":"Bedroom sensor","category":"wsdcg","online":true,"sub":true},
					{"id":"dev2","name":"Kettle","category":"kg","online":false}
				]}`),
			},
			want: []Device{
				{ID: "dev1", Name: "Bedroom sensor", Category: "wsdcg", Online: true, Sub: true},
				{ID: "dev2", Name: "Kettle", Category: "kg"},
			},
			wantCalls: []call{{method: http.MethodGet, path: "/v1.0/users/user42/devices"}},
		},
		{
			name:     "a token without a user id explains what to do",
			platform: &platform{tokenUID: ""},
			wantErr:  "carries no user ID",
		},
		{
			name: "an api error is wrapped with the user",
			platform: &platform{
				tokenUID: "user42",
				handler:  jsonHandler(`{"success":false,"code":1106,"msg":"permission deny"}`),
			},
			wantErr: "list devices of user user42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, p := newPlatform(t, tt.platform)

			got, err := c.Devices(t.Context())
			if !checkErr(t, err, tt.wantErr) {
				return
			}
			assertDevices(t, got, tt.want)
			assertCalls(t, p.requests(false), tt.wantCalls)
		})
	}
}

func TestClientDevicesByUser(t *testing.T) {
	pinClock(t)

	tests := []struct {
		name      string
		uid       string
		platform  *platform
		want      []Device
		wantCalls []call
		wantErr   string
	}{
		{
			name:      "requests the devices of the given user",
			uid:       "user7",
			platform:  &platform{handler: jsonHandler(`{"success":true,"result":[{"id":"dev1"}]}`)},
			want:      []Device{{ID: "dev1"}},
			wantCalls: []call{{method: http.MethodGet, path: "/v1.0/users/user7/devices"}},
		},
		{
			name:     "a user id with a slash stays one path segment",
			uid:      "a/b",
			platform: &platform{handler: jsonHandler(`{"success":true,"result":[]}`)},
			want:     []Device{},
			wantCalls: []call{{
				method:  http.MethodGet,
				escaped: "/v1.0/users/a%2Fb/devices",
			}},
		},
		{
			name:    "an empty user id is refused",
			uid:     "",
			wantErr: "user ID is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, p := newPlatform(t, tt.platform)

			got, err := c.DevicesByUser(t.Context(), tt.uid)
			if !checkErr(t, err, tt.wantErr) {
				return
			}
			assertDevices(t, got, tt.want)
			assertCalls(t, p.requests(false), tt.wantCalls)
		})
	}
}

func TestClientDevice(t *testing.T) {
	pinClock(t)

	tests := []struct {
		name      string
		platform  *platform
		want      Device
		wantCalls []call
		wantErr   string
	}{
		{
			name: "returns the device with its status",
			platform: &platform{handler: jsonHandler(`{"success":true,"result":{
				"id":"dev1","name":"Bedroom sensor","online":true,
				"status":[{"code":"va_temperature","value":235}]
			}}`)},
			want: Device{
				ID: "dev1", Name: "Bedroom sensor", Online: true,
				Status: Status{{Code: "va_temperature", Value: json.RawMessage("235")}},
			},
			wantCalls: []call{{method: http.MethodGet, path: "/v1.0/devices/dev1"}},
		},
		{
			name:     "an unknown device is reported with its id",
			platform: &platform{handler: jsonHandler(`{"success":false,"code":1000,"msg":"device not exist"}`)},
			wantErr:  "get device dev1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, p := newPlatform(t, tt.platform)

			got, err := c.Device(t.Context(), "dev1")
			if !checkErr(t, err, tt.wantErr) {
				return
			}
			assertDevices(t, []Device{got}, []Device{tt.want})
			assertCalls(t, p.requests(false), tt.wantCalls)
		})
	}
}

func TestClientDeviceStatus(t *testing.T) {
	pinClock(t)

	tests := []struct {
		name      string
		platform  *platform
		want      Status
		wantCalls []call
		wantErr   string
	}{
		{
			name: "returns the reported data points",
			platform: &platform{handler: jsonHandler(`{"success":true,"result":[
				{"code":"va_temperature","value":235},
				{"code":"va_humidity","value":48},
				{"code":"battery_state","value":"high"}
			]}`)},
			want: Status{
				{Code: "va_temperature", Value: json.RawMessage("235")},
				{Code: "va_humidity", Value: json.RawMessage("48")},
				{Code: "battery_state", Value: json.RawMessage(`"high"`)},
			},
			wantCalls: []call{{method: http.MethodGet, path: "/v1.0/devices/dev1/status"}},
		},
		{
			name:     "an empty result is not an error",
			platform: &platform{handler: jsonHandler(`{"success":true,"result":[]}`)},
			want:     Status{},
		},
		{
			name:     "a failure names the device",
			platform: &platform{handler: jsonHandler(`{"success":false,"code":1000,"msg":"device not exist"}`)},
			wantErr:  "get status of device dev1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, p := newPlatform(t, tt.platform)

			got, err := c.DeviceStatus(t.Context(), "dev1")
			if !checkErr(t, err, tt.wantErr) {
				return
			}
			assertStatus(t, got, tt.want)
			if tt.wantCalls != nil {
				assertCalls(t, p.requests(false), tt.wantCalls)
			}
		})
	}
}

func TestClientSpecification(t *testing.T) {
	pinClock(t)

	tests := []struct {
		name      string
		platform  *platform
		want      Specification
		wantCalls []call
		wantErr   string
	}{
		{
			name: "returns the readable and writable data points",
			platform: &platform{handler: jsonHandler(`{"success":true,"result":{
				"category":"wsdcg",
				"functions":[],
				"status":[{"code":"va_temperature","type":"Integer","values":"{\"unit\":\"℃\",\"min\":-200,\"max\":600,\"scale\":1,\"step\":1}"}]
			}}`)},
			want: Specification{
				Category:  "wsdcg",
				Functions: []Definition{},
				Status: []Definition{{
					Code: "va_temperature", Type: "Integer",
					Values: `{"unit":"℃","min":-200,"max":600,"scale":1,"step":1}`,
				}},
			},
			wantCalls: []call{{method: http.MethodGet, path: "/v1.0/devices/dev1/specifications"}},
		},
		{
			name:     "a failure names the device",
			platform: &platform{handler: jsonHandler(`{"success":false,"code":1000,"msg":"device not exist"}`)},
			wantErr:  "get specification of device dev1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, p := newPlatform(t, tt.platform)

			got, err := c.Specification(t.Context(), "dev1")
			if !checkErr(t, err, tt.wantErr) {
				return
			}
			if got.Category != tt.want.Category {
				t.Errorf("category = %q, want %q", got.Category, tt.want.Category)
			}
			if len(got.Status) != len(tt.want.Status) {
				t.Fatalf("got %d status definitions, want %d", len(got.Status), len(tt.want.Status))
			}
			for i := range tt.want.Status {
				if got.Status[i] != tt.want.Status[i] {
					t.Errorf("status[%d] = %+v, want %+v", i, got.Status[i], tt.want.Status[i])
				}
			}
			assertCalls(t, p.requests(false), tt.wantCalls)
		})
	}
}

func TestClientSendCommands(t *testing.T) {
	pinClock(t)

	tests := []struct {
		name      string
		commands  []Command
		platform  *platform
		wantCalls []call
		wantErr   string
	}{
		{
			name:     "wraps the commands in the expected envelope",
			commands: []Command{{Code: "switch_1", Value: true}},
			platform: &platform{handler: jsonHandler(`{"success":true,"result":true}`)},
			wantCalls: []call{{
				method: http.MethodPost,
				path:   "/v1.0/devices/dev1/commands",
				body:   `{"commands":[{"code":"switch_1","value":true}]}`,
			}},
		},
		{
			name:     "sends several commands at once",
			commands: []Command{{Code: "switch_1", Value: true}, {Code: "countdown_1", Value: 300}},
			platform: &platform{handler: jsonHandler(`{"success":true,"result":true}`)},
			wantCalls: []call{{
				method: http.MethodPost,
				path:   "/v1.0/devices/dev1/commands",
				body:   `{"commands":[{"code":"switch_1","value":true},{"code":"countdown_1","value":300}]}`,
			}},
		},
		{
			name:     "no commands is refused before any request",
			commands: nil,
			wantErr:  "at least one command is required",
		},
		{
			name:     "a rejected command names the device",
			commands: []Command{{Code: "switch_1", Value: true}},
			platform: &platform{handler: jsonHandler(`{"success":false,"code":2008,"msg":"command invalid"}`)},
			wantErr:  "send commands to device dev1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, p := newPlatform(t, tt.platform)

			err := c.SendCommands(t.Context(), "dev1", tt.commands...)
			if !checkErr(t, err, tt.wantErr) {
				return
			}
			assertCalls(t, p.requests(false), tt.wantCalls)
		})
	}
}

func TestStatusGet(t *testing.T) {
	status := Status{
		{Code: "va_temperature", Value: json.RawMessage("235")},
		{Code: "va_humidity", Value: json.RawMessage("48")},
	}

	tests := []struct {
		name  string
		code  string
		want  string
		found bool
	}{
		{name: "finds the first data point", code: "va_temperature", want: "235", found: true},
		{name: "finds a later data point", code: "va_humidity", want: "48", found: true},
		{name: "reports a missing code", code: "battery", found: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dp, ok := status.Get(tt.code)
			if ok != tt.found {
				t.Fatalf("found = %v, want %v", ok, tt.found)
			}
			if ok && string(dp.Value) != tt.want {
				t.Errorf("value = %s, want %s", dp.Value, tt.want)
			}
		})
	}
}

func TestDataPointInt(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int64
		wantErr string
	}{
		{name: "positive", value: "235", want: 235},
		{name: "negative", value: "-40", want: -40},
		{name: "zero", value: "0", want: 0},
		{name: "a string is not an integer", value: `"235"`, wantErr: "not an integer"},
		{name: "a fraction is not an integer", value: "23.5", wantErr: "not an integer"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dp := DataPoint{Code: "va_temperature", Value: json.RawMessage(tt.value)}
			got, err := dp.Int()
			if !checkErr(t, err, tt.wantErr) {
				return
			}
			if got != tt.want {
				t.Errorf("Int() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDataPointFloat(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    float64
		wantErr string
	}{
		{name: "fraction", value: "23.5", want: 23.5},
		{name: "integer", value: "235", want: 235},
		{name: "a string is not a number", value: `"23.5"`, wantErr: "not a number"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dp := DataPoint{Code: "va_temperature", Value: json.RawMessage(tt.value)}
			got, err := dp.Float()
			if !checkErr(t, err, tt.wantErr) {
				return
			}
			if got != tt.want {
				t.Errorf("Float() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDataPointBool(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    bool
		wantErr string
	}{
		{name: "true", value: "true", want: true},
		{name: "false", value: "false", want: false},
		{name: "a number is not a boolean", value: "1", wantErr: "not a boolean"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dp := DataPoint{Code: "switch_1", Value: json.RawMessage(tt.value)}
			got, err := dp.Bool()
			if !checkErr(t, err, tt.wantErr) {
				return
			}
			if got != tt.want {
				t.Errorf("Bool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDataPointText(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr string
	}{
		{name: "string", value: `"high"`, want: "high"},
		{name: "empty string", value: `""`, want: ""},
		{name: "a number is not a string", value: "3", wantErr: "not a string"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dp := DataPoint{Code: "battery_state", Value: json.RawMessage(tt.value)}
			got, err := dp.Text()
			if !checkErr(t, err, tt.wantErr) {
				return
			}
			if got != tt.want {
				t.Errorf("Text() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSpecificationGet(t *testing.T) {
	spec := Specification{
		Status:    []Definition{{Code: "va_temperature", Type: "Integer"}},
		Functions: []Definition{{Code: "switch_1", Type: "Boolean"}},
	}

	tests := []struct {
		name  string
		code  string
		want  string
		found bool
	}{
		{name: "finds a readable data point", code: "va_temperature", want: "Integer", found: true},
		{name: "finds a writable data point", code: "switch_1", want: "Boolean", found: true},
		{name: "reports a missing code", code: "nope", found: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def, ok := spec.Get(tt.code)
			if ok != tt.found {
				t.Fatalf("found = %v, want %v", ok, tt.found)
			}
			if ok && def.Type != tt.want {
				t.Errorf("type = %q, want %q", def.Type, tt.want)
			}
		})
	}
}

func TestDefinitionRange(t *testing.T) {
	tests := []struct {
		name    string
		values  string
		want    Range
		wantErr string
	}{
		{
			name:   "temperature range",
			values: `{"unit":"℃","min":-200,"max":600,"scale":1,"step":1}`,
			want:   Range{Unit: "℃", Min: -200, Max: 600, Scale: 1, Step: 1},
		},
		{
			name:   "unscaled percentage",
			values: `{"unit":"%","min":0,"max":100,"scale":0,"step":1}`,
			want:   Range{Unit: "%", Min: 0, Max: 100, Scale: 0, Step: 1},
		},
		{
			name:    "an enum definition is not a range",
			values:  `{"range":["high","low"]}`,
			wantErr: "",
			want:    Range{},
		},
		{
			name:    "malformed values are reported with the code",
			values:  `not json`,
			wantErr: "parse values",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := Definition{Code: "va_temperature", Values: tt.values}
			got, err := def.Range()
			if !checkErr(t, err, tt.wantErr) {
				return
			}
			if got != tt.want {
				t.Errorf("Range() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRangeScaled(t *testing.T) {
	tests := []struct {
		name  string
		scale int
		raw   int64
		want  float64
	}{
		{name: "scale 1 turns tenths into units", scale: 1, raw: 235, want: 23.5},
		{name: "scale 0 leaves the value alone", scale: 0, raw: 48, want: 48},
		{name: "scale 2 turns hundredths into units", scale: 2, raw: 1234, want: 12.34},
		{name: "negative readings keep their sign", scale: 1, raw: -155, want: -15.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Range{Scale: tt.scale}).Scaled(tt.raw); got != tt.want {
				t.Errorf("Scaled(%d) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

// assertDevices compares the fields a case fills in, ignoring the rest.
func assertDevices(t *testing.T, got, want []Device) {
	t.Helper()
	if want == nil {
		return
	}
	if len(got) != len(want) {
		t.Fatalf("got %d devices, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Name != want[i].Name ||
			got[i].Category != want[i].Category || got[i].Online != want[i].Online ||
			got[i].Sub != want[i].Sub {
			t.Errorf("device %d = %+v, want %+v", i, got[i], want[i])
		}
		assertStatus(t, got[i].Status, want[i].Status)
	}
}

// assertStatus compares data points by code and raw value.
func assertStatus(t *testing.T, got, want Status) {
	t.Helper()
	if want == nil {
		return
	}
	if len(got) != len(want) {
		t.Fatalf("got %d data points, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Code != want[i].Code || string(got[i].Value) != string(want[i].Value) {
			t.Errorf("data point %d = %s=%s, want %s=%s",
				i, got[i].Code, got[i].Value, want[i].Code, want[i].Value)
		}
	}
}
