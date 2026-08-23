# tuyacloud

[![Go Reference](https://pkg.go.dev/badge/github.com/mshaulsky/tuyacloud.svg)](https://pkg.go.dev/github.com/mshaulsky/tuyacloud)
[![CI](https://github.com/mshaulsky/tuyacloud/actions/workflows/ci.yml/badge.svg)](https://github.com/mshaulsky/tuyacloud/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/mshaulsky/tuyacloud)](https://goreportcard.com/report/github.com/mshaulsky/tuyacloud)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A small, dependency-free Go client for the **Tuya Cloud (IoT Core) OpenAPI** —
the API behind the Smart Life and Tuya Smart apps.

It does the parts that are easy to get wrong (request signing, token lifecycle)
and stays out of the way for the rest.

```sh
go get github.com/mshaulsky/tuyacloud
```

## Why another client

Tuya publishes two Go SDKs, and both are hard to live with in a small service:
they keep credentials and tokens in package-level globals, take no
`context.Context`, and pull in a logging framework with log rotation — one of
them pulls in a whole web framework. Neither has been updated since early 2024.

This package is the opposite trade-off:

* **No third-party dependencies.** Standard library only.
* **`context.Context` on every call**, so timeouts and cancellation work.
* **No global state.** Credentials live on the client; several can coexist.
* **Errors carry context** and are matchable with `errors.As`.
* Small enough to read in one sitting (~750 lines).

## Quick start

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/mshaulsky/tuyacloud"
)

func main() {
	client, err := tuyacloud.New(accessID, accessKey,
		tuyacloud.WithRegion(tuyacloud.RegionEurope))
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	status, err := client.DeviceStatus(ctx, deviceID)
	if err != nil {
		log.Fatal(err)
	}

	if dp, ok := status.Get("va_temperature"); ok {
		raw, _ := dp.Int()
		log.Printf("temperature: %.1f °C", float64(raw)/10) // see Scales below
	}
}
```

Access tokens are fetched on first use, cached until shortly before they
expire, and replaced automatically if the platform rejects one mid-flight. A
`Client` is safe for concurrent use.

## Getting credentials

The API is not open to app accounts directly — you need a cloud project:

1. Register at [platform.tuya.com](https://platform.tuya.com) (the former
   iot.tuya.com) and create a **Cloud
   project** (development method: *Smart Home*).
2. In the project, open **Devices → Link Tuya App Account** and link the
   account your devices are already in, by scanning the QR code from the Smart
   Life or Tuya Smart app.
3. Copy the **Access ID** and **Access Key** from the project overview.
4. Under **Service API**, make sure *IoT Core* is subscribed and authorised.

Then find your device IDs with the bundled tool:

```sh
export TUYA_ACCESS_ID=...
export TUYA_ACCESS_KEY=...

go run ./examples/poll -list
go run ./examples/poll -device bfa1b2c3d4e5f6
go run ./examples/poll -device bfa1b2c3d4e5f6 -watch 30s
```

## Data centres

An app account lives in exactly **one** data centre, decided by the phone
country code it registered with. Credentials are not valid across data centres:
the wrong one fails authentication rather than redirecting.

| Region | Endpoint | Serves |
|---|---|---|
| `RegionEurope` | `openapi.tuyaeu.com` | Europe, Africa, Middle East, Central Asia |
| `RegionAmerica` | `openapi.tuyaus.com` | Americas, Asia-Pacific, anything unlisted |
| `RegionChina` | `openapi.tuyacn.com` | China |
| `RegionIndia` | `openapi.tuyain.com` | India |

`RegionEurope` is the default. If authentication fails with code `1001` on
credentials you are sure about, the region is the first thing to check.

## Scales: why a thermometer reports 235

Tuya reports numeric data points as **integers scaled by a power of ten**. A
temperature of 23.5 °C arrives as `235` with a scale of `1`. The scale is part
of the product specification, not the reading, so fetch it once and reuse it:

```go
spec, _ := client.Specification(ctx, deviceID)
def, _ := spec.Get("va_temperature")
r, _ := def.Range()          // {Unit:"℃", Min:-200, Max:600, Scale:1, Step:1}

dp, _ := status.Get("va_temperature")
raw, _ := dp.Int()
celsius := r.Scaled(raw)     // 235 -> 23.5
```

Data-point codes vary by product — `va_temperature` and `va_humidity` are
common for climate sensors, but check `-list` and `Specification` for yours.

## Errors

Failures come back in three flavours, all wrapped with the operation that
produced them:

```go
var apiErr *tuyacloud.APIError
if errors.As(err, &apiErr) && apiErr.Code == tuyacloud.CodeDataNotExist {
	// the device ID is unknown
}
```

* `*APIError` — the platform refused the call (`Code`, `Msg`). Named codes:
  `CodeDataNotExist` (1000), `CodeSecretInvalid` (1001), `CodeTokenExpired` (1010).
* `*HTTPError` — the response was not a Tuya envelope at all, which usually
  means a proxy, an outage or the wrong host.
* Everything else wraps the underlying transport or JSON error, so
  `context.DeadlineExceeded` and friends survive `errors.Is`.

## What is covered

| Call | Endpoint |
|---|---|
| `Devices` | `GET /v1.0/iot-01/associated-users/devices` (paginated) |
| `DevicesByUser` | `GET /v1.3/iot-03/devices?source_type=tuyaUser` (paginated) |
| `Device` | `GET /v1.0/devices/{id}` |
| `DeviceStatus` | `GET /v1.0/devices/{id}/status` |
| `DevicesStatus` | `GET /v1.0/iot-03/devices/status` (batched by 20) |
| `Specification` | `GET /v1.0/devices/{id}/specifications` |
| `SendCommands` | `POST /v1.0/devices/{id}/commands` |

The legacy device listing (`/v1.0/users/{uid}/devices`) is deliberately not
used: newly created projects get `1106 permission deny` from it no matter what
is authorised.

Anything else is one line away — `Get` and `Post` sign, send and decode an
arbitrary path, so an unwrapped endpoint needs no changes to this package:

```go
var out struct {
	Devices []map[string]any `json:"devices"`
}
err := client.Get(ctx, "/v2.0/cloud/thing/device", url.Values{"page_size": {"20"}}, &out)
```

Push messages (Tuya's Pulsar message service) are **not** covered; polling is.
For real-time events, Tuya's `tuya-pulsar-sdk-go` still works alongside this.

## Rate limits and polling

Tuya's free tier is generous for a home setup but not unlimited. Poll on the
order of **once a minute per device**, not once a second — battery-powered
sensors only report every few minutes anyway, so faster polling returns the
same values while burning quota.

## Development

```sh
go test ./...        # no credentials or network required
go generate ./...    # regenerate the mock and the wire models
golangci-lint run    # same configuration CI uses
```

The suite runs against an in-process fake of the platform (`httptest`) that
recomputes the signature of every request server-side and rejects mismatches,
so each case also proves the request on the wire was signed correctly. The signing test
checks the output against vectors produced by an independent transcription of
[Tuya's documented algorithm](https://developer.tuya.com/en/docs/iot/new-singnature?id=Kbw0q34cs2e5g),
so a match means this package agrees with the specification and not merely with
itself.

The wire models in `types_gen.go` are generated (via
[oapi-codegen](https://github.com/oapi-codegen/oapi-codegen)) from
[`openapi.yaml`](openapi.yaml) — a hand-maintained OpenAPI description of the
Tuya endpoints this package uses, kept because Tuya publishes no
machine-readable specification of its own. To model a new endpoint, describe it
in the spec and run `go generate`; the transport in `client.go` needs no
changes.

## License

MIT — see [LICENSE](LICENSE).
