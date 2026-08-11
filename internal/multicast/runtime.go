// SPDX-License-Identifier: Apache-2.0

package multicast

import (
	"fmt"
	"net/netip"
	"reflect"
	"sort"
	"sync"
	"time"
)

// ReplicationChange asks the platform backend to start or stop delivery of one
// authorized stream on one subscriber attachment.
type ReplicationChange struct {
	Enable     bool
	Subscriber Subscriber
	Attachment Attachment
	Profile    Profile
	Group      ActiveGroup
}

// UpstreamReport is a normalized join or leave that the backend sends through
// the profile's ANI endpoint and GEM port. The backend performs G.988 upstream
// VLAN tag control while constructing the Ethernet frame.
type UpstreamReport struct {
	Join       bool
	Subscriber Subscriber
	Attachment Attachment
	Profile    Profile
	Group      ActiveGroup
	SourceMAC  [6]byte
	Tags       []VLANTag
}

type RuntimeBackend interface {
	Configure(Config) error
	SetReplication(ReplicationChange) error
	SendReport(UpstreamReport) error
}

type noopRuntimeBackend struct{}

func (noopRuntimeBackend) Configure(Config) error                 { return nil }
func (noopRuntimeBackend) SetReplication(ReplicationChange) error { return nil }
func (noopRuntimeBackend) SendReport(UpstreamReport) error        { return nil }

type subscriberBinding struct {
	subscriber Subscriber
	attachment Attachment
}

type clientGroupKey struct {
	subscriberID  uint16
	interfaceName string
	client        netip.Addr
	sourceMAC     [6]byte
	vlan          VLAN
	group         netip.Addr
}

type runtimeStreamKey struct {
	subscriberID  uint16
	interfaceName string
	source        netip.Addr
	group         netip.Addr
	vlan          VLAN
}

type clientIdentity struct {
	address netip.Addr
	mac     [6]byte
}

type runtimeStream struct {
	clients map[clientIdentity]ActiveGroup
}

type rateKey struct {
	subscriberID  uint16
	interfaceName string
	profileID     uint16
}

type rateWindow struct {
	second int64
	count  uint32
}

// Runtime joins packet-level IGMP/MLD state to the G.988 authorization engine.
// It keeps client membership separately so SPR/proxy profiles report only the
// first join and final leave while class 311 still lists every client.
type Runtime struct {
	mu sync.Mutex

	now         func() time.Time
	backend     RuntimeBackend
	engine      *Engine
	config      Config
	profiles    map[uint16]Profile
	bindings    map[string]subscriberBinding
	memberships map[clientGroupKey]map[netip.Addr]struct{}
	streams     map[runtimeStreamKey]*runtimeStream
	rates       map[rateKey]rateWindow
}

func NewRuntime(config Config, backend RuntimeBackend, now func() time.Time) (*Runtime, error) {
	if now == nil {
		now = time.Now
	}
	if backend == nil {
		backend = noopRuntimeBackend{}
	}
	result := &Runtime{now: now, backend: backend}
	if err := result.configureLocked(config); err != nil {
		return nil, err
	}
	return result, nil
}

// Configure atomically replaces policy from the runtime's perspective. The
// backend prepares its static filters before active in-memory membership is
// discarded, so an invalid or unrepresentable graph leaves the old policy live.
func (r *Runtime) Configure(config Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.configureLocked(config)
}

func (r *Runtime) configureLocked(config Config) error {
	if err := Validate(config); err != nil {
		return err
	}
	if r.engine != nil && reflect.DeepEqual(config, r.config) {
		return nil
	}
	profiles := make(map[uint16]Profile, len(config.Profiles))
	for _, profile := range config.Profiles {
		profiles[profile.EntityID] = profile
	}
	bindings := make(map[string]subscriberBinding)
	for _, subscriber := range config.Subscribers {
		if len(subscriber.Attachments) == 0 {
			return fmt.Errorf("multicast subscriber %#x has no attachment", subscriber.EntityID)
		}
		for _, attachment := range subscriber.Attachments {
			if attachment.Interface == "" {
				return fmt.Errorf("multicast subscriber %#x has an empty attachment interface", subscriber.EntityID)
			}
			if previous, exists := bindings[attachment.Interface]; exists {
				return fmt.Errorf("multicast interface %s is shared by subscribers %#x and %#x",
					attachment.Interface, previous.subscriber.EntityID, subscriber.EntityID)
			}
			bindings[attachment.Interface] = subscriberBinding{
				subscriber: subscriber, attachment: attachment,
			}
		}
	}
	engine, err := New(config, r.now)
	if err != nil {
		return err
	}
	if err := r.backend.Configure(config); err != nil {
		return fmt.Errorf("configure multicast backend: %w", err)
	}
	r.engine = engine
	r.config = config
	r.profiles = profiles
	r.bindings = bindings
	r.memberships = make(map[clientGroupKey]map[netip.Addr]struct{})
	r.streams = make(map[runtimeStreamKey]*runtimeStream)
	r.rates = make(map[rateKey]rateWindow)
	return nil
}

// Handle applies every record in one validated report received from a UNI.
// Queries originating from a subscriber are consumed but never treated as a
// subscription request.
func (r *Runtime) Handle(interfaceName string, message MembershipMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	binding, exists := r.bindings[interfaceName]
	if !exists {
		return fmt.Errorf("multicast report arrived on unmanaged interface %s", interfaceName)
	}
	if message.Kind != MessageReport {
		return nil
	}
	for _, record := range message.Records {
		if err := r.applyRecordLocked(binding, message, record); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) applyRecordLocked(binding subscriberBinding, message MembershipMessage,
	record MembershipRecord) error {
	key := clientGroupKey{
		subscriberID: binding.subscriber.EntityID, interfaceName: binding.attachment.Interface,
		client: message.Client, sourceMAC: message.SourceMAC, vlan: message.VLAN, group: record.Group,
	}
	before := cloneSourceSet(r.memberships[key])
	wanted := transitionSources(before, record, message.Client.BitLen())
	after := cloneSourceSet(before)

	for _, source := range sortedSourceDifference(before, wanted) {
		if err := r.removeLocked(binding, key, source, message.Tags); err != nil {
			return err
		}
		delete(after, source)
	}
	for _, source := range sortedSourceDifference(wanted, before) {
		accepted, err := r.addLocked(binding, key, source, message.Tags)
		if err != nil {
			return err
		}
		if accepted {
			after[source] = struct{}{}
		}
	}
	for _, source := range sortedSourceIntersection(before, wanted) {
		if err := r.refreshLocked(binding, key, source, message.Tags); err != nil {
			return err
		}
	}
	if len(after) == 0 {
		delete(r.memberships, key)
	} else {
		r.memberships[key] = after
	}
	return nil
}

func (r *Runtime) addLocked(binding subscriberBinding, client clientGroupKey,
	source netip.Addr, tags []VLANTag) (bool, error) {
	request := Join{SubscriberID: binding.subscriber.EntityID, Interface: binding.attachment.Interface,
		UNIVLAN: client.vlan, Source: source, Group: client.group, Client: client.client}
	streamKey := runtimeStreamKey{subscriberID: request.SubscriberID, interfaceName: request.Interface,
		source: source, group: request.Group, vlan: request.UNIVLAN}
	stream, streamExists := r.streams[streamKey]
	decision := r.engine.Join(request)
	group := activeGroup(request, decision)
	profile, profileExists := r.profiles[decision.ProfileID]
	if !decision.Accepted {
		if decision.ForwardUpstream && profileExists {
			if err := r.sendReportLocked(UpstreamReport{Join: true, Subscriber: binding.subscriber,
				Attachment: binding.attachment, Profile: profile, Group: group,
				SourceMAC: client.sourceMAC, Tags: cloneVLANTags(tags)}); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	if !profileExists {
		r.engine.Leave(request)
		return false, fmt.Errorf("authorized multicast decision references missing profile %#x", decision.ProfileID)
	}
	identity := clientIdentity{address: client.client, mac: client.sourceMAC}
	if !streamExists {
		change := ReplicationChange{Enable: true, Subscriber: binding.subscriber,
			Attachment: binding.attachment, Profile: profile, Group: group}
		if err := r.backend.SetReplication(change); err != nil {
			r.engine.Leave(request)
			return false, fmt.Errorf("enable multicast replication: %w", err)
		}
		stream = &runtimeStream{clients: make(map[clientIdentity]ActiveGroup)}
		r.streams[streamKey] = stream
	}
	stream.clients[identity] = group
	if profile.IGMPFunction == 0 || !streamExists {
		if err := r.sendReportLocked(UpstreamReport{Join: true, Subscriber: binding.subscriber,
			Attachment: binding.attachment, Profile: profile, Group: group,
			SourceMAC: client.sourceMAC, Tags: cloneVLANTags(tags)}); err != nil {
			return true, err
		}
	}
	return true, nil
}

func (r *Runtime) refreshLocked(binding subscriberBinding, client clientGroupKey,
	source netip.Addr, tags []VLANTag) error {
	request := Join{SubscriberID: binding.subscriber.EntityID, Interface: binding.attachment.Interface,
		UNIVLAN: client.vlan, Source: source, Group: client.group, Client: client.client}
	decision := r.engine.Join(request)
	if !decision.Accepted {
		return fmt.Errorf("active multicast stream %s/%s was rejected during refresh", source, client.group)
	}
	profile, exists := r.profiles[decision.ProfileID]
	if !exists {
		return fmt.Errorf("active multicast stream references missing profile %#x", decision.ProfileID)
	}
	streamKey := runtimeStreamKey{subscriberID: request.SubscriberID, interfaceName: request.Interface,
		source: source, group: request.Group, vlan: request.UNIVLAN}
	stream := r.streams[streamKey]
	if stream == nil {
		return fmt.Errorf("active multicast stream %s/%s has no runtime state", source, client.group)
	}
	group := activeGroup(request, decision)
	stream.clients[clientIdentity{address: client.client, mac: client.sourceMAC}] = group
	if profile.IGMPFunction == 0 {
		return r.sendReportLocked(UpstreamReport{Join: true, Subscriber: binding.subscriber,
			Attachment: binding.attachment, Profile: profile, Group: group,
			SourceMAC: client.sourceMAC, Tags: cloneVLANTags(tags)})
	}
	return nil
}

func (r *Runtime) removeLocked(binding subscriberBinding, client clientGroupKey,
	source netip.Addr, tags []VLANTag) error {
	streamKey := runtimeStreamKey{subscriberID: binding.subscriber.EntityID,
		interfaceName: binding.attachment.Interface, source: source, group: client.group, vlan: client.vlan}
	stream := r.streams[streamKey]
	identity := clientIdentity{address: client.client, mac: client.sourceMAC}
	if stream == nil {
		return nil
	}
	group, exists := stream.clients[identity]
	if !exists {
		return nil
	}
	profile, profileExists := r.profiles[group.ProfileID]
	if !profileExists {
		return fmt.Errorf("active multicast stream references missing profile %#x", group.ProfileID)
	}
	last := len(stream.clients) == 1
	if last {
		change := ReplicationChange{Enable: false, Subscriber: binding.subscriber,
			Attachment: binding.attachment, Profile: profile, Group: group}
		if err := r.backend.SetReplication(change); err != nil {
			return fmt.Errorf("disable multicast replication: %w", err)
		}
	}
	request := Join{SubscriberID: binding.subscriber.EntityID, Interface: binding.attachment.Interface,
		UNIVLAN: client.vlan, Source: source, Group: client.group, Client: client.client}
	if !r.engine.Leave(request) {
		return fmt.Errorf("active multicast stream %s/%s is missing from policy state", source, client.group)
	}
	delete(stream.clients, identity)
	if last {
		delete(r.streams, streamKey)
	}
	if profile.IGMPFunction == 0 || last {
		return r.sendReportLocked(UpstreamReport{Join: false, Subscriber: binding.subscriber,
			Attachment: binding.attachment, Profile: profile, Group: group,
			SourceMAC: client.sourceMAC, Tags: cloneVLANTags(tags)})
	}
	return nil
}

func (r *Runtime) sendReportLocked(report UpstreamReport) error {
	limit := report.Profile.UpstreamRate
	if limit != 0 {
		key := rateKey{subscriberID: report.Subscriber.EntityID,
			interfaceName: report.Attachment.Interface, profileID: report.Profile.EntityID}
		now := r.now().Unix()
		window := r.rates[key]
		if window.second != now {
			window = rateWindow{second: now}
		}
		if window.count >= limit {
			r.rates[key] = window
			return nil
		}
		window.count++
		r.rates[key] = window
	}
	if err := r.backend.SendReport(report); err != nil {
		return fmt.Errorf("send upstream multicast report: %w", err)
	}
	return nil
}

// Expire stops replication for previews whose G.988 timer elapsed.
func (r *Runtime) Expire() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, group := range r.engine.Expire() {
		streamKey := runtimeStreamKey{subscriberID: subscriberForInterface(r.bindings, group.Interface),
			interfaceName: group.Interface, source: group.Source, group: group.Group, vlan: group.UNIVLAN}
		stream := r.streams[streamKey]
		if stream == nil {
			continue
		}
		var identity clientIdentity
		for candidate := range stream.clients {
			if candidate.address == group.Client {
				identity = candidate
				break
			}
		}
		delete(stream.clients, identity)
		binding := r.bindings[group.Interface]
		profile := r.profiles[group.ProfileID]
		if len(stream.clients) == 0 {
			if err := r.backend.SetReplication(ReplicationChange{Enable: false,
				Subscriber: binding.subscriber, Attachment: binding.attachment,
				Profile: profile, Group: group}); err != nil {
				return fmt.Errorf("expire multicast replication: %w", err)
			}
			delete(r.streams, streamKey)
		}
		for key, sources := range r.memberships {
			if key.subscriberID == binding.subscriber.EntityID && key.interfaceName == group.Interface &&
				key.client == group.Client && key.vlan == group.UNIVLAN && key.group == group.Group {
				delete(sources, group.Source)
				if len(sources) == 0 {
					delete(r.memberships, key)
				}
			}
		}
		if profile.IGMPFunction != 0 && len(stream.clients) != 0 {
			continue
		}
		if err := r.sendReportLocked(UpstreamReport{Join: false, Subscriber: binding.subscriber,
			Attachment: binding.attachment, Profile: profile, Group: group, SourceMAC: identity.mac}); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) Monitor(subscriberID uint16) Monitor {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.engine.Monitor(subscriberID)
}

func (r *Runtime) SubscriberIDs() []uint16 {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[uint16]struct{}, len(r.config.Subscribers))
	for _, subscriber := range r.config.Subscribers {
		seen[subscriber.EntityID] = struct{}{}
	}
	result := make([]uint16, 0, len(seen))
	for entityID := range seen {
		result = append(result, entityID)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (r *Runtime) Interfaces() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]string, 0, len(r.bindings))
	for interfaceName := range r.bindings {
		result = append(result, interfaceName)
	}
	sort.Strings(result)
	return result
}

func activeGroup(request Join, decision Decision) ActiveGroup {
	return ActiveGroup{
		Interface: request.Interface, Source: request.Source, Group: request.Group,
		Client: request.Client, UNIVLAN: request.UNIVLAN, ANIVLAN: decision.ANIVLAN,
		ProfileID: decision.ProfileID, ACLRowKey: decision.ACLRowKey,
		GEMPortID: decision.GEMPortID, ImputedBandwidth: decision.ImputedBandwidth,
		PreviewUntil: decision.PreviewUntil,
	}
}

func transitionSources(before map[netip.Addr]struct{}, record MembershipRecord,
	bitLength int) map[netip.Addr]struct{} {
	wildcard := netip.IPv4Unspecified()
	if bitLength == 128 {
		wildcard = netip.IPv6Unspecified()
	}
	sources := make(map[netip.Addr]struct{}, len(record.Sources))
	for _, source := range record.Sources {
		sources[source] = struct{}{}
	}
	switch record.Type {
	case ModeIsInclude, ChangeToIncludeMode:
		return sources
	case ModeIsExclude, ChangeToExcludeMode:
		return map[netip.Addr]struct{}{wildcard: {}}
	case AllowNewSources:
		result := cloneSourceSet(before)
		if _, exclude := result[wildcard]; exclude {
			return result
		}
		for source := range sources {
			result[source] = struct{}{}
		}
		return result
	case BlockOldSources:
		result := cloneSourceSet(before)
		if _, exclude := result[wildcard]; exclude {
			return result
		}
		for source := range sources {
			delete(result, source)
		}
		return result
	default:
		return cloneSourceSet(before)
	}
}

func cloneSourceSet(source map[netip.Addr]struct{}) map[netip.Addr]struct{} {
	result := make(map[netip.Addr]struct{}, len(source))
	for address := range source {
		result[address] = struct{}{}
	}
	return result
}

func sortedSourceDifference(left, right map[netip.Addr]struct{}) []netip.Addr {
	var result []netip.Addr
	for source := range left {
		if _, exists := right[source]; !exists {
			result = append(result, source)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Compare(result[j]) < 0 })
	return result
}

func sortedSourceIntersection(left, right map[netip.Addr]struct{}) []netip.Addr {
	var result []netip.Addr
	for source := range left {
		if _, exists := right[source]; exists {
			result = append(result, source)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Compare(result[j]) < 0 })
	return result
}

func subscriberForInterface(bindings map[string]subscriberBinding, interfaceName string) uint16 {
	return bindings[interfaceName].subscriber.EntityID
}

func cloneVLANTags(tags []VLANTag) []VLANTag {
	return append([]VLANTag(nil), tags...)
}
