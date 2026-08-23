// Package tuyacloud is a client for the Tuya Cloud (IoT Core) OpenAPI.
//
// It covers what a smart-home integration actually needs — authentication,
// device discovery, reading device status and sending commands — on the Go
// standard library alone, with no third-party dependencies.
//
// # Credentials
//
// The access ID and access key come from a cloud project on the Tuya IoT
// platform (https://iot.tuya.com), with the Smart Life or Tuya Smart app
// account linked to it. Pick the [Region] of the data centre serving that
// account; a mismatch fails authentication rather than falling back.
//
// # Tokens
//
// Access tokens are fetched on demand and cached until shortly before they
// expire, so callers never manage them. A [Client] is safe for concurrent use.
//
// # Example
//
//	client, err := tuyacloud.New(accessID, accessKey, tuyacloud.WithRegion(tuyacloud.RegionEurope))
//	if err != nil {
//		return err
//	}
//	status, err := client.DeviceStatus(ctx, deviceID)
//	if err != nil {
//		return err
//	}
//	for _, dp := range status {
//		log.Printf("%s = %s", dp.Code, dp.Value)
//	}
package tuyacloud
