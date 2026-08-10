// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"fmt"
	"sort"
	"time"

	omci "github.com/opencord/omci-lib-go/v2"
	me "github.com/opencord/omci-lib-go/v2/generated"
	"github.com/xg2010g/airoha-omci/internal/mib"
)

const (
	arcAttribute         = "Arc"
	arcIntervalAttribute = "ArcInterval"
)

// PollARC expires all problem-free NALM-QI timers and returns the mandatory
// ARC cancellation AVCs. It is intended to be called by the daemon ticker.
func (e *Engine) PollARC(device omci.DeviceIdent) ([][]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := validateDeviceIdentifier(device); err != nil {
		return nil, err
	}
	keys := make([]mib.Key, 0, len(e.arcFreeSince))
	for key := range e.arcFreeSince {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ClassID == keys[j].ClassID {
			return keys[i].EntityID < keys[j].EntityID
		}
		return keys[i].ClassID < keys[j].ClassID
	})

	var frames [][]byte
	for _, key := range keys {
		generated, err := e.pollARCKeyLocked(key, device)
		if err != nil {
			return nil, err
		}
		frames = append(frames, generated...)
	}
	return frames, nil
}

func (e *Engine) pollARCKeyLocked(key mib.Key, device omci.DeviceIdent) ([][]byte, error) {
	enabled, interval, supported, err := e.arcConfigurationLocked(key)
	if err != nil {
		return nil, err
	}
	if !supported || !enabled {
		delete(e.arcFreeSince, key)
		return nil, nil
	}
	if e.alarms[key] != ([28]byte{}) {
		delete(e.arcFreeSince, key)
		return nil, nil
	}

	now := e.now()
	since, started := e.arcFreeSince[key]
	if !started {
		e.arcFreeSince[key] = now
		since = now
	}
	if interval == 255 || now.Before(since.Add(time.Duration(interval)*time.Minute)) {
		return nil, nil
	}

	delete(e.arcFreeSince, key)
	return e.notifyAttributeChangeLocked(key, me.AttributeValueMap{
		arcAttribute: uint8(0),
	}, device)
}

func (e *Engine) arcEnabledLocked(key mib.Key) bool {
	enabled, _, supported, err := e.arcConfigurationLocked(key)
	return err == nil && supported && enabled
}

func (e *Engine) arcConfigurationLocked(key mib.Key) (bool, uint8, bool, error) {
	entity, omciErr := me.LoadManagedEntityDefinition(key.ClassID, me.ParamData{EntityID: key.EntityID})
	if omciErr.StatusCode() != me.Success {
		return false, 0, false, omciErr.GetError()
	}
	definitions := entity.GetAttributeDefinitions()
	arcDefinition, arcErr := me.GetAttributeDefinitionByName(definitions, arcAttribute)
	intervalDefinition, intervalErr := me.GetAttributeDefinitionByName(definitions, arcIntervalAttribute)
	if arcErr != nil || intervalErr != nil {
		return false, 0, false, nil
	}
	instance, err := e.mib.Get(key, arcDefinition.Mask|intervalDefinition.Mask)
	if err != nil {
		return false, 0, true, err
	}
	arc, arcPresent := instance.Attributes[arcAttribute].(uint8)
	interval, intervalPresent := instance.Attributes[arcIntervalAttribute].(uint8)
	if !arcPresent || !intervalPresent {
		return false, 0, true, fmt.Errorf("ARC attributes are missing from %v/%#x", key.ClassID, key.EntityID)
	}
	return arc == 1, interval, true, nil
}

func (e *Engine) afterSetLocked(key mib.Key, attributes me.AttributeValueMap,
	device omci.DeviceIdent) ([][]byte, error) {
	_, arcChanged := attributes[arcAttribute]
	_, intervalChanged := attributes[arcIntervalAttribute]
	if arcChanged || intervalChanged {
		enabled, _, supported, err := e.arcConfigurationLocked(key)
		if err != nil {
			return nil, err
		}
		if supported && enabled {
			if e.alarms[key] == ([28]byte{}) {
				if _, started := e.arcFreeSince[key]; !started {
					e.arcFreeSince[key] = e.now()
				}
			} else {
				delete(e.arcFreeSince, key)
			}
		} else {
			delete(e.arcFreeSince, key)
		}
	}

	var frames [][]byte
	if sample, present := e.opticalSample[key]; present && opticalConfigurationChanged(attributes) {
		generated, err := e.evaluateOpticalSampleLocked(key, sample, device)
		if err != nil {
			return nil, err
		}
		frames = append(frames, generated...)
	}
	if arcChanged || intervalChanged {
		generated, err := e.pollARCKeyLocked(key, device)
		if err != nil {
			return nil, err
		}
		frames = append(frames, generated...)
	}
	return frames, nil
}

func opticalConfigurationChanged(attributes me.AttributeValueMap) bool {
	for _, name := range []string{
		me.AniG_LowerOpticalThreshold,
		me.AniG_UpperOpticalThreshold,
		me.AniG_LowerTransmitPowerThreshold,
		me.AniG_UpperTransmitPowerThreshold,
		arcAttribute,
		arcIntervalAttribute,
	} {
		if _, present := attributes[name]; present {
			return true
		}
	}
	return false
}
