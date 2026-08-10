// SPDX-License-Identifier: Apache-2.0

package mib

import (
	"fmt"
	"reflect"
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
	ClassID  me.ClassID `json:"class_id"`
	EntityID uint16     `json:"entity_id"`
}

type Instance struct {
	Key
	Attributes me.AttributeValueMap `json:"attributes"`
	Origin     Origin               `json:"origin"`
}

type Operation string

const (
	OperationCreate Operation = "create"
	OperationSet    Operation = "set"
	OperationDelete Operation = "delete"
	OperationReset  Operation = "reset"
)

// Change is the immutable candidate state passed to the platform backend.
// The store commits it only after Apply returns successfully.
type Change struct {
	Operation   Operation  `json:"operation"`
	Before      *Instance  `json:"before,omitempty"`
	After       *Instance  `json:"after,omitempty"`
	Snapshot    []Instance `json:"snapshot"`
	MIBDataSync uint8      `json:"mib_data_sync"`
}

type Applier interface {
	Apply(Change) error
}

type ApplyFunc func(Change) error

func (f ApplyFunc) Apply(change Change) error {
	return f(change)
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
	applier  Applier
}

func New(factory []Instance) (*Store, error) {
	return NewWithApplier(factory, nil)
}

func NewWithApplier(factory []Instance, applier Applier) (*Store, error) {
	s := &Store{
		factory: make(map[Key]Instance, len(factory)),
		current: make(map[Key]Instance, len(factory)),
		applier: applier,
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

func (s *Store) Exists(key Key) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.current[key]
	return exists
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
	next := cloneInstances(s.current)
	next[key] = instance
	return s.commitLocked(OperationCreate, nil, &instance, next, s.nextDataSyncLocked())
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
	proposed := cloneInstances(s.current)
	proposed[key] = normalized
	return s.commitLocked(OperationSet, &current, &normalized, proposed, s.nextDataSyncLocked())
}

// UpdateAutonomous records attributes changed by the ONU itself. Autonomous
// changes do not advance MIB data sync and are not sent to the platform
// applier: they describe hardware state that has already changed. The returned
// map contains only values that differ from the committed MIB.
func (s *Store) UpdateAutonomous(key Key, attributes me.AttributeValueMap) (me.AttributeValueMap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, exists := s.current[key]
	if !exists {
		return nil, unknownKeyError(key)
	}
	entity, result := loadDefinition(key.ClassID, key.EntityID, current.Attributes)
	if result != nil {
		return nil, result
	}

	definitions := entity.GetAttributeDefinitions()
	var failed uint16
	var unsupported uint16
	for name := range attributes {
		if name == me.ManagedEntityID ||
			(key.ClassID == me.OnuDataClassID && name == me.OnuData_MibDataSync) {
			unsupported = 0xffff
			continue
		}
		definition, err := me.GetAttributeDefinitionByName(definitions, name)
		if err != nil {
			unsupported = 0xffff
			continue
		}
		if !me.SupportsAttributeAccess(*definition, me.Read) {
			failed |= definition.Mask
		}
	}
	if failed != 0 || unsupported != 0 {
		return nil, &ResultError{
			Result:          me.AttributeFailure,
			FailedMask:      failed,
			UnsupportedMask: unsupported,
		}
	}

	next := cloneInstance(current)
	for name, value := range attributes {
		next.Attributes[name] = cloneValue(value)
	}
	normalized, err := normalize(next)
	if err != nil {
		return nil, err
	}

	changed := make(me.AttributeValueMap)
	for name := range attributes {
		value := normalized.Attributes[name]
		if !reflect.DeepEqual(current.Attributes[name], value) {
			changed[name] = cloneValue(value)
		}
	}
	if len(changed) == 0 {
		return changed, nil
	}
	s.current[key] = normalized
	return changed, nil
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

	next := cloneInstances(s.current)
	delete(next, key)
	return s.commitLocked(OperationDelete, &instance, nil, next, s.nextDataSyncLocked())
}

func (s *Store) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := make(map[Key]Instance, len(s.factory))
	for key, instance := range s.factory {
		next[key] = cloneInstance(instance)
	}
	return s.commitLocked(OperationReset, nil, nil, next, 0)
}

func (s *Store) Snapshot() []Instance {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return snapshotInstances(s.current)
}

func snapshotInstances(instances map[Key]Instance) []Instance {
	items := make([]Instance, 0, len(instances))
	for _, instance := range instances {
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

func (s *Store) commitLocked(operation Operation, before, after *Instance,
	next map[Key]Instance, dataSync uint8) error {
	setDataSync(next, dataSync)
	change := Change{
		Operation:   operation,
		Before:      cloneInstancePointer(before),
		After:       cloneInstancePointer(after),
		Snapshot:    snapshotInstances(next),
		MIBDataSync: dataSync,
	}
	if s.applier != nil {
		if err := s.applier.Apply(change); err != nil {
			return &ResultError{Result: me.ProcessingError, Cause: fmt.Errorf("apply platform state: %w", err)}
		}
	}
	s.current = next
	s.dataSync = dataSync
	return nil
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
	classSupport := entity.GetManagedEntityDefinition().GetClassSupport()
	if classSupport == me.UnsupportedManagedEntity ||
		classSupport == me.UnsupportedVendorSpecificManagedEntity {
		return nil, &ResultError{Result: me.UnknownEntity,
			Cause: fmt.Errorf("managed entity class %#x is unknown", classID)}
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

func (s *Store) nextDataSyncLocked() uint8 {
	if s.dataSync == 255 {
		return 1
	}
	next := s.dataSync + 1
	if next == 0 {
		return 1
	}
	return next
}

func (s *Store) setDataSyncLocked(value uint8) {
	s.dataSync = value
	setDataSync(s.current, value)
}

func setDataSync(instances map[Key]Instance, value uint8) {
	key := Key{ClassID: me.OnuDataClassID, EntityID: 0}
	instance, exists := instances[key]
	if !exists {
		return
	}
	instance.Attributes[me.OnuData_MibDataSync] = value
	instances[key] = instance
}

func cloneInstances(instances map[Key]Instance) map[Key]Instance {
	cloned := make(map[Key]Instance, len(instances))
	for key, instance := range instances {
		cloned[key] = cloneInstance(instance)
	}
	return cloned
}

func cloneInstancePointer(instance *Instance) *Instance {
	if instance == nil {
		return nil
	}
	cloned := cloneInstance(*instance)
	return &cloned
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
	case me.TableRows:
		return me.TableRows{NumRows: typed.NumRows, Rows: append([]byte(nil), typed.Rows...)}
	default:
		return value
	}
}
