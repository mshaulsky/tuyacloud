// Command poll is a small tool for exploring a Tuya cloud project from the
// terminal: it lists the devices linked to the account and reads their current
// values. Use it to find the device IDs an application should poll.
//
//	export TUYA_ACCESS_ID=...
//	export TUYA_ACCESS_KEY=...
//
//	go run ./examples/poll -list                  # discover devices
//	go run ./examples/poll -device bfa1b2c3...    # read one device
//	go run ./examples/poll -device bfa1b2c3... -watch 30s
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mshaulsky/tuyacloud"
)

func main() {
	log.SetFlags(log.Ltime)

	list := flag.Bool("list", false, "list the devices linked to the account")
	device := flag.String("device", "", "read the status of this device ID")
	region := flag.String("region", "eu", "data centre: eu, us, cn or in")
	watch := flag.Duration("watch", 0, "keep polling the device at this interval")
	flag.Parse()

	if err := run(*list, *device, *region, *watch); err != nil {
		log.Fatal(err)
	}
}

func run(list bool, device, region string, watch time.Duration) error {
	if !list && device == "" {
		return errors.New("nothing to do: pass -list or -device ID")
	}

	client, err := newClient(region)
	if err != nil {
		return err
	}

	// Ctrl-C cancels the in-flight request rather than killing the process
	// mid-poll, which is the pattern an application should follow too.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if list {
		return listDevices(ctx, client)
	}
	if watch > 0 {
		return watchDevice(ctx, client, device, watch)
	}
	return showDevice(ctx, client, device)
}

// newClient builds a client from the environment and the chosen data centre.
func newClient(region string) (*tuyacloud.Client, error) {
	accessID, accessKey := os.Getenv("TUYA_ACCESS_ID"), os.Getenv("TUYA_ACCESS_KEY")
	if accessID == "" || accessKey == "" {
		return nil, errors.New("set TUYA_ACCESS_ID and TUYA_ACCESS_KEY from your cloud project")
	}

	regions := map[string]tuyacloud.Region{
		"eu": tuyacloud.RegionEurope,
		"us": tuyacloud.RegionAmerica,
		"cn": tuyacloud.RegionChina,
		"in": tuyacloud.RegionIndia,
	}
	r, ok := regions[region]
	if !ok {
		return nil, fmt.Errorf("unknown region %q: use eu, us, cn or in", region)
	}
	return tuyacloud.New(accessID, accessKey, tuyacloud.WithRegion(r))
}

// listDevices prints every device with its ID, which is what an application
// needs in its configuration.
func listDevices(ctx context.Context, client *tuyacloud.Client) error {
	devices, err := client.Devices(ctx)
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		return errors.New("no devices found: is the app account linked to the cloud project?")
	}

	fmt.Printf("%-24s %-10s %-8s %s\n", "DEVICE ID", "CATEGORY", "ONLINE", "NAME")
	for i := range devices {
		d := &devices[i]
		fmt.Printf("%-24s %-10s %-8t %s\n", d.ID, d.Category, d.Online, d.Name)
	}
	return nil
}

// showDevice prints one device's data points, converting the numeric ones into
// real units using the product specification.
func showDevice(ctx context.Context, client *tuyacloud.Client, deviceID string) error {
	spec, err := client.Specification(ctx, deviceID)
	if err != nil {
		return err
	}
	status, err := client.DeviceStatus(ctx, deviceID)
	if err != nil {
		return err
	}
	for _, dp := range status {
		fmt.Printf("%-24s %s\n", dp.Code, format(spec, dp))
	}
	return nil
}

// watchDevice polls one device until the context is cancelled.
func watchDevice(ctx context.Context, client *tuyacloud.Client, deviceID string, every time.Duration) error {
	spec, err := client.Specification(ctx, deviceID)
	if err != nil {
		return err
	}

	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		status, err := client.DeviceStatus(ctx, deviceID)
		if err != nil {
			// A cancelled context means the user asked to stop, not a failure.
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("poll failed: %v", err)
		} else {
			parts := make([]string, 0, len(status))
			for _, dp := range status {
				parts = append(parts, dp.Code+"="+format(spec, dp))
			}
			log.Println(strings.Join(parts, "  "))
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// format renders a data point for a human, applying the scale from the product
// specification so 235 shows up as 23.5 °C rather than as a bare integer.
func format(spec tuyacloud.Specification, dp tuyacloud.DataPoint) string {
	def, ok := spec.Get(dp.Code)
	if !ok || def.Type != "Integer" {
		return string(dp.Value)
	}
	r, err := def.Range()
	if err != nil {
		return string(dp.Value)
	}
	raw, err := dp.Int()
	if err != nil {
		return string(dp.Value)
	}
	return fmt.Sprintf("%g%s", r.Scaled(raw), r.Unit)
}
