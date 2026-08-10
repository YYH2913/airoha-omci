// SPDX-License-Identifier: Apache-2.0

package mib

import (
	"fmt"
	"sort"
	"sync"

	me "github.com/opencord/omci-lib-go/v2/generated"
)

type Origin uint8

const (
	OriginONU Origin = iota
	OriginOLT
)

type Key struct {
	ClassID  me.ClassID
	EntityID uint16
}

type Instance struct {
	Key
	Attributes me.AttributeValueMap
	Origin     Origin
}

type ResultError struct {
	Result          me.Results
	FailedMask      uint16
	UnsupportedMask uint16
	Cause           error
}

func (e *ResultError) Error() string {
	if e.Cause == nil {
		return e.Result.String()
	}
	return fmt.Sprintf("%s: %v", e.Result, e.Cause)
}

func (e *ResultError) Unwrap() error {
	return e.Cause
}

type Store struct {
	mu       sync.RWMutex
	factory  map[Key]Instance
	current  map[Key]Instance
	dataSync uint8
}

func New(factory []Instance) (*Store, error) {
	s := &Store{
		factory: make(map[Key]Instance, len(factory)),
		current: make(map[Key]Instance, len(factory)),
	}

	for _, instance := range factory {
		instance.Origin = OriginONU
		normalized, err := normalize(instance)
		if err != nil {
			return nil, fmt.Errorf("invalid factory ME %v/%#x: %w", instance.ClassID, instance.EntityID, err)
		}
		if _, exists := s.factory[normalized.Key]; exists {
			return nil, fmt.Errorf("duplicate factory ME %v/%#x", instance.ClassID, instance.EntityID)
		}
		s.factory[normalized.Key] = normalized
		s.current[normalized.Key] = cloneInstance(normalized)
	}
	s.setDataSyncLocked(0)
	return s, nil
}

func (s *Store) DataSync() uint8 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dataSync
}

func (s *Store) Get(key Key, mask uint16) (Instance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instance, exists := s.current[key]
	if !exists {
		return Instance{}, unknownKeyError(key)
	}

	entity, result := loadDefinition(key.ClassID, key.EntityID, instance.Attributes)
	if result != nil {
		return Instance{}, result
	}
	allowed := entity.GetAllowedAttributeMask()
	unsupported := mask &^ allowed
	selectedMask := mask & allowed

	selected := make(me.AttributeValueMap)
	for index, definition := range entity.GetAttributeDefinitions() {
		if index == 0 || selectedMask&definition.Mask != 0 {
			if value, ok := instance.Attributes[definition.GetName()]; ok {
				selected[definition.GetName()] = cloneValue(value)
			}
		}
	}
	instance.Attributes = selected
	if unsupported != 0 {
		return instance, &ResultError{Result: me.AttributeFailure, UnsupportedMask: unsupported}
	}
	return instance, nil
}

func (s *Store) Create(classID me.ClassID, entityID uint16, attributes me.AttributeValueMap) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := Key{ClassID: classID, EntityID: entityID}
	if _, exists := s.current[key]; exists {
		return &ResultError{Result: me.InstanceExists}
	}

	entity, result := loadDefinition(classID, entityID, attributes)
	if result != nil {
		return result
	}
	definition := entity.GetManagedEntityDefinition()
	if !me.SupportsMsgType(entity, me.Create) ||
		(definition.Access != me.CreatedByOlt && definition.Access != me.CreatedByBoth) {
		return &ResultError{Result: me.NotSupported}
	}
	if err := validateAccess(entity, attributes, me.SetByCreate); err != nil {
		return err
	}

	instance, err := normalize(Instance{
		Key:        key,
		Attributes: attributes,
		Origin:     OriginOLT,
	})
	if err != nil {
		return err
	}
	s.current[key] = instance
	s.bumpDataSyncLocked()
	return nil
}

func (s *Store) Set(key Key, attributes me.AttributeValueMap) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, exists := s.current[key]
	if !exists {
		return unknownKeyError(key)
	}
	entity, result := loadDefinition(key.ClassID, key.EntityID, current.Attributes)
	if result != nil {
		return result
	}
	if !me.SupportsMsgType(entity, me.Set) {
		return &ResultError{Result: me.NotSupported}
	}
	if err := validateAccess(entity, attributes, me.Write); err != nil {
		return err
	}

	next := cloneInstance(current)
	for name, value := range attributes {
		if name == me.ManagedEntityID {
			continue
		}
		next.Attributes[name] = cloneValue(value)
	}
	normalized, err := normalize(next)
	if err != nil {
		return err
	}
	s.current[key] = normalized
	s.bumpDataSyncLocked()
	return nil
}

func (s *Store) Delete(key Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	instance, exists := s.current[key]
	if !exists {
		return unknownKeyError(key)
	}
	entity, result := loadDefinition(key.ClassID, key.EntityID, instance.Attributes)
	if result != nil {
		return result
	}
	definition := entity.GetManagedEntityDefinition()
	if instance.Origin == OriginONU || !me.SupportsMsgType(entity, me.Delete) ||
		(definition.Access != me.CreatedByOlt && definition.Access != me.CreatedByBoth) {
		return &ResultError{Result: me.NotSupported}
	}

	delete(s.current, key)
	s.bumpDataSyncLocked()
	return nil
}

func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.current = make(map[Key]Instance, len(s.factory))
	for key, instance := range s.factory {
		s.current[key] = cloneInstance(instance)
	}
	s.setDataSyncLocked(0)
}

func (s *Store) Snapshot() []Instance {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]Instance, 0, len(s.current))
	for _, instance := range s.current {
		items = append(items, cloneInstance(instance))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ClassID == items[j].ClassID {
			return items[i].EntityID < items[j].EntityID
		}
		return items[i].ClassID < items[j].ClassID
	})
	return items
}

func normalize(instance Instance) (Instance, error) {
	entity, result := loadDefinition(instance.ClassID, instance.EntityID, instance.Attributes)
	if result != nil {
		return Instance{}, result
	}
	instance.Attributes = cloneAttributes(entity.GetAttributeValueMap())
	return instance, nil
}

func loadDefinition(classID me.ClassID, entityID uint16, attributes me.AttributeValueMap) (*me.ManagedEntity, *ResultError) {
	entity, omciErr := me.LoadManagedEntityDefinition(classID, me.ParamData{
		EntityID:   entityID,
		Attributes: cloneAttributes(attributes),
	})
	if omciErr.StatusCode() != me.Success {
		result := omciErr.StatusCode()
		if result == me.ProcessingError {
			result = me.UnknownEntity
		}
		return nil, &ResultError{Result: result, Cause: omciErr.GetError()}
	}
	return entity, nil
}

func validateAccess(entity *me.ManagedEntity, attributes me.AttributeValueMap, required me.AttributeAccess) error {
	definitions := entity.GetAttributeDefinitions()
	var failed uint16
	var unsupported uint16

	for name := range attributes {
		if name == me.ManagedEntityID {
			continue
		}
		found := false
		for index, definition := range definitions {
			if index == 0 || definition.GetName() != name {
				continue
			}
			found = true
			if !me.SupportsAttributeAccess(definition, required) {
				failed |= definition.Mask
			}
			break
		}
		if !found {
			unsupported = 0xffff
		}
	}
	if failed != 0 || unsupported != 0 {
		return &ResultError{
			Result:          me.AttributeFailure,
			FailedMask:      failed,
			UnsupportedMask: unsupported,
		}
	}
	return nil
}

func unknownKeyError(key Key) error {
	if _, result := loadDefinition(key.ClassID, key.EntityID, nil); result != nil {
		return result
	}
	return &ResultError{Result: me.UnknownInstance}
}

func (s *Store) bumpDataSyncLocked() {
	if s.dataSync == 255 {
		s.dataSync = 1
	} else {
		s.dataSync++
		if s.dataSync == 0 {
			s.dataSync = 1
		}
	}
	s.setDataSyncLocked(s.dataSync)
}

func (s *Store) setDataSyncLocked(value uint8) {
	s.dataSync = value
	key := Key{ClassID: me.OnuDataClassID, EntityID: 0}
	instance, exists := s.current[key]
	if !exists {
		return
	}
	instance.Attributes[me.OnuData_MibDataSync] = value
	s.current[key] = instance
}

func cloneInstance(instance Instance) Instance {
	instance.Attributes = cloneAttributes(instance.Attributes)
	return instance
}

func cloneAttributes(attributes me.AttributeValueMap) me.AttributeValueMap {
	copy := make(me.AttributeValueMap, len(attributes))
	for name, value := range attributes {
		copy[name] = cloneValue(value)
	}
	return copy
}

func cloneValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case []byte:
		return append([]byte(nil), typed...)
	case []uint16:
		return append([]uint16(nil), typed...)
	case []uint32:
		return append([]uint32(nil), typed...)
	default:
		return value
	}
}
