// SPDX-License-Identifier: Apache-2.0

package multicast

import (
	"context"
	"fmt"
	"math"
	"math/bits"
	"net"
	"net/netip"
	"os/exec"
	"sort"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	multicastDownstreamChain = 2013
	normalDownstreamChain    = 2012
)

type CommandRunner interface {
	Run(name string, arguments ...string) error
}

type ExecCommandRunner struct {
	Timeout time.Duration
}

func (r ExecCommandRunner) Run(name string, arguments ...string) error {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, name, arguments...).CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("%s timed out: %w", name, ctx.Err())
	}
	if err != nil {
		return fmt.Errorf("%s %v: %w: %s", name, arguments, err, output)
	}
	return nil
}

type LinuxBackendOptions struct {
	TC     string
	Runner CommandRunner
	Sender func(string, []byte) error
}

type LinuxBackend struct {
	mu sync.Mutex

	tc      string
	runner  CommandRunner
	sender  func(string, []byte) error
	static  map[string][]downstreamRule
	dynamic map[dynamicRuleKey]downstreamRule
}

type downstreamRule struct {
	interfaceName string
	aniVLAN       uint16
	uniVLAN       VLAN
	source        netip.Addr
	start         netip.Addr
	stop          netip.Addr
	profile       Profile
}

type dynamicRuleKey struct {
	subscriberID  uint16
	interfaceName string
	source        netip.Addr
	group         netip.Addr
	uniVLAN       VLAN
}

func NewLinuxBackend(options LinuxBackendOptions) *LinuxBackend {
	if options.TC == "" {
		options.TC = "/sbin/tc"
	}
	if options.Runner == nil {
		options.Runner = ExecCommandRunner{}
	}
	if options.Sender == nil {
		options.Sender = sendEthernetFrame
	}
	return &LinuxBackend{tc: options.TC, runner: options.Runner, sender: options.Sender,
		static: make(map[string][]downstreamRule), dynamic: make(map[dynamicRuleKey]downstreamRule)}
}

func (b *LinuxBackend) Configure(config Config) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	profiles := make(map[uint16]Profile, len(config.Profiles))
	for _, profile := range config.Profiles {
		if err := validateDownstreamProfile(profile); err != nil {
			return err
		}
		profiles[profile.EntityID] = profile
	}
	candidate := make(map[string][]downstreamRule)
	for _, subscriber := range config.Subscribers {
		for _, attachment := range subscriber.Attachments {
			if attachment.BridgeEntity == 0 {
				return fmt.Errorf("multicast subscriber %#x attachment %s has no ANI bridge endpoint",
					subscriber.EntityID, attachment.Interface)
			}
			if len(subscriber.ServicePackages) == 0 {
				profile := profiles[subscriber.Profile]
				candidate[attachment.Interface] = append(candidate[attachment.Interface],
					staticDownstreamRules(attachment.Interface, VLAN{}, profile)...)
				continue
			}
			for _, service := range subscriber.ServicePackages {
				profile := profiles[service.OperationsProfile]
				uniVLAN := servicePacketVLAN(service.VLANID)
				candidate[attachment.Interface] = append(candidate[attachment.Interface],
					staticDownstreamRules(attachment.Interface, uniVLAN, profile)...)
			}
		}
	}
	interfaces := make(map[string]struct{}, len(b.static)+len(candidate))
	for interfaceName := range b.static {
		interfaces[interfaceName] = struct{}{}
	}
	for interfaceName := range candidate {
		interfaces[interfaceName] = struct{}{}
	}
	previousStatic := b.static
	previousDynamic := b.dynamic
	b.static = candidate
	b.dynamic = make(map[dynamicRuleKey]downstreamRule)
	for interfaceName := range interfaces {
		if err := b.rebuildLocked(interfaceName); err != nil {
			b.static = previousStatic
			b.dynamic = previousDynamic
			for rollbackInterface := range interfaces {
				_ = b.rebuildLocked(rollbackInterface)
			}
			return fmt.Errorf("configure multicast filters on %s: %w", interfaceName, err)
		}
	}
	return nil
}

func validateDownstreamProfile(profile Profile) error {
	if profile.DownstreamTagControl > 7 {
		return fmt.Errorf("multicast profile %#x has invalid downstream tag control %d",
			profile.EntityID, profile.DownstreamTagControl)
	}
	// The tc vlan action cannot set DEI. Passing such a profile through would
	// silently emit a different TCI, so reject it at the backend boundary.
	if profile.DownstreamTagControl >= 2 && profile.DownstreamTagControl <= 5 &&
		profile.DownstreamTCI&0x1000 != 0 {
		return fmt.Errorf("multicast profile %#x downstream DEI requires a native VLAN action",
			profile.EntityID)
	}
	return nil
}

func staticDownstreamRules(interfaceName string, uniVLAN VLAN, profile Profile) []downstreamRule {
	result := make([]downstreamRule, 0, len(profile.StaticACL))
	for _, entry := range profile.StaticACL {
		result = append(result, downstreamRule{interfaceName: interfaceName,
			aniVLAN: entry.VLANID, uniVLAN: uniVLAN, source: entry.Source,
			start: entry.Start, stop: entry.Stop, profile: profile})
	}
	return result
}

func servicePacketVLAN(value uint16) VLAN {
	switch value {
	case 4096, math.MaxUint16:
		return VLAN{}
	case 4097:
		return VLAN{Tagged: true}
	default:
		return VLAN{Tagged: true, ID: value}
	}
}

func (b *LinuxBackend) SetReplication(change ReplicationChange) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := dynamicRuleKey{subscriberID: change.Subscriber.EntityID,
		interfaceName: change.Attachment.Interface, source: change.Group.Source,
		group: change.Group.Group, uniVLAN: change.Group.UNIVLAN}
	previous, existed := b.dynamic[key]
	if change.Enable {
		b.dynamic[key] = downstreamRule{interfaceName: change.Attachment.Interface,
			aniVLAN: change.Group.ANIVLAN, uniVLAN: change.Group.UNIVLAN,
			source: change.Group.Source, start: change.Group.Group, stop: change.Group.Group,
			profile: change.Profile}
	} else {
		delete(b.dynamic, key)
	}
	if err := b.rebuildLocked(change.Attachment.Interface); err != nil {
		if existed {
			b.dynamic[key] = previous
		} else {
			delete(b.dynamic, key)
		}
		_ = b.rebuildLocked(change.Attachment.Interface)
		return err
	}
	return nil
}

func (b *LinuxBackend) SendReport(report UpstreamReport) error {
	frame, err := BuildUpstreamFrame(report)
	if err != nil || frame == nil {
		return err
	}
	if report.Attachment.BridgeEntity == 0 {
		return fmt.Errorf("multicast attachment %s has no ANI bridge endpoint", report.Attachment.Interface)
	}
	ani := fmt.Sprintf("oma%04x", report.Attachment.BridgeEntity)
	if err := b.sender(ani, frame); err != nil {
		return fmt.Errorf("send report on %s: %w", ani, err)
	}
	return nil
}

func (b *LinuxBackend) rebuildLocked(interfaceName string) error {
	// Deleting a missing chain is expected on first configuration.
	_ = b.runner.Run(b.tc, "filter", "del", "dev", interfaceName, "egress",
		"chain", strconv.Itoa(multicastDownstreamChain))
	rules := append([]downstreamRule(nil), b.static[interfaceName]...)
	for key, rule := range b.dynamic {
		if key.interfaceName == interfaceName {
			rules = append(rules, rule)
		}
	}
	sort.Slice(rules, func(i, j int) bool {
		if comparison := rules[i].start.Compare(rules[j].start); comparison != 0 {
			return comparison < 0
		}
		if comparison := rules[i].source.Compare(rules[j].source); comparison != 0 {
			return comparison < 0
		}
		return rules[i].profile.EntityID < rules[j].profile.EntityID
	})
	preference := 100
	for _, rule := range rules {
		prefixes, err := addressRangePrefixes(rule.start, rule.stop)
		if err != nil {
			return err
		}
		for _, prefix := range prefixes {
			variants := downstreamVariants(rule.aniVLAN, prefix.Addr().Is4())
			for _, variant := range variants {
				arguments, err := downstreamFilterArguments(interfaceName, preference,
					rule, prefix, variant)
				if err != nil {
					return err
				}
				if err := b.runner.Run(b.tc, arguments...); err != nil {
					return err
				}
				preference++
				if preference >= 64000 {
					return fmt.Errorf("multicast filter count exceeds tc preference space")
				}
			}
		}
	}
	return b.runner.Run(b.tc, "filter", "replace", "dev", interfaceName, "egress",
		"chain", strconv.Itoa(multicastDownstreamChain), "protocol", "all", "pref", "65000",
		"flower", "skip_hw", "action", "drop")
}

type downstreamVariant struct {
	protocol string
	tagged   bool
	tags     int
}

func downstreamVariants(aniVLAN uint16, ipv4 bool) []downstreamVariant {
	untaggedProtocol := "ipv6"
	if ipv4 {
		untaggedProtocol = "ip"
	}
	untagged := downstreamVariant{protocol: untaggedProtocol}
	var tagged []downstreamVariant
	for _, protocol := range []string{"802.1Q", "802.1ad"} {
		for count := 1; count <= 2; count++ {
			tagged = append(tagged, downstreamVariant{protocol: protocol, tagged: true, tags: count})
		}
	}
	switch aniVLAN {
	case 0:
		return []downstreamVariant{untagged}
	case 4097:
		return tagged
	case math.MaxUint16:
		return append([]downstreamVariant{untagged}, tagged...)
	default:
		return tagged
	}
}

func downstreamFilterArguments(interfaceName string, preference int, rule downstreamRule,
	prefix netip.Prefix, variant downstreamVariant) ([]string, error) {
	arguments := []string{"filter", "replace", "dev", interfaceName, "egress", "chain",
		strconv.Itoa(multicastDownstreamChain), "protocol", variant.protocol,
		"pref", strconv.Itoa(preference), "flower", "skip_hw"}
	if variant.tagged {
		arguments = append(arguments, "num_of_vlans", strconv.Itoa(variant.tags))
		if rule.aniVLAN <= 4095 {
			arguments = append(arguments, "vlan_id", strconv.Itoa(int(rule.aniVLAN)))
		}
		encapsulated := "vlan_ethtype"
		if variant.tags == 2 {
			encapsulated = "cvlan_ethtype"
		}
		if prefix.Addr().Is4() {
			arguments = append(arguments, encapsulated, "ip")
		} else {
			arguments = append(arguments, encapsulated, "ipv6")
		}
	}
	if rule.source.IsValid() && !rule.source.IsUnspecified() {
		arguments = append(arguments, "src_ip", rule.source.String())
	}
	arguments = append(arguments, "dst_ip", prefix.String())
	actions, err := downstreamActions(rule.profile, rule.uniVLAN, variant.tagged)
	if err != nil {
		return nil, err
	}
	return append(arguments, actions...), nil
}

func downstreamActions(profile Profile, uniVLAN VLAN, tagged bool) ([]string, error) {
	control := profile.DownstreamTagControl
	tci := profile.DownstreamTCI
	if control >= 5 {
		if uniVLAN.Tagged && uniVLAN.ID <= 4094 {
			tci = tci&0xf000 | uniVLAN.ID
		} else if control == 6 || control == 7 {
			if !uniVLAN.Tagged {
				if tagged {
					return []string{"action", "vlan", "pop", "pipe", "action", "pass"}, nil
				}
				return []string{"action", "pass"}, nil
			}
		}
	}
	vid := strconv.Itoa(int(tci & 0x0fff))
	priority := strconv.Itoa(int(tci >> 13 & 7))
	push := []string{"action", "vlan", "push", "protocol", "802.1Q", "id", vid,
		"priority", priority, "pipe", "action", "pass"}
	modify := []string{"action", "vlan", "modify", "id", vid,
		"priority", priority, "pipe", "action", "pass"}
	switch control {
	case 0:
		return []string{"action", "goto", "chain", strconv.Itoa(normalDownstreamChain)}, nil
	case 1:
		if tagged {
			return []string{"action", "vlan", "pop", "pipe", "action", "pass"}, nil
		}
		return []string{"action", "pass"}, nil
	case 2, 5:
		return push, nil
	case 3, 6:
		if tagged {
			return modify, nil
		}
		return push, nil
	case 4, 7:
		if tagged {
			return []string{"action", "vlan", "modify", "id", vid, "pipe", "action", "pass"}, nil
		}
		return push, nil
	default:
		return nil, fmt.Errorf("invalid downstream multicast tag control %d", control)
	}
}

func addressRangePrefixes(start, stop netip.Addr) ([]netip.Prefix, error) {
	if !start.IsValid() || !stop.IsValid() || start.BitLen() != stop.BitLen() || start.Compare(stop) > 0 {
		return nil, fmt.Errorf("invalid multicast range %s..%s", start, stop)
	}
	if start.Is4() {
		return ipv4RangePrefixes(start, stop), nil
	}
	return ipv6RangePrefixes(start, stop), nil
}

func ipv4RangePrefixes(start, stop netip.Addr) []netip.Prefix {
	current := binaryAddress4(start)
	last := binaryAddress4(stop)
	var result []netip.Prefix
	for current <= last {
		alignment := bits.TrailingZeros32(current)
		remaining := uint64(last) - uint64(current) + 1
		sizeBits := bits.Len64(remaining) - 1
		blockBits := min(alignment, sizeBits)
		result = append(result, netip.PrefixFrom(address4(current), 32-blockBits))
		current += uint32(1) << blockBits
		if current == 0 {
			break
		}
	}
	return result
}

func ipv6RangePrefixes(start, stop netip.Addr) []netip.Prefix {
	current := start.As16()
	last := stop.As16()
	var result []netip.Prefix
	for compare16(current, last) <= 0 {
		alignment := trailingZeros16(current)
		blockBits := alignment
		for blockBits > 0 && compare16(prefixLast16(current, 128-blockBits), last) > 0 {
			blockBits--
		}
		result = append(result, netip.PrefixFrom(netip.AddrFrom16(current), 128-blockBits))
		end := prefixLast16(current, 128-blockBits)
		next, overflow := increment16(end)
		if overflow {
			break
		}
		current = next
	}
	return result
}

func binaryAddress4(address netip.Addr) uint32 {
	value := address.As4()
	return uint32(value[0])<<24 | uint32(value[1])<<16 | uint32(value[2])<<8 | uint32(value[3])
}

func address4(value uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)})
}

func trailingZeros16(value [16]byte) int {
	count := 0
	for index := len(value) - 1; index >= 0; index-- {
		if value[index] == 0 {
			count += 8
			continue
		}
		count += bits.TrailingZeros8(value[index])
		break
	}
	return count
}

func prefixLast16(address [16]byte, prefixLength int) [16]byte {
	result := address
	for bit := prefixLength; bit < 128; bit++ {
		result[bit/8] |= 1 << (7 - bit%8)
	}
	return result
}

func compare16(left, right [16]byte) int {
	for index := range left {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	return 0
}

func increment16(value [16]byte) ([16]byte, bool) {
	for index := len(value) - 1; index >= 0; index-- {
		value[index]++
		if value[index] != 0 {
			return value, false
		}
	}
	return value, true
}

func sendEthernetFrame(interfaceName string, frame []byte) error {
	device, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return err
	}
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_CLOEXEC, int(htons(unix.ETH_P_ALL)))
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	return unix.Sendto(fd, frame, 0, &unix.SockaddrLinklayer{
		Protocol: htons(unix.ETH_P_ALL), Ifindex: device.Index,
	})
}

func htons(value uint16) uint16 {
	return value<<8 | value>>8
}
