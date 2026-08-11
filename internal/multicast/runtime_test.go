// SPDX-License-Identifier: Apache-2.0

package multicast

import (
	"errors"
	"net/netip"
	"testing"
	"time"
)

type recordingRuntimeBackend struct {
	configureError error
	changes        []ReplicationChange
	reports        []UpstreamReport
	queries        []DownstreamQuery
}

func (b *recordingRuntimeBackend) Configure(Config) error {
	return b.configureError
}

func (b *recordingRuntimeBackend) SetReplication(change ReplicationChange) error {
	b.changes = append(b.changes, change)
	return nil
}

func (b *recordingRuntimeBackend) SendReport(report UpstreamReport) error {
	b.reports = append(b.reports, report)
	return nil
}

func (b *recordingRuntimeBackend) SendQuery(query DownstreamQuery) error {
	b.queries = append(b.queries, query)
	return nil
}

func TestRuntimeSPRTracksClientsAndAggregatesReports(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	backend := &recordingRuntimeBackend{}
	configured := profile(1, acl(1, "239.1.0.1", "239.1.0.1", 100))
	configured.IGMPFunction = 1
	configured.ImmediateLeave = true
	configured.DynamicACL[0].ImputedBandwidth = 1000
	runtime, err := NewRuntime(Config{
		Profiles: []Profile{configured},
		Subscribers: []Subscriber{{EntityID: 10, Profile: 1,
			Attachments: []Attachment{{Interface: "lan1", BridgeEntity: 0x100}}}},
	}, backend, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	first := membershipMessage("192.0.2.10", [6]byte{2, 0, 0, 0, 0, 10}, ModeIsExclude)
	second := membershipMessage("192.0.2.11", [6]byte{2, 0, 0, 0, 0, 11}, ModeIsExclude)
	if err := runtime.Handle("lan1", first); err != nil {
		t.Fatalf("Handle(first join) error = %v", err)
	}
	if err := runtime.Handle("lan1", second); err != nil {
		t.Fatalf("Handle(second join) error = %v", err)
	}
	if len(backend.changes) != 1 || !backend.changes[0].Enable || len(backend.reports) != 1 ||
		!backend.reports[0].Join {
		t.Fatalf("join backend changes = %+v, reports = %+v", backend.changes, backend.reports)
	}
	monitor := runtime.Monitor(10)
	if len(monitor.Groups) != 2 || monitor.CurrentBandwidth != 1000 || monitor.JoinMessages != 2 {
		t.Fatalf("monitor after joins = %+v", monitor)
	}

	first.Records[0].Type = ChangeToIncludeMode
	second.Records[0].Type = ChangeToIncludeMode
	if err := runtime.Handle("lan1", first); err != nil {
		t.Fatalf("Handle(first leave) error = %v", err)
	}
	if len(backend.changes) != 1 || len(backend.reports) != 1 {
		t.Fatalf("first leave was not suppressed: changes=%+v reports=%+v", backend.changes, backend.reports)
	}
	if err := runtime.Handle("lan1", second); err != nil {
		t.Fatalf("Handle(second leave) error = %v", err)
	}
	if len(backend.changes) != 2 || backend.changes[1].Enable ||
		len(backend.reports) != 2 || backend.reports[1].Join {
		t.Fatalf("final leave backend changes = %+v, reports = %+v", backend.changes, backend.reports)
	}
}

func TestRuntimeSourceSpecificMembershipTransition(t *testing.T) {
	backend := &recordingRuntimeBackend{}
	entry := acl(1, "239.1.0.1", "239.1.0.1", 100)
	entry.Source = addr("192.0.2.1")
	configured := profile(1, entry)
	configured.ImmediateLeave = true
	runtime, err := NewRuntime(Config{
		Profiles: []Profile{configured},
		Subscribers: []Subscriber{{EntityID: 10, Profile: 1,
			Attachments: []Attachment{{Interface: "lan1"}}}},
	}, backend, nil)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	message := membershipMessage("192.0.2.10", [6]byte{2, 0, 0, 0, 0, 10}, ModeIsInclude)
	message.Records[0].Sources = []netip.Addr{addr("192.0.2.1"), addr("192.0.2.2")}
	if err := runtime.Handle("lan1", message); err != nil {
		t.Fatalf("Handle(include) error = %v", err)
	}
	monitor := runtime.Monitor(10)
	if len(monitor.Groups) != 1 || monitor.Groups[0].Source != addr("192.0.2.1") {
		t.Fatalf("source-specific monitor = %+v", monitor)
	}
	if len(backend.changes) != 1 || len(backend.reports) != 1 {
		t.Fatalf("unauthorized source was applied: changes=%d reports=%d", len(backend.changes), len(backend.reports))
	}
	message.Records[0].Type = BlockOldSources
	message.Records[0].Sources = []netip.Addr{addr("192.0.2.1")}
	if err := runtime.Handle("lan1", message); err != nil {
		t.Fatalf("Handle(block) error = %v", err)
	}
	if len(runtime.Monitor(10).Groups) != 0 || len(backend.changes) != 2 || backend.changes[1].Enable {
		t.Fatalf("source was not removed: monitor=%+v changes=%+v", runtime.Monitor(10), backend.changes)
	}
}

func TestRuntimeForwardsConfiguredUnauthorizedJoin(t *testing.T) {
	backend := &recordingRuntimeBackend{}
	configured := profile(1, acl(1, "239.1.0.1", "239.1.0.1", 100))
	configured.UnauthorizedJoinBehaviour = true
	runtime, err := NewRuntime(Config{
		Profiles: []Profile{configured},
		Subscribers: []Subscriber{{EntityID: 10, Profile: 1,
			Attachments: []Attachment{{Interface: "lan1"}}}},
	}, backend, nil)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	message := membershipMessage("192.0.2.10", [6]byte{2, 0, 0, 0, 0, 10}, ModeIsExclude)
	message.Records[0].Group = addr("239.2.0.1")
	if err := runtime.Handle("lan1", message); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(backend.changes) != 0 || len(backend.reports) != 1 || !backend.reports[0].Join ||
		backend.reports[0].Profile.EntityID != 1 {
		t.Fatalf("unauthorized forwarding changes=%+v reports=%+v", backend.changes, backend.reports)
	}
	if monitor := runtime.Monitor(10); monitor.JoinMessages != 0 || len(monitor.Groups) != 0 {
		t.Fatalf("unauthorized join entered monitor: %+v", monitor)
	}
}

func TestRuntimeConfigureFailurePreservesPolicy(t *testing.T) {
	backend := &recordingRuntimeBackend{}
	config := Config{
		Profiles: []Profile{profile(1, acl(1, "239.1.0.1", "239.1.0.1", 100))},
		Subscribers: []Subscriber{{EntityID: 10, Profile: 1,
			Attachments: []Attachment{{Interface: "lan1"}}}},
	}
	runtime, err := NewRuntime(config, backend, nil)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	backend.configureError = errors.New("unsupported")
	if err := runtime.Configure(Config{}); err == nil {
		t.Fatal("Configure() unexpectedly succeeded")
	}
	message := membershipMessage("192.0.2.10", [6]byte{2, 0, 0, 0, 0, 10}, ModeIsExclude)
	if err := runtime.Handle("lan1", message); err != nil {
		t.Fatalf("old policy was not preserved: %v", err)
	}
}

func TestRuntimeEquivalentConfigurePreservesMembership(t *testing.T) {
	backend := &recordingRuntimeBackend{}
	config := Config{
		Profiles: []Profile{profile(1, acl(1, "239.1.0.1", "239.1.0.1", 100))},
		Subscribers: []Subscriber{{EntityID: 10, Profile: 1,
			Attachments: []Attachment{{Interface: "lan1"}}}},
	}
	runtime, err := NewRuntime(config, backend, nil)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if err := runtime.Handle("lan1", membershipMessage("192.0.2.10",
		[6]byte{2, 0, 0, 0, 0, 10}, ModeIsExclude)); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if err := runtime.Configure(config); err != nil {
		t.Fatalf("Configure(equivalent) error = %v", err)
	}
	if monitor := runtime.Monitor(10); len(monitor.Groups) != 1 || monitor.JoinMessages != 1 {
		t.Fatalf("equivalent configure reset state: %+v", monitor)
	}
}

func TestRuntimeDelayedLastMemberLeaveQueriesBeforeRemoval(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	backend := &recordingRuntimeBackend{}
	configured := profile(1, acl(1, "239.1.0.1", "239.1.0.1", 100))
	configured.IGMPFunction = 1
	configured.Robustness = 2
	configured.LastMemberQueryInterval = 1
	runtime, err := NewRuntime(Config{
		Profiles: []Profile{configured},
		Subscribers: []Subscriber{{EntityID: 10, Profile: 1,
			Attachments: []Attachment{{Interface: "lan1", BridgeEntity: 0x100}}}},
	}, backend, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	message := membershipMessage("192.0.2.10", [6]byte{2, 0, 0, 0, 0, 10}, ModeIsExclude)
	if err := runtime.Handle("lan1", message); err != nil {
		t.Fatalf("Handle(join) error = %v", err)
	}
	message.Records[0].Type = ChangeToIncludeMode
	if err := runtime.Handle("lan1", message); err != nil {
		t.Fatalf("Handle(leave) error = %v", err)
	}
	if len(backend.queries) != 1 || len(backend.changes) != 1 || len(backend.reports) != 1 ||
		len(runtime.Monitor(10).Groups) != 1 {
		t.Fatalf("initial last-member state: queries=%d changes=%+v reports=%+v monitor=%+v",
			len(backend.queries), backend.changes, backend.reports, runtime.Monitor(10))
	}

	now = now.Add(100 * time.Millisecond)
	if err := runtime.Expire(); err != nil {
		t.Fatalf("Expire(repeat query) error = %v", err)
	}
	if len(backend.queries) != 2 || len(backend.changes) != 1 {
		t.Fatalf("repeat query state: queries=%d changes=%+v", len(backend.queries), backend.changes)
	}
	now = now.Add(100 * time.Millisecond)
	if err := runtime.Expire(); err != nil {
		t.Fatalf("Expire(final leave) error = %v", err)
	}
	if len(runtime.Monitor(10).Groups) != 0 || len(backend.changes) != 2 || backend.changes[1].Enable ||
		len(backend.reports) != 2 || backend.reports[1].Join {
		t.Fatalf("final delayed leave: monitor=%+v changes=%+v reports=%+v",
			runtime.Monitor(10), backend.changes, backend.reports)
	}
}

func TestRuntimeReportCancelsPendingLastMemberLeave(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	backend := &recordingRuntimeBackend{}
	configured := profile(1, acl(1, "239.1.0.1", "239.1.0.1", 100))
	configured.IGMPFunction = 2
	configured.Robustness = 1
	configured.LastMemberQueryInterval = 1
	runtime, err := NewRuntime(Config{
		Profiles: []Profile{configured},
		Subscribers: []Subscriber{{EntityID: 10, Profile: 1,
			Attachments: []Attachment{{Interface: "lan1", BridgeEntity: 0x100}}}},
	}, backend, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	message := membershipMessage("192.0.2.10", [6]byte{2, 0, 0, 0, 0, 10}, ModeIsExclude)
	if err := runtime.Handle("lan1", message); err != nil {
		t.Fatalf("Handle(join) error = %v", err)
	}
	message.Records[0].Type = ChangeToIncludeMode
	if err := runtime.Handle("lan1", message); err != nil {
		t.Fatalf("Handle(leave) error = %v", err)
	}
	message.Records[0].Type = ModeIsExclude
	now = now.Add(50 * time.Millisecond)
	if err := runtime.Handle("lan1", message); err != nil {
		t.Fatalf("Handle(query response) error = %v", err)
	}
	now = now.Add(100 * time.Millisecond)
	if err := runtime.Expire(); err != nil {
		t.Fatalf("Expire() error = %v", err)
	}
	if len(runtime.Monitor(10).Groups) != 1 || len(backend.changes) != 1 {
		t.Fatalf("query response did not cancel leave: monitor=%+v changes=%+v",
			runtime.Monitor(10), backend.changes)
	}
}

func TestRuntimeProxyGeneralQueryAndMembershipAgeing(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	backend := &recordingRuntimeBackend{}
	configured := profile(1, acl(1, "239.1.0.1", "239.1.0.1", 100))
	configured.IGMPFunction = 2
	configured.Robustness = 1
	configured.QueryInterval = 1
	configured.QueryMaxResponseTime = 1
	runtime, err := NewRuntime(Config{
		Profiles: []Profile{configured},
		Subscribers: []Subscriber{{EntityID: 10, Profile: 1,
			Attachments: []Attachment{{Interface: "lan1", BridgeEntity: 0x100}}}},
	}, backend, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if err := runtime.Handle("lan1", membershipMessage("192.0.2.10",
		[6]byte{2, 0, 0, 0, 0, 10}, ModeIsExclude)); err != nil {
		t.Fatalf("Handle(join) error = %v", err)
	}
	if err := runtime.Expire(); err != nil {
		t.Fatalf("Expire(initial query) error = %v", err)
	}
	if len(backend.queries) != 1 || backend.queries[0].Group.IsValid() {
		t.Fatalf("initial general queries = %+v", backend.queries)
	}
	now = now.Add(1100 * time.Millisecond)
	if err := runtime.Expire(); err != nil {
		t.Fatalf("Expire(ageing) error = %v", err)
	}
	if len(runtime.Monitor(10).Groups) != 0 || len(backend.changes) != 2 || backend.changes[1].Enable ||
		len(backend.reports) != 2 || backend.reports[1].Join {
		t.Fatalf("proxy membership did not age out: monitor=%+v changes=%+v reports=%+v",
			runtime.Monitor(10), backend.changes, backend.reports)
	}
}

func TestRuntimeCopiesRobustnessFromDownstreamQuery(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	backend := &recordingRuntimeBackend{}
	configured := profile(1, acl(1, "239.1.0.1", "239.1.0.1", 100))
	configured.IGMPFunction = 1
	configured.LastMemberQueryInterval = 1
	runtime, err := NewRuntime(Config{
		Profiles: []Profile{configured},
		Subscribers: []Subscriber{{EntityID: 10, Profile: 1,
			Attachments: []Attachment{{Interface: "lan1", BridgeEntity: 0x100}}}},
	}, backend, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if err := runtime.Handle("lan1", MembershipMessage{Kind: MessageQuery, Version: 3,
		Downstream: true, QueryRobustness: 3}); err != nil {
		t.Fatalf("Handle(downstream query) error = %v", err)
	}
	message := membershipMessage("192.0.2.10", [6]byte{2, 0, 0, 0, 0, 10}, ModeIsExclude)
	if err := runtime.Handle("lan1", message); err != nil {
		t.Fatalf("Handle(join) error = %v", err)
	}
	message.Records[0].Type = ChangeToIncludeMode
	if err := runtime.Handle("lan1", message); err != nil {
		t.Fatalf("Handle(leave) error = %v", err)
	}
	for index := 0; index < 2; index++ {
		now = now.Add(100 * time.Millisecond)
		if err := runtime.Expire(); err != nil {
			t.Fatalf("Expire(query %d) error = %v", index, err)
		}
	}
	if len(backend.queries) != 3 || len(runtime.Monitor(10).Groups) != 1 {
		t.Fatalf("learned robustness was not used: queries=%d monitor=%+v",
			len(backend.queries), runtime.Monitor(10))
	}
	now = now.Add(100 * time.Millisecond)
	if err := runtime.Expire(); err != nil {
		t.Fatalf("Expire(final) error = %v", err)
	}
	if len(runtime.Monitor(10).Groups) != 0 {
		t.Fatalf("membership survived learned robustness window: %+v", runtime.Monitor(10))
	}
}

func membershipMessage(client string, mac [6]byte, recordType RecordType) MembershipMessage {
	return MembershipMessage{
		Kind: MessageReport, Version: 3, Client: addr(client), SourceMAC: mac,
		Records: []MembershipRecord{{Type: recordType, Group: addr("239.1.0.1")}},
	}
}
