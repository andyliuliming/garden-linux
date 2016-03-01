package resource_pool_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime/pprof"
	"time"

	"github.com/blang/semver"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/pivotal-golang/lager/lagertest"

	"github.com/cloudfoundry-incubator/garden"
	"github.com/cloudfoundry-incubator/garden-linux/linux_backend"
	"github.com/cloudfoundry-incubator/garden-linux/linux_container"
	"github.com/cloudfoundry-incubator/garden-linux/linux_container/fake_iptables_manager"
	"github.com/cloudfoundry-incubator/garden-linux/linux_container/fake_quota_manager"
	"github.com/cloudfoundry-incubator/garden-linux/network/bridgemgr/fake_bridge_manager"
	"github.com/cloudfoundry-incubator/garden-linux/network/fakes"
	"github.com/cloudfoundry-incubator/garden-linux/network/iptables"
	"github.com/cloudfoundry-incubator/garden-linux/network/subnets"
	"github.com/cloudfoundry-incubator/garden-linux/port_pool/fake_port_pool"
	"github.com/cloudfoundry-incubator/garden-linux/resource_pool"
	"github.com/cloudfoundry-incubator/garden-linux/resource_pool/fake_filter_provider"
	"github.com/cloudfoundry-incubator/garden-linux/resource_pool/fake_mkdir_chowner"
	"github.com/cloudfoundry-incubator/garden-linux/resource_pool/fake_rootfs_cleaner"
	"github.com/cloudfoundry-incubator/garden-linux/resource_pool/fake_rootfs_provider"
	"github.com/cloudfoundry-incubator/garden-linux/resource_pool/fake_subnet_pool"
	"github.com/cloudfoundry-incubator/garden-linux/sysconfig"
	"github.com/cloudfoundry-incubator/garden-shed/layercake"
	"github.com/cloudfoundry-incubator/garden-shed/rootfs_provider"
	"github.com/cloudfoundry/gunk/command_runner/fake_command_runner"
	. "github.com/cloudfoundry/gunk/command_runner/fake_command_runner/matchers"
)

var _ = Describe("Container pool", func() {

	var (
		depotPath           string
		fakeRunner          *fake_command_runner.FakeCommandRunner
		fakeSubnetPool      *fake_subnet_pool.FakeSubnetPool
		fakeQuotaManager    *fake_quota_manager.FakeQuotaManager
		fakePortPool        *fake_port_pool.FakePortPool
		fakeRootFSProvider  *fake_rootfs_provider.FakeRootFSProvider
		fakeRootFSCleaner   *fake_rootfs_cleaner.FakeRootFSCleaner
		fakeBridges         *fake_bridge_manager.FakeBridgeManager
		fakeIPTablesManager *fake_iptables_manager.FakeIPTablesManager
		fakeFilterProvider  *fake_filter_provider.FakeFilterProvider
		fakeFilter          *fakes.FakeFilter
		pool                *resource_pool.LinuxResourcePool
		config              sysconfig.Config
		containerNetwork    *linux_backend.Network
		defaultVersion      string
		logger              *lagertest.TestLogger
		fakeMkdirChowner    *fake_mkdir_chowner.FakeMkdirChowner
	)

	BeforeEach(func() {
		fakeSubnetPool = new(fake_subnet_pool.FakeSubnetPool)

		var err error
		containerNetwork = &linux_backend.Network{}
		containerNetwork.IP, containerNetwork.Subnet, err = net.ParseCIDR("10.2.0.2/30")
		Expect(err).ToNot(HaveOccurred())
		fakeSubnetPool.AcquireReturns(containerNetwork, nil)

		fakeBridges = new(fake_bridge_manager.FakeBridgeManager)
		fakeIPTablesManager = new(fake_iptables_manager.FakeIPTablesManager)

		fakeBridges.ReserveStub = func(n *net.IPNet, c string) (string, error) {
			return fmt.Sprintf("bridge-for-%s-%s", n, c), nil
		}

		fakeFilter = new(fakes.FakeFilter)
		fakeFilterProvider = new(fake_filter_provider.FakeFilterProvider)
		fakeFilterProvider.ProvideFilterReturns(fakeFilter)

		fakeRunner = fake_command_runner.New()
		fakeQuotaManager = new(fake_quota_manager.FakeQuotaManager)
		fakePortPool = fake_port_pool.New(1000)
		fakeRootFSProvider = new(fake_rootfs_provider.FakeRootFSProvider)
		fakeRootFSCleaner = new(fake_rootfs_cleaner.FakeRootFSCleaner)

		defaultVersion = "1.0.0"
		fakeRootFSProvider.CreateReturns("/provided/rootfs/path", nil, nil)

		depotPath, err = ioutil.TempDir("", "depot-path")
		Expect(err).ToNot(HaveOccurred())

		currentContainerVersion, err := semver.Make("1.0.0")
		Expect(err).ToNot(HaveOccurred())

		config = sysconfig.NewConfig("0", false, nil)
		logger = lagertest.NewTestLogger("test")
		fakeMkdirChowner = new(fake_mkdir_chowner.FakeMkdirChowner)
		pool = resource_pool.New(
			logger,
			"/root/path",
			depotPath,
			config,
			fakeRootFSProvider,
			fakeRootFSCleaner,
			rootfs_provider.MappingList{
				{
					ContainerID: 0,
					HostID:      700000,
					Size:        65536,
				},
			},
			net.ParseIP("1.2.3.4"),
			345,
			fakeSubnetPool,
			fakeBridges,
			fakeIPTablesManager,
			fakeFilterProvider,
			iptables.NewGlobalChain("global-default-chain", fakeRunner, logger),
			fakePortPool,
			[]string{"1.1.0.0/16", "", "2.2.0.0/16"}, // empty string to test that this is ignored
			[]string{"1.1.1.1/32", "", "2.2.2.2/32"},
			fakeRunner,
			fakeQuotaManager,
			currentContainerVersion,
			fakeMkdirChowner,
		)
	})

	AfterEach(func() {
		os.RemoveAll(depotPath)
	})

	Describe("MaxContainer", func() {
		Context("when constrained by network pool size", func() {
			BeforeEach(func() {
				fakeSubnetPool.CapacityReturns(5)
			})

			It("returns the network pool size", func() {
				Expect(pool.MaxContainers()).To(Equal(5))
			})
		})
	})

	Describe("Setup", func() {
		It("executes setup.sh with the correct environment", func() {
			err := pool.Setup()
			Expect(err).ToNot(HaveOccurred())

			Expect(fakeRunner).To(HaveExecutedSerially(
				fake_command_runner.CommandSpec{
					Path: "/root/path/setup.sh",
					Env: []string{
						"CONTAINER_DEPOT_PATH=" + depotPath,
						"PATH=" + os.Getenv("PATH"),
					},
				},
			))
		})

		Context("when setup.sh fails", func() {
			nastyError := errors.New("oh no!")

			BeforeEach(func() {
				fakeRunner.WhenRunning(
					fake_command_runner.CommandSpec{
						Path: "/root/path/setup.sh",
					}, func(*exec.Cmd) error {
						return nastyError
					},
				)
			})

			It("returns the error", func() {
				err := pool.Setup()
				Expect(err).To(Equal(nastyError))
			})
		})

		It("enables disk quotas", func() {
			Expect(pool.Setup()).To(Succeed())
			Expect(fakeQuotaManager.SetupCallCount()).To(Equal(1))
		})

		Context("when setting up disk quotas fails", func() {
			It("returns the error", func() {
				fakeQuotaManager.SetupReturns(errors.New("cant cook wont cook"))
				err := pool.Setup()
				Expect(err).To(MatchError("resource_pool: enable disk quotas: cant cook wont cook"))
			})
		})

		Describe("Setting up IPTables", func() {
			It("sets up global allow and deny rules, adding allow before deny", func() {
				err := pool.Setup()
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeRunner).To(HaveExecutedSerially(
					fake_command_runner.CommandSpec{
						Path: "/root/path/setup.sh", // must run iptables rules after setup.sh
					},
					fake_command_runner.CommandSpec{
						Path: "/sbin/iptables",
						Args: []string{"-w", "-A", "global-default-chain", "--destination", "1.1.1.1/32", "--jump", "RETURN"},
					},
					fake_command_runner.CommandSpec{
						Path: "/sbin/iptables",
						Args: []string{"-w", "-A", "global-default-chain", "--destination", "2.2.2.2/32", "--jump", "RETURN"},
					},
					fake_command_runner.CommandSpec{
						Path: "/sbin/iptables",
						Args: []string{"-w", "-A", "global-default-chain", "--destination", "1.1.0.0/16", "--jump", "REJECT"},
					},
					fake_command_runner.CommandSpec{
						Path: "/sbin/iptables",
						Args: []string{"-w", "-A", "global-default-chain", "--destination", "2.2.0.0/16", "--jump", "REJECT"},
					},
				))
			})

			Context("when setting up a rule fails", func() {
				nastyError := errors.New("oh no!")

				BeforeEach(func() {
					fakeRunner.WhenRunning(
						fake_command_runner.CommandSpec{
							Path: "/sbin/iptables",
						}, func(*exec.Cmd) error {
							return nastyError
						},
					)
				})

				It("returns a wrapped error", func() {
					err := pool.Setup()
					Expect(err).To(MatchError("resource_pool: setting up allow rules in iptables: oh no!"))
				})
			})
		})
	})

	Describe("creating", func() {
		itReleasesTheIPBlock := func() {
			It("returns the container's IP block to the pool", func() {
				Expect(fakeSubnetPool.ReleaseCallCount()).To(Equal(1))
				actualNetwork, _ := fakeSubnetPool.ReleaseArgsForCall(0)
				Expect(actualNetwork).To(Equal(containerNetwork))
			})
		}

		itShouldNotLeakContainerDirectory := func() {
			It("should not leak the container directory", func() {
				entries, err := ioutil.ReadDir(depotPath)
				Expect(err).ToNot(HaveOccurred())
				Expect(entries).To(HaveLen(0))
			})
		}

		itRunsTheDestroyScript := func() {
			It("runs the destroy script", func() {
				executedCommands := fakeRunner.ExecutedCommands()

				createCommand := executedCommands[0]
				Expect(createCommand.Path).To(Equal("/root/path/create.sh"))
				containerPath := createCommand.Args[1]

				lastCommand := executedCommands[len(executedCommands)-1]
				Expect(lastCommand.Path).To(Equal("/root/path/destroy.sh"))
				Expect(lastCommand.Args[1]).To(Equal(containerPath))

				Expect(fakeIPTablesManager.ContainerTeardownCallCount()).To(Equal(1))
			})
		}

		itCleansUpTheRootfs := func() {
			It("cleans up the rootfs for the container", func() {
				Expect(fakeRootFSProvider.DestroyCallCount()).To(Equal(1))
				_, providedID, _ := fakeRootFSProvider.CreateArgsForCall(0)
				_, cleanedUpID := fakeRootFSProvider.DestroyArgsForCall(0)
				Expect(cleanedUpID).To(Equal(providedID))
			})
		}

		itReleasesAndDestroysTheBridge := func() {
			It("releases the bridge", func() {
				Expect(fakeBridges.ReleaseCallCount()).To(Equal(1))
				_, containerId := fakeBridges.ReserveArgsForCall(0)

				Expect(fakeBridges.ReleaseCallCount()).To(Equal(1))
				bridgeName, releasedContainerId := fakeBridges.ReleaseArgsForCall(0)
				Expect(bridgeName).To(Equal("bridge-for-10.2.0.0/30-" + containerId))
				Expect(releasedContainerId).To(Equal(containerId))
			})
		}

		itTearsDownTheIPTableFilters := func() {
			It("tears down the IP table filters", func() {
				Expect(fakeFilterProvider.ProvideFilterCallCount()).To(Equal(2)) // one to setup, one to teae down
				Expect(fakeFilter.TearDownCallCount()).To(Equal(1))
			})

			It("does not leak the iptable setup goroutine", func() {
				Eventually(func() []byte {
					buffer := gbytes.NewBuffer()
					defer buffer.Close()
					Expect(pprof.Lookup("goroutine").WriteTo(buffer, 1)).To(Succeed())

					return buffer.Contents()
				}).ShouldNot(ContainSubstring("Acquire"))
			})
		}

		It("returns containers with unique IDs", func() {
			containerSpec1, err := pool.Acquire(garden.ContainerSpec{})
			Expect(err).ToNot(HaveOccurred())

			containerSpec2, err := pool.Acquire(garden.ContainerSpec{})
			Expect(err).ToNot(HaveOccurred())

			Expect(containerSpec1.ID).ToNot(Equal(containerSpec2.ID))
		})

		It("creates containers with the correct grace time", func() {
			containerSpec, err := pool.Acquire(garden.ContainerSpec{
				GraceTime: 1 * time.Second,
			})
			Expect(err).ToNot(HaveOccurred())

			Expect(containerSpec.GraceTime).To(Equal(1 * time.Second))
		})

		It("creates containers with the correct properties", func() {
			properties := garden.Properties(map[string]string{
				"foo": "bar",
			})

			containerSpec, err := pool.Acquire(garden.ContainerSpec{
				Properties: properties,
			})
			Expect(err).ToNot(HaveOccurred())

			Expect(containerSpec.Properties).To(Equal(properties))
		})

		It("sets up iptable filters for the container", func() {
			containerSpec, err := pool.Acquire(garden.ContainerSpec{Handle: "test-handle"})
			Expect(err).ToNot(HaveOccurred())

			Expect(fakeFilterProvider.ProvideFilterCallCount()).To(BeNumerically(">", 0))
			Expect(fakeFilterProvider.ProvideFilterArgsForCall(0)).To(Equal(containerSpec.ID))
			Expect(fakeFilter.SetupCallCount()).To(Equal(1))
			Expect(fakeFilter.SetupArgsForCall(0)).To(Equal("test-handle"))
		})

		Describe("Disk limit", func() {
			It("should create a rootfs provider with the container's disk quota", func() {
				_, err := pool.Acquire(garden.ContainerSpec{
					Limits: garden.Limits{
						Disk: garden.DiskLimits{
							ByteHard: 98765,
							Scope:    garden.DiskLimitScopeExclusive,
						},
					},
				})
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeRootFSProvider.CreateCallCount()).To(Equal(1))
				_, _, spec := fakeRootFSProvider.CreateArgsForCall(0)
				Expect(spec.QuotaSize).To(Equal(int64(98765)))
				Expect(spec.QuotaScope).To(Equal(garden.DiskLimitScopeExclusive))
			})
		})

		Context("when setting up iptables fails", func() {
			var err error

			BeforeEach(func() {
				fakeFilter.SetupReturns(errors.New("iptables says no"))
				_, err = pool.Acquire(garden.ContainerSpec{})
				Expect(err).To(HaveOccurred())
			})

			It("returns a wrapped error", func() {
				Expect(err).To(MatchError("resource_pool: set up filter: iptables says no"))
			})

			itReleasesTheIPBlock()
			itCleansUpTheRootfs()
			itRunsTheDestroyScript()
		})

		Context("in an unprivileged container", func() {
			It("executes create.sh with a translated rootfs", func() {
				_, err := pool.Acquire(garden.ContainerSpec{Privileged: false})
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeRootFSProvider.CreateCallCount()).To(Equal(1))
				_, _, spec := fakeRootFSProvider.CreateArgsForCall(0)
				Expect(spec.Namespaced).To(Equal(true))
			})

			It("always executes create.sh with a root_uid of 10001", func() {
				for i := 0; i < 2; i++ {
					container, err := pool.Acquire(garden.ContainerSpec{Privileged: false})
					Expect(err).ToNot(HaveOccurred())

					Expect(fakeRunner).To(HaveExecutedSerially(
						fake_command_runner.CommandSpec{
							Path: "/root/path/create.sh",
							Args: []string{path.Join(depotPath, container.ID)},
							Env: []string{
								"PATH=" + os.Getenv("PATH"),
								"bridge_iface=bridge-for-10.2.0.0/30-" + container.ID,
								"container_iface_mtu=345",
								"external_ip=1.2.3.4",
								"id=" + container.ID,
								"network_cidr=10.2.0.0/30",
								"network_cidr_suffix=30",
								"network_container_ip=10.2.0.2",
								"network_host_ip=10.2.0.1",
								"root_uid=700000",
								"rootfs_path=/provided/rootfs/path",
							},
						},
					))
				}
			})
		})

		Context("when the privileged flag is specified and true", func() {
			It("executes create.sh with a root_uid of 0", func() {
				container, err := pool.Acquire(garden.ContainerSpec{Privileged: true})
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeRunner).To(HaveExecutedSerially(
					fake_command_runner.CommandSpec{
						Path: "/root/path/create.sh",
						Args: []string{path.Join(depotPath, container.ID)},
						Env: []string{
							"PATH=" + os.Getenv("PATH"),
							"bridge_iface=bridge-for-10.2.0.0/30-" + container.ID,
							"container_iface_mtu=345",
							"external_ip=1.2.3.4",
							"id=" + container.ID,
							"network_cidr=10.2.0.0/30",
							"network_cidr_suffix=30",
							"network_container_ip=10.2.0.2",
							"network_host_ip=10.2.0.1",
							"root_uid=0",
							"rootfs_path=/provided/rootfs/path",
						},
					},
				))
			})
		})

		Context("when no Network parameter is specified", func() {
			It("executes create.sh with the correct args and environment", func() {
				container, err := pool.Acquire(garden.ContainerSpec{})
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeRunner).To(HaveExecutedSerially(
					fake_command_runner.CommandSpec{
						Path: "/root/path/create.sh",
						Args: []string{path.Join(depotPath, container.ID)},
						Env: []string{
							"PATH=" + os.Getenv("PATH"),
							"bridge_iface=bridge-for-10.2.0.0/30-" + container.ID,
							"container_iface_mtu=345",
							"external_ip=1.2.3.4",
							"id=" + container.ID,
							"network_cidr=10.2.0.0/30",
							"network_cidr_suffix=30",
							"network_container_ip=10.2.0.2",
							"network_host_ip=10.2.0.1",
							"root_uid=700000",
							"rootfs_path=/provided/rootfs/path",
						},
					},
				))
			})
		})

		Context("when the Network parameter is specified", func() {
			It("executes create.sh with the correct args and environment", func() {
				differentNetwork := &linux_backend.Network{}
				differentNetwork.IP, differentNetwork.Subnet, _ = net.ParseCIDR("10.3.0.2/29")
				fakeSubnetPool.AcquireReturns(differentNetwork, nil)

				container, err := pool.Acquire(garden.ContainerSpec{
					Network: "1.3.0.0/30",
				})
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeRunner).To(HaveExecutedSerially(
					fake_command_runner.CommandSpec{
						Path: "/root/path/create.sh",
						Args: []string{path.Join(depotPath, container.ID)},
						Env: []string{
							"PATH=" + os.Getenv("PATH"),
							"bridge_iface=bridge-for-10.3.0.0/29-" + container.ID,
							"container_iface_mtu=345",
							"external_ip=1.2.3.4",
							"id=" + container.ID,
							"network_cidr=10.3.0.0/29",
							"network_cidr_suffix=29",
							"network_container_ip=10.3.0.2",
							"network_host_ip=10.3.0.1",
							"root_uid=700000",
							"rootfs_path=/provided/rootfs/path",
						},
					},
				))
			})

			It("creates the container directory", func() {
				container, err := pool.Acquire(garden.ContainerSpec{})
				Expect(err).To(Succeed())

				containerDir := path.Join(depotPath, container.ID)
				_, err = os.Stat(containerDir)
				Expect(err).ToNot(HaveOccurred())
			})

			Context("when creating the container directory fails", func() {
				JustBeforeEach(func() {
					Expect(os.Remove(depotPath)).To(Succeed())
					ioutil.WriteFile(depotPath, []byte(""), 0755)
				})

				It("returns an error", func() {
					_, err := pool.Acquire(garden.ContainerSpec{})
					Expect(err).To(MatchError(HavePrefix("resource_pool: creating container directory")))
				})
			})

			Describe("allocating the requested network", func() {
				itShouldAcquire := func(subnet subnets.SubnetSelector, ip subnets.IPSelector) {
					Expect(fakeSubnetPool.AcquireCallCount()).To(Equal(1))
					s, i, _ := fakeSubnetPool.AcquireArgsForCall(0)

					Expect(s).To(Equal(subnet))
					Expect(i).To(Equal(ip))
				}

				Context("when the network string is empty", func() {
					It("allocates a dynamic subnet and ip", func() {
						_, err := pool.Acquire(garden.ContainerSpec{Network: ""})
						Expect(err).ToNot(HaveOccurred())

						itShouldAcquire(subnets.DynamicSubnetSelector, subnets.DynamicIPSelector)
					})
				})

				Context("when the network parameter is not empty", func() {
					Context("when it contains a prefix length", func() {
						It("statically allocates the requested subnet ", func() {
							_, err := pool.Acquire(garden.ContainerSpec{Network: "1.2.3.0/30"})
							Expect(err).ToNot(HaveOccurred())

							_, sn, _ := net.ParseCIDR("1.2.3.0/30")
							itShouldAcquire(subnets.StaticSubnetSelector{sn}, subnets.DynamicIPSelector)
						})
					})

					Context("when it does not contain a prefix length", func() {
						It("statically allocates the requested Network from Subnets as a /30", func() {
							_, err := pool.Acquire(garden.ContainerSpec{Network: "1.2.3.0"})
							Expect(err).ToNot(HaveOccurred())

							_, sn, _ := net.ParseCIDR("1.2.3.0/30")
							itShouldAcquire(subnets.StaticSubnetSelector{sn}, subnets.DynamicIPSelector)
						})
					})

					Context("when the network parameter has non-zero host bits", func() {
						It("statically allocates an IP address based on the network parameter", func() {
							_, err := pool.Acquire(garden.ContainerSpec{Network: "1.2.3.1/20"})
							Expect(err).ToNot(HaveOccurred())

							_, sn, _ := net.ParseCIDR("1.2.3.0/20")
							itShouldAcquire(subnets.StaticSubnetSelector{sn}, subnets.StaticIPSelector{net.ParseIP("1.2.3.1")})
						})
					})

					Context("when the network parameter has zero host bits", func() {
						It("dynamically allocates an IP address", func() {
							_, err := pool.Acquire(garden.ContainerSpec{Network: "1.2.3.0/24"})
							Expect(err).ToNot(HaveOccurred())

							_, sn, _ := net.ParseCIDR("1.2.3.0/24")
							itShouldAcquire(subnets.StaticSubnetSelector{sn}, subnets.DynamicIPSelector)
						})
					})

					Context("when an invalid network string is passed", func() {
						It("returns an error", func() {
							_, err := pool.Acquire(garden.ContainerSpec{Network: "not a network"})
							Expect(err).To(MatchError("create container: invalid network spec: invalid CIDR address: not a network/30"))
						})

						It("does not acquire any resources", func() {
							Expect(fakePortPool.Acquired).To(HaveLen(0))
							Expect(fakeSubnetPool.AcquireCallCount()).To(Equal(0))
						})
					})
				})
			})

			Context("when allocation of the specified Network fails", func() {
				var err error
				allocateError := errors.New("allocateError")

				BeforeEach(func() {
					fakeSubnetPool.AcquireReturns(nil, allocateError)

					_, err = pool.Acquire(garden.ContainerSpec{
						Network: "1.2.0.0/30",
					})
				})

				It("returns the error", func() {
					Expect(err).To(Equal(allocateError))
				})

				It("does not execute create.sh", func() {
					Expect(fakeRunner).ToNot(HaveExecutedSerially(
						fake_command_runner.CommandSpec{
							Path: "/root/path/create.sh",
						},
					))
				})

				It("doesn't attempt to release the network if it has not been assigned", func() {
					Expect(fakeSubnetPool.ReleaseCallCount()).To(Equal(0))
				})
			})
		})

		It("saves the bridge name to the depot", func() {
			container, err := pool.Acquire(garden.ContainerSpec{})
			Expect(err).ToNot(HaveOccurred())

			body, err := ioutil.ReadFile(path.Join(depotPath, container.ID, "bridge-name"))
			Expect(err).ToNot(HaveOccurred())

			Expect(string(body)).To(Equal("bridge-for-10.2.0.0/30-" + container.ID))
		})

		It("saves the container version to the depot", func() {
			container, err := pool.Acquire(garden.ContainerSpec{})
			Expect(err).ToNot(HaveOccurred())

			body, err := ioutil.ReadFile(path.Join(depotPath, container.ID, "version"))
			Expect(err).ToNot(HaveOccurred())

			Expect(string(body)).To(Equal(defaultVersion))
		})

		It("initializes the container with the current version", func() {
			spec, err := pool.Acquire(garden.ContainerSpec{})
			Expect(err).ToNot(HaveOccurred())

			Expect(spec.Version).To(Equal(semver.MustParse(defaultVersion)))
		})

		It("runs garbage collection", func() {
			_, err := pool.Acquire(garden.ContainerSpec{})
			Expect(err).NotTo(HaveOccurred())

			Expect(fakeRootFSProvider.GCCallCount()).To(Equal(1))
		})

		Context("when garbage collection fails", func() {
			It("does NOT return an error", func() {
				fakeRootFSProvider.GCReturns(errors.New("potato"))

				_, err := pool.Acquire(garden.ContainerSpec{})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("when a rootfs is specified", func() {
			It("is used to provide a rootfs", func() {
				container, err := pool.Acquire(garden.ContainerSpec{
					RootFSPath: "fake:///path/to/custom-rootfs",
				})
				Expect(err).ToNot(HaveOccurred())

				_, id, spec := fakeRootFSProvider.CreateArgsForCall(0)
				Expect(id).To(Equal(container.ID))
				Expect(spec.RootFS).To(Equal(&url.URL{
					Scheme: "fake",
					Host:   "",
					Path:   "/path/to/custom-rootfs",
				}))
			})

			It("should clean the rootfs", func() {
				fakeRootFSProvider.CreateReturns("/path/to/rootfs", []string{}, nil)

				_, err := pool.Acquire(garden.ContainerSpec{
					RootFSPath: "fake:///path/to/custom-rootfs",
				})
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeRootFSCleaner.CleanCallCount()).To(Equal(1))
				_, path := fakeRootFSCleaner.CleanArgsForCall(0)
				Expect(path).To(Equal("/path/to/rootfs"))
			})

			Context("when cleaning the rootfs fails", func() {
			})

			It("passes the provided rootfs as $rootfs_path to create.sh", func() {
				fakeRootFSProvider.CreateReturns("/var/some/mount/point", nil, nil)

				_, err := pool.Acquire(garden.ContainerSpec{
					RootFSPath: "fake:///path/to/custom-rootfs",
				})
				Expect(err).ToNot(HaveOccurred())
			})

			It("saves the composite rootfs provider to the depot", func() {
				container, err := pool.Acquire(garden.ContainerSpec{
					RootFSPath: "fake:///path/to/custom-rootfs",
				})
				Expect(err).ToNot(HaveOccurred())

				body, err := ioutil.ReadFile(path.Join(depotPath, container.ID, "rootfs-provider"))
				Expect(err).ToNot(HaveOccurred())

				Expect(string(body)).To(Equal("docker-composite"))
			})

			It("returns an error if the supplied environment is invalid", func() {
				_, err := pool.Acquire(garden.ContainerSpec{
					Env: []string{
						"hello",
					},
				})
				Expect(err).To(MatchError(HavePrefix("process: malformed environment")))
			})

			It("merges the env vars associated with the rootfs with those in the spec", func() {
				fakeRootFSProvider.CreateReturns("/provided/rootfs/path", []string{
					"var2=rootfs-value-2",
					"var3=rootfs-value-3",
				}, nil)

				containerSpec, err := pool.Acquire(garden.ContainerSpec{
					RootFSPath: "fake:///path/to/custom-rootfs",
					Env: []string{
						"var1=spec-value1",
						"var2=spec-value2",
					},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(containerSpec.Env).To(Equal([]string{
					"var1=spec-value1",
					"var2=spec-value2",
					"var3=rootfs-value-3",
				}))
			})

			Context("when the rootfs URL is not valid", func() {
				var err error

				BeforeEach(func() {
					_, err = pool.Acquire(garden.ContainerSpec{
						RootFSPath: "::::::",
					})
				})

				It("returns an error", func() {
					Expect(err).To(BeAssignableToTypeOf(&url.Error{}))
				})

				itShouldNotLeakContainerDirectory()

				itReleasesTheIPBlock()

				It("does not acquire a bridge", func() {
					Expect(fakeBridges.ReserveCallCount()).To(Equal(0))
				})
			})

			Context("when providing the mount point fails", func() {
				var err error
				providerErr := errors.New("oh no!")

				BeforeEach(func() {
					fakeRootFSProvider.CreateReturns("", nil, providerErr)

					_, err = pool.Acquire(garden.ContainerSpec{
						RootFSPath: "fake:///path/to/custom-rootfs",
					})
				})

				It("returns the error", func() {
					Expect(err).To(Equal(providerErr))
				})

				itReleasesTheIPBlock()

				itShouldNotLeakContainerDirectory()

				It("does not acquire a bridge", func() {
					Expect(fakeBridges.ReserveCallCount()).To(Equal(0))
				})

				It("does not execute create.sh", func() {
					Expect(fakeRunner).ToNot(HaveExecutedSerially(
						fake_command_runner.CommandSpec{
							Path: "/root/path/create.sh",
						},
					))
				})
			})

			Context("when cleaning the rootfs fails", func() {
				var err error
				providerErr := errors.New("oh no!")

				BeforeEach(func() {
					fakeRootFSCleaner.CleanReturns(providerErr)

					_, err = pool.Acquire(garden.ContainerSpec{
						RootFSPath: "fake:///path/to/custom-rootfs",
					})
				})

				It("returns the error", func() {
					Expect(err).To(Equal(providerErr))
				})

				itReleasesTheIPBlock()

				itShouldNotLeakContainerDirectory()

				It("does not acquire a bridge", func() {
					Expect(fakeBridges.ReserveCallCount()).To(Equal(0))
				})

				It("does not execute create.sh", func() {
					Expect(fakeRunner).ToNot(HaveExecutedSerially(
						fake_command_runner.CommandSpec{
							Path: "/root/path/create.sh",
						},
					))
				})
			})
		})

		Context("when acquiring the bridge fails", func() {
			var err error
			BeforeEach(func() {
				fakeRootFSProvider.CreateReturns("the-rootfs", nil, nil)
				fakeBridges.ReserveReturns("", errors.New("o no"))
				_, err = pool.Acquire(garden.ContainerSpec{
					RootFSPath: "fake:///path/to/custom-rootfs",
				})
			})

			It("does not execute create.sh", func() {
				Expect(fakeRunner).ToNot(HaveExecutedSerially(
					fake_command_runner.CommandSpec{
						Path: "/root/path/create.sh",
					},
				))
			})

			itCleansUpTheRootfs()

			itShouldNotLeakContainerDirectory()

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when bind mounts are specified", func() {
			var (
				container linux_backend.LinuxContainerSpec
			)

			BeforeEach(func() {
				var err error
				container, err = pool.Acquire(garden.ContainerSpec{
					BindMounts: []garden.BindMount{
						{
							SrcPath: "/src/path-ro",
							DstPath: "/dst/path-ro",
							Mode:    garden.BindMountModeRO,
						},
						{
							SrcPath: "/src/path-rw",
							DstPath: "/dst/path-rw",
							Mode:    garden.BindMountModeRW,
						},
						{
							SrcPath: "/src/path-rw",
							DstPath: "/dst/path-rw",
							Mode:    garden.BindMountModeRW,
							Origin:  garden.BindMountOriginContainer,
						},
					},
				})

				Expect(err).ToNot(HaveOccurred())
			})

			It("delegates creating the bind mount directories to the mkdirChowner", func() {
				Expect(fakeMkdirChowner.MkdirChownCallCount()).To(Equal(3))
				path, uid, gid, mode := fakeMkdirChowner.MkdirChownArgsForCall(0)
				Expect(path).To(Equal("/provided/rootfs/path/dst/path-ro"))
				Expect(uid).To(BeEquivalentTo(700000))
				Expect(gid).To(BeEquivalentTo(700000))
				Expect(mode).To(BeEquivalentTo(0755))
			})

			Context("when the mkdirChowner fails", func() {
				It("propagates the error", func() {
					myErr := fmt.Errorf("wow!")
					fakeMkdirChowner.MkdirChownReturns(myErr)
					_, err := pool.Acquire(garden.ContainerSpec{
						BindMounts: []garden.BindMount{{}},
					})

					Expect(err).To(MatchError(ContainSubstring("wow")))
				})
			})

			It("appends mount commands to hook-parent-before-clone.sh", func() {
				containerPath := path.Join(depotPath, container.ID)
				rootfsPath := "/provided/rootfs/path"

				Expect(fakeRunner).To(HaveExecutedSerially(
					fake_command_runner.CommandSpec{
						Path: "bash",
						Args: []string{
							"-c",
							"echo >> " + containerPath + "/lib/hook-parent-before-clone.sh",
						},
					},
					fake_command_runner.CommandSpec{
						Path: "bash",
						Args: []string{
							"-c",
							"echo mount -n --bind /src/path-ro " + rootfsPath + "/dst/path-ro" +
								" >> " + containerPath + "/lib/hook-parent-before-clone.sh",
						},
					},
					fake_command_runner.CommandSpec{
						Path: "bash",
						Args: []string{
							"-c",
							"echo mount -n --bind -o remount,ro /src/path-ro " + rootfsPath + "/dst/path-ro" +
								" >> " + containerPath + "/lib/hook-parent-before-clone.sh",
						},
					},
					fake_command_runner.CommandSpec{
						Path: "bash",
						Args: []string{
							"-c",
							"echo >> " + containerPath + "/lib/hook-parent-before-clone.sh",
						},
					},
					fake_command_runner.CommandSpec{
						Path: "bash",
						Args: []string{
							"-c",
							"echo mount -n --bind /src/path-rw " + rootfsPath + "/dst/path-rw" +
								" >> " + containerPath + "/lib/hook-parent-before-clone.sh",
						},
					},
					fake_command_runner.CommandSpec{
						Path: "bash",
						Args: []string{
							"-c",
							"echo mount -n --bind -o remount,rw /src/path-rw " + rootfsPath + "/dst/path-rw" +
								" >> " + containerPath + "/lib/hook-parent-before-clone.sh",
						},
					},

					fake_command_runner.CommandSpec{
						Path: "bash",
						Args: []string{
							"-c",
							"echo mount -n --bind " + rootfsPath + "/src/path-rw " + rootfsPath + "/dst/path-rw" +
								" >> " + containerPath + "/lib/hook-parent-before-clone.sh",
						},
					},
					fake_command_runner.CommandSpec{
						Path: "bash",
						Args: []string{
							"-c",
							"echo mount -n --bind -o remount,rw " + rootfsPath + "/src/path-rw " + rootfsPath + "/dst/path-rw" +
								" >> " + containerPath + "/lib/hook-parent-before-clone.sh",
						},
					},
				))
			})
		})

		Context("when appending to hook-parent-before-clone.sh fails", func() {
			var err error
			disaster := errors.New("oh no!")

			BeforeEach(func() {
				fakeRunner.WhenRunning(fake_command_runner.CommandSpec{
					Path: "bash",
				}, func(*exec.Cmd) error {
					return disaster
				})

				_, err = pool.Acquire(garden.ContainerSpec{
					BindMounts: []garden.BindMount{
						{
							SrcPath: "/src/path-ro",
							DstPath: "/dst/path-ro",
							Mode:    garden.BindMountModeRO,
						},
						{
							SrcPath: "/src/path-rw",
							DstPath: "/dst/path-rw",
							Mode:    garden.BindMountModeRW,
						},
					},
				})
			})

			It("returns the error", func() {
				Expect(err).To(Equal(disaster))
			})

			itReleasesTheIPBlock()
			itCleansUpTheRootfs()
			itRunsTheDestroyScript()
		})

		Context("when executing create.sh fails", func() {
			nastyError := errors.New("oh no!")
			var err error

			BeforeEach(func() {
				fakeRunner.WhenRunning(
					fake_command_runner.CommandSpec{
						Path: "/root/path/create.sh",
					}, func(cmd *exec.Cmd) error {
						return nastyError
					},
				)

				_, err = pool.Acquire(garden.ContainerSpec{})
			})

			It("returns the error and releases the uid and network", func() {
				Expect(err).To(Equal(nastyError))

				Expect(fakeSubnetPool.ReleaseCallCount()).To(Equal(1))
				actualNetwork, _ := fakeSubnetPool.ReleaseArgsForCall(0)
				Expect(actualNetwork).To(Equal(containerNetwork))
			})

			itReleasesTheIPBlock()
			itRunsTheDestroyScript()
			itCleansUpTheRootfs()
			itReleasesAndDestroysTheBridge()
		})

		Context("when saving the rootfs provider fails", func() {
			var err error

			BeforeEach(func() {
				fakeRunner.WhenRunning(
					fake_command_runner.CommandSpec{
						Path: "/root/path/create.sh",
					}, func(cmd *exec.Cmd) error {
						containerPath := cmd.Args[1]
						rootfsProviderPath := filepath.Join(containerPath, "rootfs-provider")

						// creating a directory with this name will cause the write to the
						// file to fail.
						err := os.MkdirAll(rootfsProviderPath, 0755)
						Expect(err).ToNot(HaveOccurred())

						return nil
					},
				)

				_, err = pool.Acquire(garden.ContainerSpec{})
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
			})

			itReleasesTheIPBlock()
			itCleansUpTheRootfs()
			itRunsTheDestroyScript()
		})

		Context("the container environment is invalid", func() {
			var err error

			BeforeEach(func() {
				_, err = pool.Acquire(garden.ContainerSpec{
					Env: []string{
						"hello=world",
						"invalidstring",
						"",
						"=12",
					},
				})
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
			})

			itTearsDownTheIPTableFilters()
			itReleasesTheIPBlock()
			itRunsTheDestroyScript()
			itCleansUpTheRootfs()
			itReleasesAndDestroysTheBridge()
		})
	})

	Describe("restoring", func() {
		var snapshot io.Reader
		var buf *bytes.Buffer

		var containerNetwork *linux_backend.Network
		var rootUID int
		var bridgeName string

		BeforeEach(func() {
			rootUID = 10001

			buf = new(bytes.Buffer)
			snapshot = buf
			_, subnet, _ := net.ParseCIDR("2.3.4.5/29")
			containerNetwork = &linux_backend.Network{
				Subnet: subnet,
				IP:     net.ParseIP("1.2.3.4"),
			}

			bridgeName = "some-bridge"
		})

		JustBeforeEach(func() {
			err := json.NewEncoder(buf).Encode(
				linux_container.ContainerSnapshot{
					ID:     "some-restored-id",
					Handle: "some-restored-handle",

					GraceTime: 1 * time.Second,

					State: "some-restored-state",
					Events: []string{
						"some-restored-event",
						"some-other-restored-event",
					},

					Resources: linux_container.ResourcesSnapshot{
						RootUID: rootUID,
						Network: containerNetwork,
						Bridge:  bridgeName,
						Ports:   []uint32{61001, 61002, 61003},
					},

					Properties: map[string]string{
						"foo": "bar",
					},
				},
			)
			Expect(err).ToNot(HaveOccurred())
		})

		It("constructs a container from the snapshot", func() {
			containerSpec, err := pool.Restore(snapshot)
			Expect(err).ToNot(HaveOccurred())

			Expect(containerSpec.ID).To(Equal("some-restored-id"))
			Expect(containerSpec.Handle).To(Equal("some-restored-handle"))
			Expect(containerSpec.GraceTime).To(Equal(1 * time.Second))
			Expect(containerSpec.Properties).To(Equal(garden.Properties(map[string]string{
				"foo": "bar",
			})))

			Expect(containerSpec.State).To(Equal(linux_backend.State("some-restored-state")))
			Expect(containerSpec.Events).To(Equal([]string{
				"some-restored-event",
				"some-other-restored-event",
			}))

			Expect(containerSpec.Resources.Network).To(Equal(containerNetwork))
			Expect(containerSpec.Resources.Bridge).To(Equal("some-bridge"))
		})

		Context("when a version file exists in the container", func() {
			var (
				expectedVersion semver.Version
				containerSpec   linux_backend.LinuxContainerSpec
				versionFilePath string
			)

			JustBeforeEach(func() {
				var err error

				expectedVersion, err = semver.Make("1.0.0")
				Expect(err).ToNot(HaveOccurred())

				id := "some-restored-id"
				Expect(os.MkdirAll(filepath.Join(depotPath, id), 0755)).To(Succeed())
				versionFilePath = filepath.Join(depotPath, id, "version")

				err = ioutil.WriteFile(versionFilePath, []byte(expectedVersion.String()), 0644)
				Expect(err).ToNot(HaveOccurred())

				containerSpec, err = pool.Restore(snapshot)
				Expect(err).ToNot(HaveOccurred())
			})

			AfterEach(func() {
				Expect(os.RemoveAll(versionFilePath)).To(Succeed())
			})

			It("restores the container version", func() {
				Expect(containerSpec.Version).To(Equal(expectedVersion))
			})
		})

		Context("when a version file does not exists in the container", func() {
			var (
				expectedVersion semver.Version
				containerSpec   linux_backend.LinuxContainerSpec
			)

			JustBeforeEach(func() {
				var err error

				expectedVersion, err = semver.Make("0.0.0")
				Expect(err).ToNot(HaveOccurred())

				containerSpec, err = pool.Restore(snapshot)
				Expect(err).ToNot(HaveOccurred())
			})

			It("restores the empty version", func() {
				Expect(containerSpec.Version).To(Equal(expectedVersion))
			})
		})

		It("removes its network from the pool", func() {
			_, err := pool.Restore(snapshot)
			Expect(err).ToNot(HaveOccurred())

			Expect(fakeSubnetPool.RemoveCallCount()).To(Equal(1))
			actualNetwork, _ := fakeSubnetPool.RemoveArgsForCall(0)
			Expect(actualNetwork).To(Equal(containerNetwork))
		})

		It("removes its ports from the pool", func() {
			_, err := pool.Restore(snapshot)
			Expect(err).ToNot(HaveOccurred())

			Expect(fakePortPool.Removed).To(ContainElement(uint32(61001)))
			Expect(fakePortPool.Removed).To(ContainElement(uint32(61002)))
			Expect(fakePortPool.Removed).To(ContainElement(uint32(61003)))
		})

		It("rereserves the bridge for the subnet from the pool", func() {
			_, err := pool.Restore(snapshot)
			Expect(err).ToNot(HaveOccurred())

			Expect(fakeBridges.RereserveCallCount()).To(Equal(1))
			bridgeName, subnet, containerId := fakeBridges.RereserveArgsForCall(0)
			Expect(bridgeName).To(Equal("some-bridge"))
			Expect(subnet.String()).To(Equal("2.3.4.0/29"))
			Expect(containerId).To(Equal("some-restored-id"))
		})

		Context("when rereserving the bridge fails", func() {
			var err error

			JustBeforeEach(func() {
				fakeBridges.RereserveReturns(errors.New("boom"))
				_, err = pool.Restore(snapshot)
			})

			It("returns the error", func() {
				Expect(err).To(HaveOccurred())
			})

			It("returns the subnet to the pool", func() {
				Expect(fakeSubnetPool.ReleaseCallCount()).To(Equal(1))
				actualNetwork, _ := fakeSubnetPool.ReleaseArgsForCall(0)
				Expect(actualNetwork).To(Equal(containerNetwork))
			})
		})

		Context("when decoding the snapshot fails", func() {
			BeforeEach(func() {
				snapshot = new(bytes.Buffer)
			})

			It("fails", func() {
				_, err := pool.Restore(snapshot)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when removing the network from the pool fails", func() {
			disaster := errors.New("oh no!")

			JustBeforeEach(func() {
				fakeSubnetPool.RemoveReturns(disaster)
			})

			It("returns the error", func() {
				_, err := pool.Restore(snapshot)
				Expect(err).To(Equal(disaster))
			})
		})

		Context("when removing a port from the pool fails", func() {
			disaster := errors.New("oh no!")

			JustBeforeEach(func() {
				fakePortPool.RemoveError = disaster
			})

			It("returns the error and releases the network and all ports", func() {
				_, err := pool.Restore(snapshot)
				Expect(err).To(Equal(disaster))

				Expect(fakeSubnetPool.ReleaseCallCount()).To(Equal(1))
				actualNetwork, _ := fakeSubnetPool.ReleaseArgsForCall(0)
				Expect(actualNetwork).To(Equal(containerNetwork))

				Expect(fakePortPool.Released).To(ContainElement(uint32(61001)))
				Expect(fakePortPool.Released).To(ContainElement(uint32(61002)))
				Expect(fakePortPool.Released).To(ContainElement(uint32(61003)))
			})

			Context("when the container is privileged", func() {
				BeforeEach(func() {
					rootUID = 0
				})

				It("returns the error", func() {
					_, err := pool.Restore(snapshot)
					Expect(err).To(Equal(disaster))
				})
			})
		})
	})

	Describe("pruning", func() {
		Context("when containers are found in the depot", func() {
			BeforeEach(func() {
				err := os.MkdirAll(path.Join(depotPath, "container-1"), 0755)
				Expect(err).ToNot(HaveOccurred())

				err = os.MkdirAll(path.Join(depotPath, "container-2"), 0755)
				Expect(err).ToNot(HaveOccurred())

				err = os.MkdirAll(path.Join(depotPath, "container-3"), 0755)
				Expect(err).ToNot(HaveOccurred())

				err = os.MkdirAll(path.Join(depotPath, "tmp"), 0755)
				Expect(err).ToNot(HaveOccurred())

				err = ioutil.WriteFile(path.Join(depotPath, "container-1", "bridge-name"), []byte("fake-bridge-1"), 0644)
				Expect(err).ToNot(HaveOccurred())

				err = ioutil.WriteFile(path.Join(depotPath, "container-2", "bridge-name"), []byte("fake-bridge-2"), 0644)
				Expect(err).ToNot(HaveOccurred())

				err = ioutil.WriteFile(path.Join(depotPath, "container-1", "rootfs-provider"), []byte("docker-remote-vfs"), 0644)
				Expect(err).ToNot(HaveOccurred())

				err = ioutil.WriteFile(path.Join(depotPath, "container-2", "rootfs-provider"), []byte("docker-remote-vfs"), 0644)
				Expect(err).ToNot(HaveOccurred())

				err = ioutil.WriteFile(path.Join(depotPath, "container-3", "rootfs-provider"), []byte(""), 0644)
				Expect(err).ToNot(HaveOccurred())
			})

			It("destroys each container", func() {
				err := pool.Prune(map[string]bool{})
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeIPTablesManager.ContainerTeardownCallCount()).To(Equal(3))

				containerID := fakeIPTablesManager.ContainerTeardownArgsForCall(0)
				Expect(containerID).To(Equal("container-1"))

				containerID = fakeIPTablesManager.ContainerTeardownArgsForCall(1)
				Expect(containerID).To(Equal("container-2"))

				containerID = fakeIPTablesManager.ContainerTeardownArgsForCall(2)
				Expect(containerID).To(Equal("container-3"))

				Expect(fakeRunner).To(HaveExecutedSerially(
					fake_command_runner.CommandSpec{
						Path: "/root/path/destroy.sh",
						Args: []string{path.Join(depotPath, "container-1")},
					},
					fake_command_runner.CommandSpec{
						Path: "/root/path/destroy.sh",
						Args: []string{path.Join(depotPath, "container-2")},
					},
					fake_command_runner.CommandSpec{
						Path: "/root/path/destroy.sh",
						Args: []string{path.Join(depotPath, "container-3")},
					},
				))
			})

			Context("after destroying it", func() {
				BeforeEach(func() {
					fakeRunner.WhenRunning(
						fake_command_runner.CommandSpec{
							Path: "/root/path/destroy.sh",
						}, func(cmd *exec.Cmd) error {
							return os.RemoveAll(cmd.Args[0])
						},
					)
				})

				It("cleans up each container's rootfs after destroying it", func() {
					err := pool.Prune(map[string]bool{})
					Expect(err).ToNot(HaveOccurred())

					Expect(fakeRootFSProvider.DestroyCallCount()).To(Equal(2))
					_, id1 := fakeRootFSProvider.DestroyArgsForCall(0)
					_, id2 := fakeRootFSProvider.DestroyArgsForCall(1)
					Expect(id1).To(Equal("container-1"))
					Expect(id2).To(Equal("container-2"))

					Expect(fakeIPTablesManager.ContainerTeardownCallCount()).To(Equal(3))

					containerID1 := fakeIPTablesManager.ContainerTeardownArgsForCall(0)
					Expect(containerID1).To(Equal("container-1"))

					containerID2 := fakeIPTablesManager.ContainerTeardownArgsForCall(1)
					Expect(containerID2).To(Equal("container-2"))
				})

				It("releases the bridge", func() {
					err := pool.Prune(map[string]bool{})
					Expect(err).ToNot(HaveOccurred())

					Expect(fakeBridges.ReleaseCallCount()).To(Equal(2))

					bridge, containerId := fakeBridges.ReleaseArgsForCall(0)
					Expect(bridge).To(Equal("fake-bridge-1"))
					Expect(containerId).To(Equal("container-1"))

					bridge, containerId = fakeBridges.ReleaseArgsForCall(1)
					Expect(bridge).To(Equal("fake-bridge-2"))
					Expect(containerId).To(Equal("container-2"))

					Expect(fakeIPTablesManager.ContainerTeardownCallCount()).To(Equal(3))

					containerID1 := fakeIPTablesManager.ContainerTeardownArgsForCall(0)
					Expect(containerID1).To(Equal("container-1"))

					containerID2 := fakeIPTablesManager.ContainerTeardownArgsForCall(1)
					Expect(containerID2).To(Equal("container-2"))
				})
			})

			Context("when a container does not declare a bridge name", func() {
				It("does nothing much", func() {
					err := pool.Prune(map[string]bool{"container-1": true, "container-2": true})
					Expect(err).ToNot(HaveOccurred())

					Expect(fakeBridges.ReleaseCallCount()).To(Equal(0))
				})
			})

			Context("when a container declares a docker rootfs provider", func() {
				BeforeEach(func() {
					err := ioutil.WriteFile(path.Join(depotPath, "container-2", "rootfs-provider"), []byte("docker"), 0644)
					Expect(err).ToNot(HaveOccurred())
				})

				It("does not clean the rootfs", func() {
					err := pool.Prune(map[string]bool{})
					Expect(err).ToNot(HaveOccurred())

					for i := 0; i < fakeRootFSProvider.DestroyCallCount(); i++ {
						_, arg := fakeRootFSProvider.DestroyArgsForCall(i)
						Expect(arg).ToNot(Equal(layercake.ContainerID("container-2")))
					}
				})
			})

			Context("when a container declares a rootfs provider", func() {
				BeforeEach(func() {
					err := os.MkdirAll(path.Join(depotPath, "container-4"), 0755)
					Expect(err).ToNot(HaveOccurred())

					err = ioutil.WriteFile(path.Join(depotPath, "container-4", "rootfs-provider"), []byte("docker-composite"), 0644)
					Expect(err).ToNot(HaveOccurred())
				})

				It("cleans up the rootfs", func() {
					err := pool.Prune(map[string]bool{})
					Expect(err).ToNot(HaveOccurred())

					Expect(fakeRootFSProvider.DestroyCallCount()).To(Equal(3))
					_, id1 := fakeRootFSProvider.DestroyArgsForCall(0)
					_, id3 := fakeRootFSProvider.DestroyArgsForCall(2)
					Expect(id1).To(Equal("container-1"))
					Expect(id3).To(Equal("container-4"))

					Expect(fakeIPTablesManager.ContainerTeardownCallCount()).To(Equal(4))
					containerID := fakeIPTablesManager.ContainerTeardownArgsForCall(0)
					Expect(containerID).To(Equal("container-1"))
					containerID = fakeIPTablesManager.ContainerTeardownArgsForCall(3)
					Expect(containerID).To(Equal("container-4"))
				})
			})

			Context("when a container does not declare a rootfs provider", func() {
				BeforeEach(func() {
					err := os.Remove(path.Join(depotPath, "container-2", "rootfs-provider"))
					Expect(err).ToNot(HaveOccurred())
				})

				JustBeforeEach(func() {
					err := pool.Prune(map[string]bool{})
					Expect(err).ToNot(HaveOccurred())
				})

				It("cleans it up using the default provider", func() {
					Expect(fakeRootFSProvider.DestroyCallCount()).To(Equal(2))
					_, id1 := fakeRootFSProvider.DestroyArgsForCall(0)
					_, id2 := fakeRootFSProvider.DestroyArgsForCall(1)
					Expect(id1).To(Equal("container-1"))
					Expect(id2).To(Equal("container-2"))
				})

				Context("when a container exists with an unknown rootfs provider", func() {
					BeforeEach(func() {
						err := ioutil.WriteFile(path.Join(depotPath, "container-2", "rootfs-provider"), []byte("unknown"), 0644)
						Expect(err).ToNot(HaveOccurred())
					})

					It("ignores the error", func() {
						for i := 0; i < fakeRootFSProvider.DestroyCallCount(); i++ {
							_, arg := fakeRootFSProvider.DestroyArgsForCall(i)
							Expect(arg).ToNot(Equal(layercake.ContainerID("container-2")))
						}
					})

					It("cleans up the iptables", func() {
						Expect(fakeIPTablesManager.ContainerTeardownCallCount()).To(Equal(3))
						containerID := fakeIPTablesManager.ContainerTeardownArgsForCall(0)
						Expect(containerID).To(Equal("container-1"))
						containerID = fakeIPTablesManager.ContainerTeardownArgsForCall(1)
						Expect(containerID).To(Equal("container-2"))
						containerID = fakeIPTablesManager.ContainerTeardownArgsForCall(2)
						Expect(containerID).To(Equal("container-3"))
					})
				})

				Context("when a container exists with an empty rootfs provider", func() {
					BeforeEach(func() {
						err := ioutil.WriteFile(path.Join(depotPath, "container-2", "rootfs-provider"), []byte(""), 0644)
						Expect(err).ToNot(HaveOccurred())
					})

					It("does not clean the rootfs", func() {
						for i := 0; i < fakeRootFSProvider.DestroyCallCount(); i++ {
							_, arg := fakeRootFSProvider.DestroyArgsForCall(i)
							Expect(arg).ToNot(Equal(layercake.ContainerID("container-2")))

							containerID := fakeIPTablesManager.ContainerTeardownArgsForCall(i)
							Expect(containerID).ToNot(Equal("container-2"))
							Expect(containerID).To(Equal(arg))
						}
					})
				})
			})

			Context("when iptables manager fails", func() {
				disaster := errors.New("oh no!")

				BeforeEach(func() {
					fakeIPTablesManager.ContainerTeardownReturns(disaster)
				})

				It("ignores the error", func() {
					err := pool.Prune(map[string]bool{"container-2": true})
					Expect(err).ToNot(HaveOccurred())

					Expect(fakeRunner).ToNot(HaveExecutedSerially(
						fake_command_runner.CommandSpec{
							Path: "/root/path/destroy.sh",
							Args: []string{path.Join(depotPath, "container-2")},
						},
					))
				})
			})

			Context("when cleaning up the rootfs fails", func() {
				disaster := errors.New("oh no!")

				BeforeEach(func() {
					fakeRootFSProvider.DestroyReturns(disaster)
				})

				It("ignores the error", func() {
					err := pool.Prune(map[string]bool{})
					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context("when a container to keep is specified", func() {
				It("is not destroyed", func() {
					err := pool.Prune(map[string]bool{"container-2": true})
					Expect(err).ToNot(HaveOccurred())

					Expect(fakeRunner).ToNot(HaveExecutedSerially(
						fake_command_runner.CommandSpec{
							Path: "/root/path/destroy.sh",
							Args: []string{path.Join(depotPath, "container-2")},
						},
					))

					Expect(fakeIPTablesManager.ContainerTeardownCallCount()).To(Equal(2))

					containerID := fakeIPTablesManager.ContainerTeardownArgsForCall(0)
					Expect(containerID).ToNot(Equal("container-2"))

					containerID = fakeIPTablesManager.ContainerTeardownArgsForCall(1)
					Expect(containerID).ToNot(Equal("container-2"))
				})

				It("is not cleaned up", func() {
					err := pool.Prune(map[string]bool{"container-2": true})
					Expect(err).ToNot(HaveOccurred())

					Expect(fakeRootFSProvider.DestroyCallCount()).To(Equal(1))
					_, prunedId := fakeRootFSProvider.DestroyArgsForCall(0)
					Expect(prunedId).ToNot(Equal(layercake.ContainerID("container-2")))

					Expect(fakeIPTablesManager.ContainerTeardownCallCount()).To(Equal(2))
				})

				It("does not release the bridge", func() {
					err := pool.Prune(map[string]bool{"container-2": true})
					Expect(err).ToNot(HaveOccurred())

					Expect(fakeBridges.ReleaseCallCount()).To(Equal(1))
					Expect(fakeIPTablesManager.ContainerTeardownCallCount()).To(Equal(2))
				})
			})

			Context("when executing destroy.sh fails", func() {
				disaster := errors.New("oh no!")

				BeforeEach(func() {
					fakeRunner.WhenRunning(
						fake_command_runner.CommandSpec{
							Path: "/root/path/destroy.sh",
						}, func(cmd *exec.Cmd) error {
							return disaster
						},
					)
				})

				It("ignores the error", func() {
					err := pool.Prune(map[string]bool{})
					Expect(err).ToNot(HaveOccurred())

					By("and does not clean up the container's rootfs")
					Expect(fakeRootFSProvider.DestroyCallCount()).To(Equal(0))
				})
			})

			It("prunes any remaining bridges", func() {
				err := pool.Prune(map[string]bool{})
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeBridges.PruneCallCount()).To(Equal(1))
			})
		})
	})

	Describe("destroying", func() {
		var container linux_backend.LinuxContainerSpec
		var err error

		BeforeEach(func() {
			container = linux_backend.LinuxContainerSpec{
				Resources: &linux_backend.Resources{
					Network: &linux_backend.Network{
						IP: net.ParseIP("1.2.3.4"),
					},
					Ports: []uint32{123, 456},
				},
			}
		})

		It("executes destroy.sh with the correct args and environment", func() {
			err = pool.Release(container)
			Expect(err).ToNot(HaveOccurred())

			Expect(fakeRunner).To(HaveExecutedSerially(
				fake_command_runner.CommandSpec{
					Path: "/root/path/destroy.sh",
					Args: []string{path.Join(depotPath, container.ID)},
				},
			))

			Expect(fakeIPTablesManager.ContainerTeardownCallCount()).To(Equal(1))
			containerID := fakeIPTablesManager.ContainerTeardownArgsForCall(0)
			Expect(containerID).To(Equal(container.ID))
		})

		It("releases the container's ports and network", func() {
			err := pool.Release(container)
			Expect(err).ToNot(HaveOccurred())

			Expect(fakePortPool.Released).To(ContainElement(uint32(123)))
			Expect(fakePortPool.Released).To(ContainElement(uint32(456)))

			Expect(fakeSubnetPool.ReleaseCallCount()).To(Equal(1))
			actualNetwork, _ := fakeSubnetPool.ReleaseArgsForCall(0)
			Expect(actualNetwork).To(Equal(container.Resources.Network))

			Expect(fakeIPTablesManager.ContainerTeardownCallCount()).To(Equal(1))
			Expect(fakeIPTablesManager.ContainerTeardownArgsForCall(0)).To(Equal(container.ID))
		})

		Context("when a bridge was created", func() {
			BeforeEach(func() {
				Expect(ioutil.WriteFile(path.Join(depotPath, container.ID, "bridge-name"), []byte("the-bridge"), 0700)).To(Succeed())
			})

			It("releases the bridge from the pool", func() {
				err := pool.Release(container)
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeBridges.ReleaseCallCount()).To(Equal(1))
				bridgeName, containerId := fakeBridges.ReleaseArgsForCall(0)

				Expect(bridgeName).To(Equal("the-bridge"))
				Expect(containerId).To(Equal(container.ID))

				Expect(fakeIPTablesManager.ContainerTeardownCallCount()).To(Equal(1))
				Expect(fakeIPTablesManager.ContainerTeardownArgsForCall(0)).To(Equal(container.ID))
			})

			Context("when the releasing the bridge fails", func() {
				It("returns the error", func() {
					releaseErr := errors.New("jam in the bridge")
					fakeBridges.ReleaseReturns(releaseErr)
					err := pool.Release(container)
					Expect(err).To(MatchError("containerpool: release bridge the-bridge: jam in the bridge"))
				})
			})
		})

		It("tears down filter chains", func() {
			err := pool.Release(container)
			Expect(err).ToNot(HaveOccurred())

			Expect(fakeFilterProvider.ProvideFilterCallCount()).To(BeNumerically(">", 0))
			Expect(fakeFilterProvider.ProvideFilterArgsForCall(0)).To(Equal(container.Handle))
			Expect(fakeFilter.TearDownCallCount()).To(Equal(1))

			Expect(fakeIPTablesManager.ContainerTeardownCallCount()).To(Equal(1))
			Expect(fakeIPTablesManager.ContainerTeardownArgsForCall(0)).To(Equal(container.ID))
		})

		Context("when the container has a rootfs provider defined", func() {
			BeforeEach(func() {
				err := os.MkdirAll(path.Join(depotPath, container.ID), 0755)
				Expect(err).ToNot(HaveOccurred())

				err = ioutil.WriteFile(path.Join(depotPath, container.ID, "rootfs-provider"), []byte("docker-remote-vfs"), 0644)
				Expect(err).ToNot(HaveOccurred())
			})

			It("cleans up the container's rootfs", func() {
				err := pool.Release(container)
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeRootFSProvider.DestroyCallCount()).To(Equal(1))
				_, id := fakeRootFSProvider.DestroyArgsForCall(0)
				Expect(id).To(Equal(container.ID))
			})

			It("clean ups the iptables", func() {
				err := pool.Release(container)
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeIPTablesManager.ContainerTeardownCallCount()).To(Equal(1))
				Expect(fakeIPTablesManager.ContainerTeardownArgsForCall(0)).To(Equal((container.ID)))
			})

			Context("when cleaning up the container's rootfs fails", func() {
				disaster := errors.New("oh no!")

				BeforeEach(func() {
					fakeRootFSProvider.DestroyReturns(disaster)
				})

				It("returns the error", func() {
					err := pool.Release(container)
					Expect(err).To(Equal(disaster))
				})

				It("does not release the container's ports", func() {
					pool.Release(container)

					Expect(fakePortPool.Released).ToNot(ContainElement(uint32(123)))
					Expect(fakePortPool.Released).ToNot(ContainElement(uint32(456)))
				})

				It("does not release the network", func() {
					pool.Release(container)

					Expect(fakeSubnetPool.ReleaseCallCount()).To(Equal(0))
				})

				It("does not tear down the filter", func() {
					pool.Release(container)
					Expect(fakeFilter.TearDownCallCount()).To(Equal(0))
				})
			})
		})

		Context("when iptables manager fails", func() {
			disaster := errors.New("oh no!")

			BeforeEach(func() {
				fakeIPTablesManager.ContainerTeardownReturns(disaster)
			})

			It("returns the error", func() {
				err := pool.Release(container)
				Expect(err).To(Equal(disaster))

				Expect(fakeRunner).ToNot(HaveExecutedSerially(
					fake_command_runner.CommandSpec{
						Path: "/root/path/destroy.sh",
						Args: []string{path.Join(depotPath, container.ID)},
					},
				))
			})
		})

		Context("when destroy.sh fails", func() {
			disaster := errors.New("oh no!")

			BeforeEach(func() {
				fakeRunner.WhenRunning(
					fake_command_runner.CommandSpec{
						Path: "/root/path/destroy.sh",
						Args: []string{path.Join(depotPath, container.ID)},
					},
					func(*exec.Cmd) error {
						return disaster
					},
				)
			})

			It("returns the error", func() {
				err := pool.Release(container)
				Expect(err).To(Equal(disaster))
			})

			It("does not clean up the container's rootfs", func() {
				err := pool.Release(container)
				Expect(err).To(HaveOccurred())

				Expect(fakeRootFSProvider.DestroyCallCount()).To(Equal(0))
			})

			It("does not release the container's ports", func() {
				err := pool.Release(container)
				Expect(err).To(HaveOccurred())

				Expect(fakePortPool.Released).To(BeEmpty())
				Expect(fakePortPool.Released).To(BeEmpty())
			})

			It("does not release the network", func() {
				err := pool.Release(container)
				Expect(err).To(HaveOccurred())
				Expect(fakeSubnetPool.ReleaseCallCount()).To(Equal(0))
			})

			It("does not tear down the filter", func() {
				pool.Release(container)
				Expect(fakeFilter.TearDownCallCount()).To(Equal(0))
			})
		})
	})

	Describe("Logging", func() {
		Context("when acquiring", func() {
			It("should log before and after bridge setup", func() {
				_, err := pool.Acquire(garden.ContainerSpec{})
				Expect(err).ToNot(HaveOccurred())

				Expect(logger.LogMessages()).To(ContainElement(ContainSubstring("setup-bridge-starting")))
				Expect(logger.LogMessages()).To(ContainElement(ContainSubstring("setup-bridge-ended")))
			})

			It("should log before and after iptables setup", func() {
				_, err := pool.Acquire(garden.ContainerSpec{})
				Expect(err).ToNot(HaveOccurred())

				Expect(logger.LogMessages()).To(ContainElement(ContainSubstring("setup-iptables-starting")))
				Expect(logger.LogMessages()).To(ContainElement(ContainSubstring("setup-iptables-ended")))
			})

			It("should log before and after RootFS provision", func() {
				_, err := pool.Acquire(garden.ContainerSpec{})
				Expect(err).ToNot(HaveOccurred())

				Expect(logger.LogMessages()).To(ContainElement(ContainSubstring("provide-rootfs-starting")))
				Expect(logger.LogMessages()).To(ContainElement(ContainSubstring("provide-rootfs-ended")))
			})
		})
	})
})
