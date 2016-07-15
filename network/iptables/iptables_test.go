package iptables_test

import (
	"errors"
	"net"
	"os/exec"

	"code.cloudfoundry.org/garden"
	. "code.cloudfoundry.org/garden-linux/network/iptables"
	"github.com/cloudfoundry/gunk/command_runner/fake_command_runner"
	. "github.com/cloudfoundry/gunk/command_runner/fake_command_runner/matchers"
	"code.cloudfoundry.org/lager/lagertest"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Iptables", func() {
	Describe("Chain", func() {
		var fakeRunner *fake_command_runner.FakeCommandRunner
		var subject Chain
		var useKernelLogging bool

		JustBeforeEach(func() {
			fakeRunner = fake_command_runner.New()
			subject = NewLoggingChain("foo-bar-baz", useKernelLogging, fakeRunner, lagertest.NewTestLogger("test"))
		})

		Describe("Setup", func() {
			Context("when kernel logging is not enabled", func() {
				It("creates the log chain using iptables", func() {
					Expect(subject.Setup("logPrefix")).To(Succeed())
					Expect(fakeRunner).To(HaveExecutedSerially(
						fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-F", "foo-bar-baz-log"},
						},
						fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-X", "foo-bar-baz-log"},
						},
						fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-N", "foo-bar-baz-log"},
						},
						fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-A", "foo-bar-baz-log", "-m", "conntrack", "--ctstate", "NEW,UNTRACKED,INVALID", "--protocol", "tcp", "--jump", "NFLOG", "--nflog-prefix", "logPrefix", "--nflog-group", "1"},
						},
						fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-A", "foo-bar-baz-log", "--jump", "RETURN"},
						}))
				})
			})

			Context("when kernel logging is enabled", func() {
				BeforeEach(func() {
					useKernelLogging = true
				})

				It("creates the log chain using iptables", func() {
					Expect(subject.Setup("logPrefix")).To(Succeed())
					Expect(fakeRunner).To(HaveExecutedSerially(
						fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-F", "foo-bar-baz-log"},
						},
						fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-X", "foo-bar-baz-log"},
						},
						fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-N", "foo-bar-baz-log"},
						},
						fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-A", "foo-bar-baz-log", "-m", "conntrack", "--ctstate", "NEW,UNTRACKED,INVALID", "--protocol", "tcp",
								"--jump", "LOG", "--log-prefix", "logPrefix"},
						},
						fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-A", "foo-bar-baz-log", "--jump", "RETURN"},
						}))
				})
			})

			It("ignores failures to flush", func() {
				someError := errors.New("y")
				fakeRunner.WhenRunning(
					fake_command_runner.CommandSpec{
						Path: "/sbin/iptables",
						Args: []string{"-w", "-F", "foo-bar-baz-log"},
					},
					func(cmd *exec.Cmd) error {
						return someError
					})

				Expect(subject.Setup("logPrefix")).To(Succeed())
			})

			It("ignores failures to delete", func() {
				someError := errors.New("y")
				fakeRunner.WhenRunning(
					fake_command_runner.CommandSpec{
						Path: "/sbin/iptables",
						Args: []string{"-w", "-X", "foo-bar-baz-log"},
					},
					func(cmd *exec.Cmd) error {
						return someError
					})

				Expect(subject.Setup("logPrefix")).To(Succeed())
			})

			It("returns any error returned when the table is created", func() {
				someError := errors.New("y")
				fakeRunner.WhenRunning(
					fake_command_runner.CommandSpec{
						Path: "/sbin/iptables",
						Args: []string{"-w", "-N", "foo-bar-baz-log"},
					},
					func(cmd *exec.Cmd) error {
						return someError
					})

				Expect(subject.Setup("logPrefix")).To(MatchError("iptables: log chain setup: y"))
			})

			It("returns any error returned when the logging rule is added", func() {
				someError := errors.New("y")
				fakeRunner.WhenRunning(
					fake_command_runner.CommandSpec{
						Path: "/sbin/iptables",
						Args: []string{"-w", "-A", "foo-bar-baz-log", "-m", "conntrack", "--ctstate", "NEW,UNTRACKED,INVALID", "--protocol", "tcp", "--jump", "LOG", "--log-prefix", "logPrefix"},
					},
					func(cmd *exec.Cmd) error {
						return someError
					})

				Expect(subject.Setup("logPrefix")).To(MatchError("iptables: log chain setup: y"))
			})

			It("returns any error returned when the RETURN rule is added", func() {
				someError := errors.New("y")
				fakeRunner.WhenRunning(
					fake_command_runner.CommandSpec{
						Path: "/sbin/iptables",
						Args: []string{"-w", "-A", "foo-bar-baz-log", "--jump", "RETURN"},
					},
					func(cmd *exec.Cmd) error {
						return someError
					})

				Expect(subject.Setup("logPrefix")).To(MatchError("iptables: log chain setup: y"))
			})
		})

		Describe("TearDown", func() {
			It("should flush and delete the underlying iptables log chain", func() {
				Expect(subject.TearDown()).To(Succeed())
				Expect(fakeRunner).To(HaveExecutedSerially(
					fake_command_runner.CommandSpec{
						Path: "/sbin/iptables",
						Args: []string{"-w", "-F", "foo-bar-baz-log"},
					},
					fake_command_runner.CommandSpec{
						Path: "/sbin/iptables",
						Args: []string{"-w", "-X", "foo-bar-baz-log"},
					}))
			})

			It("ignores failures to flush", func() {
				someError := errors.New("y")
				fakeRunner.WhenRunning(
					fake_command_runner.CommandSpec{
						Path: "/sbin/iptables",
						Args: []string{"-w", "-F", "foo-bar-baz-log"},
					},
					func(cmd *exec.Cmd) error {
						return someError
					})

				Expect(subject.TearDown()).To(Succeed())
			})

			It("ignores failures to delete", func() {
				someError := errors.New("y")
				fakeRunner.WhenRunning(
					fake_command_runner.CommandSpec{
						Path: "/sbin/iptables",
						Args: []string{"-w", "-X", "foo-bar-baz-log"},
					},
					func(cmd *exec.Cmd) error {
						return someError
					})

				Expect(subject.TearDown()).To(Succeed())
			})

		})

		Describe("AppendRule", func() {
			It("runs iptables to create the rule with the correct parameters", func() {
				subject.AppendRule("", "2.0.0.0/11", Return)

				Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
					Path: "/sbin/iptables",
					Args: []string{"-w", "-A", "foo-bar-baz", "--destination", "2.0.0.0/11", "--jump", "RETURN"},
				}))
			})
		})

		Describe("AppendNatRule", func() {
			Context("creating a rule", func() {
				Context("when all parameters are specified", func() {
					It("runs iptables to create the rule with the correct parameters", func() {
						subject.AppendNatRule("1.3.5.0/28", "2.0.0.0/11", Return, net.ParseIP("1.2.3.4"))

						Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-t", "nat", "-A", "foo-bar-baz", "--source", "1.3.5.0/28", "--destination", "2.0.0.0/11", "--jump", "RETURN", "--to", "1.2.3.4"},
						}))
					})
				})

				Context("when Source is not specified", func() {
					It("does not include the --source parameter in the command", func() {
						subject.AppendNatRule("", "2.0.0.0/11", Return, net.ParseIP("1.2.3.4"))

						Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-t", "nat", "-A", "foo-bar-baz", "--destination", "2.0.0.0/11", "--jump", "RETURN", "--to", "1.2.3.4"},
						}))
					})
				})

				Context("when Destination is not specified", func() {
					It("does not include the --destination parameter in the command", func() {
						subject.AppendNatRule("1.3.5.0/28", "", Return, net.ParseIP("1.2.3.4"))

						Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-t", "nat", "-A", "foo-bar-baz", "--source", "1.3.5.0/28", "--jump", "RETURN", "--to", "1.2.3.4"},
						}))
					})
				})

				Context("when To is not specified", func() {
					It("does not include the --to parameter in the command", func() {
						subject.AppendNatRule("1.3.5.0/28", "2.0.0.0/11", Return, nil)

						Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-t", "nat", "-A", "foo-bar-baz", "--source", "1.3.5.0/28", "--destination", "2.0.0.0/11", "--jump", "RETURN"},
						}))
					})
				})

				Context("when the command returns an error", func() {
					It("returns an error", func() {
						someError := errors.New("badly laid iptable")
						fakeRunner.WhenRunning(
							fake_command_runner.CommandSpec{Path: "/sbin/iptables"},
							func(cmd *exec.Cmd) error {
								return someError
							},
						)

						Expect(subject.AppendRule("1.2.3.4/5", "", "")).ToNot(Succeed())
					})
				})
			})

			Describe("DeleteRule", func() {
				It("runs iptables to delete the rule with the correct parameters", func() {
					subject.DeleteRule("", "2.0.0.0/11", Return)

					Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
						Path: "/sbin/iptables",
						Args: []string{"-w", "-D", "foo-bar-baz", "--destination", "2.0.0.0/11", "--jump", "RETURN"},
					}))
				})
			})

			Context("DeleteNatRule", func() {
				Context("when all parameters are specified", func() {
					It("runs iptables to delete the rule with the correct parameters", func() {
						subject.DeleteNatRule("1.3.5.0/28", "2.0.0.0/11", Return, net.ParseIP("1.2.3.4"))

						Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-t", "nat", "-D", "foo-bar-baz", "--source", "1.3.5.0/28", "--destination", "2.0.0.0/11", "--jump", "RETURN", "--to", "1.2.3.4"},
						}))
					})
				})

				Context("when Source is not specified", func() {
					It("does not include the --source parameter in the command", func() {
						subject.DeleteNatRule("", "2.0.0.0/11", Return, net.ParseIP("1.2.3.4"))

						Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-t", "nat", "-D", "foo-bar-baz", "--destination", "2.0.0.0/11", "--jump", "RETURN", "--to", "1.2.3.4"},
						}))
					})
				})

				Context("when Destination is not specified", func() {
					It("does not include the --destination parameter in the command", func() {
						subject.DeleteNatRule("1.3.5.0/28", "", Return, net.ParseIP("1.2.3.4"))

						Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-t", "nat", "-D", "foo-bar-baz", "--source", "1.3.5.0/28", "--jump", "RETURN", "--to", "1.2.3.4"},
						}))
					})
				})

				Context("when To is not specified", func() {
					It("does not include the --to parameter in the command", func() {
						subject.DeleteNatRule("1.3.5.0/28", "2.0.0.0/11", Return, nil)

						Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-t", "nat", "-D", "foo-bar-baz", "--source", "1.3.5.0/28", "--destination", "2.0.0.0/11", "--jump", "RETURN"},
						}))
					})
				})

				Context("when the command returns an error", func() {
					It("returns an error", func() {
						someError := errors.New("badly laid iptable")
						fakeRunner.WhenRunning(
							fake_command_runner.CommandSpec{Path: "/sbin/iptables"},
							func(cmd *exec.Cmd) error {
								return someError
							},
						)

						Expect(subject.DeleteNatRule("1.3.4.5/6", "", "", nil)).ToNot(Succeed())
					})
				})
			})

			Describe("PrependFilterRule", func() {
				Context("when all parameters are defaulted", func() {
					It("runs iptables with appropriate parameters", func() {
						Expect(subject.PrependFilterRule(garden.NetOutRule{})).To(Succeed())
						Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "all", "--jump", "RETURN"},
						}))
					})
				})

				Describe("Network", func() {
					Context("when an empty IPRange is specified", func() {
						It("does not limit the range", func() {
							Expect(subject.PrependFilterRule(garden.NetOutRule{
								Networks: []garden.IPRange{
									garden.IPRange{},
								},
							})).To(Succeed())

							Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
								Path: "/sbin/iptables",
								Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "all", "--jump", "RETURN"},
							}))
						})
					})

					Context("when a single destination IP is specified", func() {
						It("opens only that IP", func() {
							Expect(subject.PrependFilterRule(garden.NetOutRule{
								Networks: []garden.IPRange{
									{
										Start: net.ParseIP("1.2.3.4"),
									},
								},
							})).To(Succeed())

							Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
								Path: "/sbin/iptables",
								Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "all", "--destination", "1.2.3.4", "--jump", "RETURN"},
							}))
						})
					})

					Context("when a multiple destination networks are specified", func() {
						It("opens only that IP", func() {
							Expect(subject.PrependFilterRule(garden.NetOutRule{
								Networks: []garden.IPRange{
									{
										Start: net.ParseIP("1.2.3.4"),
									},
									{
										Start: net.ParseIP("2.2.3.4"),
										End:   net.ParseIP("2.2.3.9"),
									},
								},
							})).To(Succeed())

							Expect(fakeRunner.ExecutedCommands()).To(HaveLen(2))
							Expect(fakeRunner).To(HaveExecutedSerially(
								fake_command_runner.CommandSpec{
									Path: "/sbin/iptables",
									Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "all", "--destination", "1.2.3.4", "--jump", "RETURN"},
								},
								fake_command_runner.CommandSpec{
									Path: "/sbin/iptables",
									Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "all", "-m", "iprange", "--dst-range", "2.2.3.4-2.2.3.9", "--jump", "RETURN"},
								},
							))
						})
					})

					Context("when a EndIP is specified without a StartIP", func() {
						It("opens only that IP", func() {
							Expect(subject.PrependFilterRule(garden.NetOutRule{
								Networks: []garden.IPRange{
									{
										End: net.ParseIP("1.2.3.4"),
									},
								},
							})).To(Succeed())

							Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
								Path: "/sbin/iptables",
								Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "all", "--destination", "1.2.3.4", "--jump", "RETURN"},
							}))
						})
					})

					Context("when a range of IPs is specified", func() {
						It("opens only the range", func() {
							Expect(subject.PrependFilterRule(garden.NetOutRule{
								Networks: []garden.IPRange{
									{
										net.ParseIP("1.2.3.4"), net.ParseIP("2.3.4.5"),
									},
								},
							})).To(Succeed())

							Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
								Path: "/sbin/iptables",
								Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "all", "-m", "iprange", "--dst-range", "1.2.3.4-2.3.4.5", "--jump", "RETURN"},
							}))
						})
					})
				})

				Describe("Ports", func() {
					Context("when a single port is specified", func() {
						It("opens only that port", func() {
							Expect(subject.PrependFilterRule(garden.NetOutRule{
								Protocol: garden.ProtocolTCP,
								Ports: []garden.PortRange{
									garden.PortRangeFromPort(22),
								},
							})).To(Succeed())

							Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
								Path: "/sbin/iptables",
								Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "tcp", "--destination-port", "22", "--jump", "RETURN"},
							}))
						})
					})

					Context("when a port range is specified", func() {
						It("opens that port range", func() {
							Expect(subject.PrependFilterRule(garden.NetOutRule{
								Protocol: garden.ProtocolTCP,
								Ports: []garden.PortRange{
									{12, 24},
								},
							})).To(Succeed())

							Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
								Path: "/sbin/iptables",
								Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "tcp", "--destination-port", "12:24", "--jump", "RETURN"},
							}))
						})
					})

					Context("when multiple port ranges are specified", func() {
						It("opens those port ranges", func() {
							Expect(subject.PrependFilterRule(garden.NetOutRule{
								Protocol: garden.ProtocolTCP,
								Ports: []garden.PortRange{
									{12, 24},
									{64, 942},
								},
							})).To(Succeed())

							Expect(fakeRunner).To(HaveExecutedSerially(
								fake_command_runner.CommandSpec{
									Path: "/sbin/iptables",
									Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "tcp", "--destination-port", "12:24", "--jump", "RETURN"},
								},
								fake_command_runner.CommandSpec{
									Path: "/sbin/iptables",
									Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "tcp", "--destination-port", "64:942", "--jump", "RETURN"},
								},
							))
						})
					})
				})

				Describe("Protocol", func() {
					Context("when tcp protocol is specified", func() {
						It("passes tcp protocol to iptables", func() {
							Expect(subject.PrependFilterRule(garden.NetOutRule{
								Protocol: garden.ProtocolTCP,
							})).To(Succeed())

							Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
								Path: "/sbin/iptables",
								Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "tcp", "--jump", "RETURN"},
							}))
						})
					})

					Context("when udp protocol is specified", func() {
						It("passes udp protocol to iptables", func() {
							Expect(subject.PrependFilterRule(garden.NetOutRule{
								Protocol: garden.ProtocolUDP,
							})).To(Succeed())

							Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
								Path: "/sbin/iptables",
								Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "udp", "--jump", "RETURN"},
							}))
						})
					})

					Context("when icmp protocol is specified", func() {
						It("passes icmp protocol to iptables", func() {
							Expect(subject.PrependFilterRule(garden.NetOutRule{
								Protocol: garden.ProtocolICMP,
							})).To(Succeed())

							Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
								Path: "/sbin/iptables",
								Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "icmp", "--jump", "RETURN"},
							}))
						})

						Context("when icmp type is specified", func() {
							It("passes icmp protcol type to iptables", func() {
								Expect(subject.PrependFilterRule(garden.NetOutRule{
									Protocol: garden.ProtocolICMP,
									ICMPs: &garden.ICMPControl{
										Type: 99,
									},
								})).To(Succeed())

								Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
									Path: "/sbin/iptables",
									Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "icmp", "--icmp-type", "99", "--jump", "RETURN"},
								}))
							})
						})

						Context("when icmp type and code are specified", func() {
							It("passes icmp protcol type and code to iptables", func() {
								Expect(subject.PrependFilterRule(garden.NetOutRule{
									Protocol: garden.ProtocolICMP,
									ICMPs: &garden.ICMPControl{
										Type: 99,
										Code: garden.ICMPControlCode(11),
									},
								})).To(Succeed())

								Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
									Path: "/sbin/iptables",
									Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "icmp", "--icmp-type", "99/11", "--jump", "RETURN"},
								}))
							})
						})
					})
				})

				Describe("Log", func() {
					It("redirects via the log chain if log is specified", func() {
						Expect(subject.PrependFilterRule(garden.NetOutRule{
							Log: true,
						})).To(Succeed())

						Expect(fakeRunner).To(HaveExecutedSerially(fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
							Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "all", "--goto", "foo-bar-baz-log"},
						}))
					})
				})

				Context("when multiple port ranges and multiple networks are specified", func() {
					It("opens the permutations of those port ranges and networks", func() {
						Expect(subject.PrependFilterRule(garden.NetOutRule{
							Protocol: garden.ProtocolTCP,
							Networks: []garden.IPRange{
								{
									Start: net.ParseIP("1.2.3.4"),
								},
								{
									Start: net.ParseIP("2.2.3.4"),
									End:   net.ParseIP("2.2.3.9"),
								},
							},
							Ports: []garden.PortRange{
								{12, 24},
								{64, 942},
							},
						})).To(Succeed())

						Expect(fakeRunner.ExecutedCommands()).To(HaveLen(4))
						Expect(fakeRunner).To(HaveExecutedSerially(
							fake_command_runner.CommandSpec{
								Path: "/sbin/iptables",
								Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "tcp", "--destination", "1.2.3.4", "--destination-port", "12:24", "--jump", "RETURN"},
							},
							fake_command_runner.CommandSpec{
								Path: "/sbin/iptables",
								Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "tcp", "--destination", "1.2.3.4", "--destination-port", "64:942", "--jump", "RETURN"},
							},
							fake_command_runner.CommandSpec{
								Path: "/sbin/iptables",
								Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "tcp", "-m", "iprange", "--dst-range", "2.2.3.4-2.2.3.9", "--destination-port", "12:24", "--jump", "RETURN"},
							},
							fake_command_runner.CommandSpec{
								Path: "/sbin/iptables",
								Args: []string{"-w", "-I", "foo-bar-baz", "1", "--protocol", "tcp", "-m", "iprange", "--dst-range", "2.2.3.4-2.2.3.9", "--destination-port", "64:942", "--jump", "RETURN"},
							},
						))
					})
				})

				Context("when a portrange is specified for ProtocolALL", func() {
					It("returns a nice error message", func() {
						Expect(subject.PrependFilterRule(garden.NetOutRule{
							Protocol: garden.ProtocolAll,
							Ports:    []garden.PortRange{{Start: 1, End: 5}},
						})).To(MatchError("Ports cannot be specified for Protocol ALL"))
					})

					It("does not run iptables", func() {
						subject.PrependFilterRule(garden.NetOutRule{
							Protocol: garden.ProtocolAll,
							Ports:    []garden.PortRange{{Start: 1, End: 5}},
						})

						Expect(fakeRunner.ExecutedCommands()).To(HaveLen(0))
					})
				})

				Context("when a portrange is specified for ProtocolICMP", func() {
					It("returns a nice error message", func() {
						Expect(subject.PrependFilterRule(garden.NetOutRule{
							Protocol: garden.ProtocolICMP,
							Ports:    []garden.PortRange{{Start: 1, End: 5}},
						})).To(MatchError("Ports cannot be specified for Protocol ICMP"))
					})

					It("does not run iptables", func() {
						subject.PrependFilterRule(garden.NetOutRule{
							Protocol: garden.ProtocolICMP,
							Ports:    []garden.PortRange{{Start: 1, End: 5}},
						})

						Expect(fakeRunner.ExecutedCommands()).To(HaveLen(0))
					})
				})

				Context("when an invaild protocol is specified", func() {
					It("returns an error", func() {
						err := subject.PrependFilterRule(garden.NetOutRule{
							Protocol: garden.Protocol(52),
						})
						Expect(err).To(HaveOccurred())
						Expect(err).To(MatchError("invalid protocol: 52"))
					})
				})

				Context("when the command returns an error", func() {
					It("returns a wrapped error, including stderr", func() {
						someError := errors.New("badly laid iptable")
						fakeRunner.WhenRunning(
							fake_command_runner.CommandSpec{Path: "/sbin/iptables"},
							func(cmd *exec.Cmd) error {
								cmd.Stderr.Write([]byte("stderr contents"))
								return someError
							},
						)

						Expect(subject.PrependFilterRule(garden.NetOutRule{})).To(MatchError("iptables: badly laid iptable, stderr contents"))
					})
				})
			})
		})
	})
})
