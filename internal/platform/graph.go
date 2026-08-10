// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"fmt"

	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/mib"
)

// ValidateServiceGraph checks the references needed to program the Airoha
// T-CONT/GEM data path. It intentionally accepts incomplete higher-level
// bridge and VLAN graphs while the OLT is still constructing them.
func ValidateServiceGraph(snapshot []mib.Instance) error {
	instances := make(map[mib.Key]mib.Instance, len(snapshot))
	for _, instance := range snapshot {
		if _, exists := instances[instance.Key]; exists {
			return fmt.Errorf("duplicate managed entity %d/%#x", instance.ClassID, instance.EntityID)
		}
		instances[instance.Key] = instance
	}

	gemPorts := make(map[uint16]mib.Key)
	for _, instance := range snapshot {
		switch instance.ClassID {
		case me.GemPortNetworkCtpClassID:
			portID, err := uint16Attribute(instance, me.GemPortNetworkCtp_PortId)
			if err != nil {
				return err
			}
			if portID > 0x0fff {
				return fmt.Errorf("GEM CTP %#x has out-of-range port ID %d", instance.EntityID, portID)
			}
			if previous, duplicate := gemPorts[portID]; duplicate {
				return fmt.Errorf("GEM port ID %d is shared by %#x and %#x", portID, previous.EntityID, instance.EntityID)
			}
			gemPorts[portID] = instance.Key

			tcont, err := uint16Attribute(instance, me.GemPortNetworkCtp_TContPointer)
			if err != nil {
				return err
			}
			if _, exists := instances[mib.Key{ClassID: me.TContClassID, EntityID: tcont}]; !exists {
				return fmt.Errorf("GEM CTP %#x references missing T-CONT %#x", instance.EntityID, tcont)
			}
			direction, err := uint8Attribute(instance, me.GemPortNetworkCtp_Direction)
			if err != nil {
				return err
			}
			if direction < 1 || direction > 3 {
				return fmt.Errorf("GEM CTP %#x has invalid direction %d", instance.EntityID, direction)
			}

		case me.GemInterworkingTerminationPointClassID:
			pointer, err := uint16Attribute(instance,
				me.GemInterworkingTerminationPoint_GemPortNetworkCtpConnectivityPointer)
			if err != nil {
				return err
			}
			if _, exists := instances[mib.Key{ClassID: me.GemPortNetworkCtpClassID, EntityID: pointer}]; !exists {
				return fmt.Errorf("GEM IW TP %#x references missing GEM CTP %#x", instance.EntityID, pointer)
			}
		}
	}
	return nil
}

func uint16Attribute(instance mib.Instance, name string) (uint16, error) {
	value, present := instance.Attributes[name]
	if !present {
		return 0, fmt.Errorf("managed entity %d/%#x is missing %s", instance.ClassID, instance.EntityID, name)
	}
	typed, ok := value.(uint16)
	if !ok {
		return 0, fmt.Errorf("managed entity %d/%#x attribute %s has type %T, want uint16",
			instance.ClassID, instance.EntityID, name, value)
	}
	return typed, nil
}

func uint8Attribute(instance mib.Instance, name string) (uint8, error) {
	value, present := instance.Attributes[name]
	if !present {
		return 0, fmt.Errorf("managed entity %d/%#x is missing %s", instance.ClassID, instance.EntityID, name)
	}
	typed, ok := value.(uint8)
	if !ok {
		return 0, fmt.Errorf("managed entity %d/%#x attribute %s has type %T, want uint8",
			instance.ClassID, instance.EntityID, name, value)
	}
	return typed, nil
}
