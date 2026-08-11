// SPDX-License-Identifier: Apache-2.0

package multicast

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

type recordedCommand struct {
	name      string
	arguments []string
}

type recordingCommandRunner struct {
	commands []recordedCommand
}

func (r *recordingCommandRunner) Run(name string, arguments ...string) error {
	r.commands = append(r.commands, recordedCommand{name: name,
		arguments: append([]string(nil), arguments...)})
	return nil
}

func TestAddressRangePrefixes(t *testing.T) {
	prefixes, err := addressRangePrefixes(addr("239.1.0.1"), addr("239.1.0.6"))
	if err != nil {
		t.Fatalf("addressRangePrefixes(IPv4) error = %v", err)
	}
	if got, want := prefixStrings(prefixes), []string{
		"239.1.0.1/32", "239.1.0.2/31", "239.1.0.4/31", "239.1.0.6/32",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("IPv4 prefixes = %v, want %v", got, want)
	}
	prefixes, err = addressRangePrefixes(addr("ff3e::1"), addr("ff3e::6"))
	if err != nil {
		t.Fatalf("addressRangePrefixes(IPv6) error = %v", err)
	}
	if got, want := prefixStrings(prefixes), []string{
		"ff3e::1/128", "ff3e::2/127", "ff3e::4/127", "ff3e::6/128",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("IPv6 prefixes = %v, want %v", got, want)
	}
}

func TestLinuxBackendBuildsStaticRangeAndDefaultDrop(t *testing.T) {
	runner := &recordingCommandRunner{}
	backend := NewLinuxBackend(LinuxBackendOptions{TC: "tc-test", Runner: runner})
	entry := acl(1, "239.1.0.1", "239.1.0.2", 100)
	configured := profile(1)
	configured.StaticACL = []ACLEntry{entry}
	if err := backend.Configure(Config{
		Profiles: []Profile{configured},
		Subscribers: []Subscriber{{EntityID: 10, Profile: 1,
			Attachments: []Attachment{{Interface: "lan1", BridgeEntity: 0x100}}}},
	}); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	joined := commandLines(runner.commands)
	for _, expected := range []string{
		"tc-test filter del dev lan1 egress chain 2013",
		"dst_ip 239.1.0.1/32 action goto chain 2012",
		"dst_ip 239.1.0.2/32 action goto chain 2012",
		"protocol all pref 65000 flower skip_hw action drop",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("commands do not contain %q:\n%s", expected, joined)
		}
	}
}

func TestLinuxBackendDynamicDownstreamTagReplacement(t *testing.T) {
	runner := &recordingCommandRunner{}
	backend := NewLinuxBackend(LinuxBackendOptions{TC: "tc-test", Runner: runner})
	configured := profile(1, acl(1, "239.1.0.1", "239.1.0.1", 100))
	configured.DownstreamTagControl = 3
	configured.DownstreamTCI = 0xa123
	config := Config{
		Profiles: []Profile{configured},
		Subscribers: []Subscriber{{EntityID: 10, Profile: 1,
			Attachments: []Attachment{{Interface: "lan1", BridgeEntity: 0x100}}}},
	}
	if err := backend.Configure(config); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	runner.commands = nil
	if err := backend.SetReplication(ReplicationChange{Enable: true,
		Subscriber: config.Subscribers[0], Attachment: config.Subscribers[0].Attachments[0],
		Profile: configured, Group: ActiveGroup{Source: netip.IPv4Unspecified(),
			Group: addr("239.1.0.1"), ANIVLAN: 100}},
	); err != nil {
		t.Fatalf("SetReplication() error = %v", err)
	}
	joined := commandLines(runner.commands)
	if !strings.Contains(joined, "vlan_id 100") ||
		!strings.Contains(joined, "action vlan modify id 291 priority 5") {
		t.Fatalf("dynamic filter does not replace downstream TCI:\n%s", joined)
	}
}

func TestLinuxBackendRejectsUnrepresentableDEI(t *testing.T) {
	configured := profile(1)
	configured.DownstreamTagControl = 2
	configured.DownstreamTCI = 0x1001
	backend := NewLinuxBackend(LinuxBackendOptions{Runner: &recordingCommandRunner{}})
	err := backend.Configure(Config{Profiles: []Profile{configured}})
	if err == nil || !strings.Contains(err.Error(), "DEI") {
		t.Fatalf("Configure() error = %v, want DEI rejection", err)
	}
}

func prefixStrings(prefixes []netip.Prefix) []string {
	result := make([]string, len(prefixes))
	for index := range prefixes {
		result[index] = prefixes[index].String()
	}
	return result
}

func commandLines(commands []recordedCommand) string {
	lines := make([]string, len(commands))
	for index, command := range commands {
		lines[index] = command.name + " " + strings.Join(command.arguments, " ")
	}
	return strings.Join(lines, "\n")
}
