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

	omci "github.com/opencord/omci-lib-go/v2"
	"github.com/xg2010g/airoha-omci/internal/engine"
	platformevent "github.com/xg2010g/airoha-omci/internal/event"
	"github.com/xg2010g/airoha-omci/internal/mib"
	"github.com/xg2010g/airoha-omci/internal/model"
	"github.com/xg2010g/airoha-omci/internal/platform"
	"github.com/xg2010g/airoha-omci/internal/software"
	"github.com/xg2010g/airoha-omci/internal/status"
	"github.com/xg2010g/airoha-omci/internal/transport"
)

var version = "devel"

type options struct {
	interfaceName  string
	serialNumber   string
	equipmentID    string
	statusPath     string
	applyHelper    string
	controlHelper  string
	eventHelper    string
	softwareHelper string
}

func main() {
	var opts options
	flag.StringVar(&opts.interfaceName, "interface", "omci", "OMCI netdev")
	flag.StringVar(&opts.serialNumber, "serial", "", "ONU serial: four vendor characters and eight hex digits")
	flag.StringVar(&opts.equipmentID, "equipment-id", "XG2010G", "ONU equipment identifier")
	flag.StringVar(&opts.statusPath, "status", "/var/run/airoha-omcid/status.json", "atomic JSON status path")
	flag.StringVar(&opts.applyHelper, "apply-helper", "", "fixed executable receiving candidate service graphs as JSON")
	flag.StringVar(&opts.controlHelper, "control-helper", "", "fixed executable handling time sync and scheduled reboot")
	flag.StringVar(&opts.eventHelper, "event-helper", "", "fixed executable streaming platform events as JSON lines")
	flag.StringVar(&opts.softwareHelper, "software-helper", "", "fixed executable handling software image lifecycle")
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
	var softwareController software.Controller
	softwareBackend := "disabled"
	if opts.softwareHelper != "" {
		softwareController = platform.ExecSoftwareController{Path: opts.softwareHelper}
		softwareBackend = opts.softwareHelper
	}
	protocol := engine.NewWithControllers(store, controller, softwareController)
	if err := protocol.RefreshSoftwareImages(); err != nil {
		return fmt.Errorf("load software image state: %w", err)
	}

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
		SoftwareBackend: softwareBackend,
	}
	updateSoftwareStatus := func() {
		softwareState := protocol.SoftwareStatus()
		state.SoftwarePhase = softwareState.Phase
		state.SoftwareImageID = softwareState.ImageID
		state.SoftwareBytes = softwareState.Bytes
		state.SoftwareImageSize = softwareState.ImageSize
		state.SoftwareImageHash = softwareState.ImageHash
	}
	updateSoftwareStatus()
	statusWriter := status.NewWriter(opts.statusPath)
	if err := statusWriter.Write(state); err != nil {
		return err
	}
	sendNotifications := func(frames [][]byte) error {
		for _, frame := range frames {
			if err := conn.WriteFrame(ctx, frame); err != nil {
				return err
			}
			state.TXFrames++
			state.NotificationFrames++
			if len(frame) >= 3 {
				state.LastNotificationType = frame[2]
			}
		}
		return nil
	}

	type receiveResult struct {
		frame []byte
		err   error
	}
	received := make(chan receiveResult, 1)
	go func() {
		for {
			frame, err := conn.ReadFrame(ctx)
			select {
			case received <- receiveResult{frame: frame, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	var platformEvents chan platformevent.Event
	var eventSourceErrors chan error
	if opts.eventHelper != "" {
		platformEvents = make(chan platformevent.Event)
		eventSourceErrors = make(chan error, 1)
		go func() {
			err := (platformevent.ExecSource{Path: opts.eventHelper}).Run(ctx,
				func(value platformevent.Event) error {
					select {
					case platformEvents <- value:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
			eventSourceErrors <- err
		}()
	}
	arcTicker := time.NewTicker(time.Second)
	defer arcTicker.Stop()
	performanceTicker := time.NewTicker(time.Second)
	defer performanceTicker.Stop()
	multicastTicker := time.NewTicker(time.Second)
	defer multicastTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			state.State = "stopped"
			_ = statusWriter.Write(state)
			return nil

		case sourceErr := <-eventSourceErrors:
			if ctx.Err() != nil {
				state.State = "stopped"
				_ = statusWriter.Write(state)
				return nil
			}
			state.TransportErrors++
			state.LastError = sourceErr.Error()
			_ = statusWriter.Write(state)
			return sourceErr

		case value := <-platformEvents:
			frames, err := value.Dispatch(protocol)
			if err != nil {
				state.EventErrors++
				state.LastError = err.Error()
				_ = statusWriter.Write(state)
				log.Printf("OMCI platform event rejected: %v", err)
				continue
			}
			if err := sendNotifications(frames); err != nil {
				state.TransportErrors++
				state.LastError = err.Error()
				_ = statusWriter.Write(state)
				return err
			}
			state.MIBDataSync = store.DataSync()
			state.MIBEntries = len(store.Snapshot())
			updateSoftwareStatus()
			state.LastError = ""
			if err := statusWriter.Write(state); err != nil {
				log.Printf("publish status: %v", err)
			}

		case <-arcTicker.C:
			frames, err := protocol.PollARC(omci.BaselineIdent)
			if err != nil {
				state.EventErrors++
				state.LastError = err.Error()
				_ = statusWriter.Write(state)
				log.Printf("OMCI ARC timer rejected: %v", err)
				continue
			}
			if err := sendNotifications(frames); err != nil {
				state.TransportErrors++
				state.LastError = err.Error()
				_ = statusWriter.Write(state)
				return err
			}
			if len(frames) != 0 {
				state.MIBDataSync = store.DataSync()
				state.LastError = ""
				if err := statusWriter.Write(state); err != nil {
					log.Printf("publish status: %v", err)
				}
			}

		case <-performanceTicker.C:
			frames, err := protocol.PollPerformance(omci.BaselineIdent)
			if err != nil {
				state.PerformanceErrors++
				state.LastError = err.Error()
				_ = statusWriter.Write(state)
				log.Printf("OMCI performance collection failed: %v", err)
				continue
			}
			if err := sendNotifications(frames); err != nil {
				state.TransportErrors++
				state.LastError = err.Error()
				_ = statusWriter.Write(state)
				return err
			}
			if len(frames) != 0 {
				state.LastError = ""
				if err := statusWriter.Write(state); err != nil {
					log.Printf("publish status: %v", err)
				}
			}

		case <-multicastTicker.C:
			if err := protocol.PollMulticast(); err != nil {
				state.EventErrors++
				state.LastError = err.Error()
				_ = statusWriter.Write(state)
				log.Printf("OMCI multicast timer synchronization failed: %v", err)
				continue
			}

		case result := <-received:
			if result.err != nil {
				if errors.Is(result.err, context.Canceled) {
					state.State = "stopped"
					_ = statusWriter.Write(state)
					return nil
				}
				state.TransportErrors++
				state.LastError = result.err.Error()
				_ = statusWriter.Write(state)
				return result.err
			}

			frame := result.frame
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
				updateSoftwareStatus()
				_ = statusWriter.Write(state)
				log.Printf("OMCI request rejected: %v", err)
				continue
			}
			if len(response) != 0 {
				if err := conn.WriteFrame(ctx, response); err != nil {
					state.TransportErrors++
					state.LastError = err.Error()
					_ = statusWriter.Write(state)
					return err
				}
				state.TXFrames++
			}
			if err := sendNotifications(protocol.DrainNotifications()); err != nil {
				state.TransportErrors++
				state.LastError = err.Error()
				_ = statusWriter.Write(state)
				return err
			}
			notificationErr := protocol.DrainNotificationError()
			if notificationErr != nil {
				state.EventErrors++
				state.LastError = notificationErr.Error()
				log.Printf("OMCI derived notification failed: %v", notificationErr)
			}
			state.MIBDataSync = store.DataSync()
			state.MIBEntries = len(store.Snapshot())
			updateSoftwareStatus()
			if notificationErr == nil {
				state.LastError = ""
			}
			if err := statusWriter.Write(state); err != nil {
				log.Printf("publish status: %v", err)
			}
		}
	}
}
