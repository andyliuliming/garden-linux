package sysconfig

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cloudfoundry-incubator/garden-linux/process"
)

type Config struct {
	CgroupPath             string
	CgroupNodeFilePath     string
	NetworkInterfacePrefix string
	IPTables               IPTablesConfig
	Tag                    string
	DNSServers             []string
}

type IPTablesConfig struct {
	Filter IPTablesFilterConfig
	NAT    IPTablesNATConfig
}

type IPTablesFilterConfig struct {
	AllowHostAccess bool
	InputChain      string
	ForwardChain    string
	DefaultChain    string
	InstancePrefix  string
}

type IPTablesNATConfig struct {
	PreroutingChain  string
	PostroutingChain string
	InstancePrefix   string
}

func NewConfig(tag string, allowHostAccess bool, dnsServers []string) Config {
	return Config{
		NetworkInterfacePrefix: fmt.Sprintf("w%s", tag),
		Tag:        tag,
		DNSServers: dnsServers,

		CgroupPath:         fmt.Sprintf("/tmp/garden-%s/cgroup", tag),
		CgroupNodeFilePath: "/proc/self/cgroup",

		IPTables: IPTablesConfig{
			Filter: IPTablesFilterConfig{
				AllowHostAccess: allowHostAccess,
				InputChain:      fmt.Sprintf("w-%s-input", tag),
				ForwardChain:    fmt.Sprintf("w-%s-forward", tag),
				DefaultChain:    fmt.Sprintf("w-%s-default", tag),
				InstancePrefix:  fmt.Sprintf("w-%s-instance-", tag),
			},
			NAT: IPTablesNATConfig{
				PreroutingChain:  fmt.Sprintf("w-%s-prerouting", tag),
				PostroutingChain: fmt.Sprintf("w-%s-postrouting", tag),
				InstancePrefix:   fmt.Sprintf("w-%s-instance-", tag),
			},
		},
	}
}

func (config Config) Environ() process.Env {
	return process.Env{
		"GARDEN_CGROUP_PATH": config.CgroupPath,

		"GARDEN_NETWORK_INTERFACE_PREFIX": config.NetworkInterfacePrefix,
		"GARDEN_TAG":                      config.Tag,
		"GARDEN_DNS_SERVERS":              strings.Join(config.DNSServers, "\n"),

		"GARDEN_IPTABLES_ALLOW_HOST_ACCESS":  strconv.FormatBool(config.IPTables.Filter.AllowHostAccess),
		"GARDEN_IPTABLES_FILTER_INPUT_CHAIN": config.IPTables.Filter.InputChain,

		"GARDEN_IPTABLES_FILTER_FORWARD_CHAIN":   config.IPTables.Filter.ForwardChain,
		"GARDEN_IPTABLES_FILTER_DEFAULT_CHAIN":   config.IPTables.Filter.DefaultChain,
		"GARDEN_IPTABLES_FILTER_INSTANCE_PREFIX": config.IPTables.Filter.InstancePrefix,

		"GARDEN_IPTABLES_NAT_PREROUTING_CHAIN":  config.IPTables.NAT.PreroutingChain,
		"GARDEN_IPTABLES_NAT_POSTROUTING_CHAIN": config.IPTables.NAT.PostroutingChain,
		"GARDEN_IPTABLES_NAT_INSTANCE_PREFIX":   config.IPTables.NAT.InstancePrefix,
	}
}
