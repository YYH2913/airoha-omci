// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xg2010g/airoha-omci/internal/engine"
	"github.com/xg2010g/airoha-omci/internal/mib"
	"github.com/xg2010g/airoha-omci/internal/model"
	"github.com/xg2010g/airoha-omci/internal/platform"
	"github.com/xg2010g/airoha-omci/internal/status"
	"github.com/xg2010g/airoha-omci/internal/transport"
)

var version = "devel"

type options struct {
	interfaceName string
	serialNumber  string
	equipmentID   string
	statusPath    string
	applyHelper   string
	controlHelper string
}

func main() {
	var opts options
	flag.StringVar(&opts.interfaceName, "interface", "omci", "OMCI netdev")
	flag.StringVar(&opts.serialNumber, "serial", "", "ONU serial: four vendor characters and eight hex digits")
	flag.StringVar(&opts.equipmentID, "equipment-id", "XG2010G", "ONU equipment identifier")
	flag.StringVar(&opts.statusPath, "status", "/var/run/airoha-omcid/status.json", "atomic JSON status path")
	flag.StringVar(&opts.applyHelper, "apply-helper", "", "fixed executable receiving candidate MIB snapshots as JSON")
	flag.StringVar(&opts.controlHelper, "control-helper", "", "fixed executable handling time sync and scheduled reboot")
	flag.Parse()

	if err := run(opts); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}

func run(opts options) error {
	factory, err := model.XG2010G(model.Identity{
		SerialNumber: opts.serialNumber,
		Version:      version,
		EquipmentID:  opts.equipmentID,
	})
	if err != nil {
		return err
	}
	var applier mib.Applier
	platformBackend := "memory-only"
	if opts.applyHelper != "" {
		applier = platform.ExecApplier{Path: opts.applyHelper}
		platformBackend = opts.applyHelper
	}
	store, err := mib.NewWithApplier(factory, applier)
	if err != nil {
		return fmt.Errorf("initialize ONU MIB: %w", err)
	}
	var controller engine.Controller
	if opts.controlHelper != "" {
		controller = platform.ExecController{Path: opts.controlHelper}
	}
	protocol := engine.NewWithController(store, controller)

	conn, err := transport.OpenPacket(opts.interfaceName)
	if err != nil {
		return err
	}
	defer conn.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	started := time.Now().UTC()
	state := status.Snapshot{
		State:           "online",
		Interface:       opts.interfaceName,
		StartedAt:       started,
		MIBDataSync:     store.DataSync(),
		MIBEntries:      len(store.Snapshot()),
		PlatformBackend: platformBackend,
	}
	statusWriter := status.NewWriter(opts.statusPath)
	if err := statusWriter.Write(state); err != nil {
		return err
	}

	for {
		frame, err := conn.ReadFrame(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				state.State = "stopped"
				_ = statusWriter.Write(state)
				return nil
			}
			state.TransportErrors++
			state.LastError = err.Error()
			_ = statusWriter.Write(state)
			return err
		}

		state.RXFrames++
		if len(frame) >= 4 {
			state.LastTransactionID = uint16(frame[0])<<8 | uint16(frame[1])
			state.LastMessageType = frame[2]
		}
		response, err := protocol.Handle(frame)
		if err != nil {
			state.DecodeErrors++
			state.LastError = err.Error()
			state.MIBDataSync = store.DataSync()
			state.MIBEntries = len(store.Snapshot())
			_ = statusWriter.Write(state)
			log.Printf("OMCI request rejected: %v", err)
			continue
		}
		if err := conn.WriteFrame(ctx, response); err != nil {
			state.TransportErrors++
			state.LastError = err.Error()
			_ = statusWriter.Write(state)
			return err
		}
		state.TXFrames++
		state.MIBDataSync = store.DataSync()
		state.MIBEntries = len(store.Snapshot())
		state.LastError = ""
		if err := statusWriter.Write(state); err != nil {
			log.Printf("publish status: %v", err)
		}
	}
}
