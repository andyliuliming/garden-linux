package linux_container_test

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/blang/semver"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"code.cloudfoundry.org/lager/lagertest"

	"code.cloudfoundry.org/garden"
	"code.cloudfoundry.org/garden-linux/linux_backend"
	"code.cloudfoundry.org/garden-linux/linux_container"
	"code.cloudfoundry.org/garden-linux/linux_container/bandwidth_manager/fake_bandwidth_manager"
	"code.cloudfoundry.org/garden-linux/linux_container/cgroups_manager/fake_cgroups_manager"
	"code.cloudfoundry.org/garden-linux/linux_container/fake_iptables_manager"
	"code.cloudfoundry.org/garden-linux/linux_container/fake_network_statisticser"
	"code.cloudfoundry.org/garden-linux/linux_container/fake_quota_manager"
	"code.cloudfoundry.org/garden-linux/linux_container/fake_watcher"
	networkFakes "code.cloudfoundry.org/garden-linux/network/fakes"
	"code.cloudfoundry.org/garden-linux/port_pool/fake_port_pool"
	"code.cloudfoundry.org/garden-linux/process_tracker"
	"code.cloudfoundry.org/garden-linux/process_tracker/fake_process_tracker"
	wfakes "code.cloudfoundry.org/garden/gardenfakes"
	"github.com/cloudfoundry/gunk/command_runner/fake_command_runner"
)

var _ = Describe("Linux containers", func() {
	var containerResources *linux_backend.Resources
	var container *linux_container.LinuxContainer
	var fakeProcessTracker *fake_process_tracker.FakeProcessTracker
	var logger *lagertest.TestLogger
	var containerDir string
	var containerVersion semver.Version

	BeforeEach(func() {
		fakeProcessTracker = new(fake_process_tracker.FakeProcessTracker)
		containerVersion = semver.Version{Major: 1, Minor: 0, Patch: 0}

		var err error
		containerDir, err = ioutil.TempDir("", "depot")
		Expect(err).ToNot(HaveOccurred())

		_, subnet, _ := net.ParseCIDR("2.3.4.0/30")
		containerResources = linux_backend.NewResources(
			1235,
			&linux_backend.Network{
				IP:     net.ParseIP("1.2.3.4"),
				Subnet: subnet,
			},
			"some-bridge",
			[]uint32{},
			nil,
		)
	})

	JustBeforeEach(func() {
		logger = lagertest.NewTestLogger("linux-container-limits-test")
		container = linux_container.NewLinuxContainer(
			linux_backend.LinuxContainerSpec{
				ID:                  "some-id",
				ContainerPath:       containerDir,
				ContainerRootFSPath: "some-volume-path",
				Resources:           containerResources,
				ContainerSpec: garden.ContainerSpec{
					Handle:    "some-handle",
					GraceTime: time.Second * 1,
					Env:       []string{"env1=env1Value", "env2=env2Value"},
				},
				Version: containerVersion,
			},
			fake_port_pool.New(1000),
			fake_command_runner.New(),
			new(fake_cgroups_manager.FakeCgroupsManager),
			new(fake_quota_manager.FakeQuotaManager),
			fake_bandwidth_manager.New(),
			fakeProcessTracker,
			new(networkFakes.FakeFilter),
			new(fake_iptables_manager.FakeIPTablesManager),
			new(fake_network_statisticser.FakeNetworkStatisticser),
			new(fake_watcher.FakeWatcher),
			logger,
		)
	})

	Describe("Running", func() {
		It("runs the /bin/bash via wsh with the given script as the input, and rlimits in env", func() {
			_, err := container.Run(garden.ProcessSpec{
				User: "alice",
				Path: "/some/script",
				Args: []string{"arg1", "arg2"},
				Limits: garden.ResourceLimits{
					As:         uint64ptr(1),
					Core:       uint64ptr(2),
					Cpu:        uint64ptr(3),
					Data:       uint64ptr(4),
					Fsize:      uint64ptr(5),
					Locks:      uint64ptr(6),
					Memlock:    uint64ptr(7),
					Msgqueue:   uint64ptr(8),
					Nice:       uint64ptr(9),
					Nofile:     uint64ptr(10),
					Nproc:      uint64ptr(11),
					Rss:        uint64ptr(12),
					Rtprio:     uint64ptr(13),
					Sigpending: uint64ptr(14),
					Stack:      uint64ptr(15),
				},
			}, garden.ProcessIO{})

			Expect(err).ToNot(HaveOccurred())

			Expect(fakeProcessTracker.RunCallCount()).To(Equal(1))
			_, ranCmd, _, _, _ := fakeProcessTracker.RunArgsForCall(0)
			Expect(ranCmd.Path).To(Equal(containerDir + "/bin/wsh"))

			Expect(ranCmd.Args).To(Equal([]string{
				containerDir + "/bin/wsh",
				"--socket", containerDir + "/run/wshd.sock",
				"--readSignals",
				"--user", "alice",
				"--env", "env1=env1Value",
				"--env", "env2=env2Value",
				"/some/script",
				"arg1",
				"arg2",
			}))

			Expect(ranCmd.Env).To(Equal([]string{
				"RLIMIT_AS=1",
				"RLIMIT_CORE=2",
				"RLIMIT_CPU=3",
				"RLIMIT_DATA=4",
				"RLIMIT_FSIZE=5",
				"RLIMIT_LOCKS=6",
				"RLIMIT_MEMLOCK=7",
				"RLIMIT_MSGQUEUE=8",
				"RLIMIT_NICE=9",
				"RLIMIT_NOFILE=10",
				"RLIMIT_NPROC=11",
				"RLIMIT_RSS=12",
				"RLIMIT_RTPRIO=13",
				"RLIMIT_SIGPENDING=14",
				"RLIMIT_STACK=15",
			}))
		})

		It("runs wsh", func() {
			_, err := container.Run(garden.ProcessSpec{
				User: "alice",
				Path: "/some/script",
			}, garden.ProcessIO{})
			Expect(err).ToNot(HaveOccurred())

			_, ranCmd, _, _, _ := fakeProcessTracker.RunArgsForCall(0)
			Expect(ranCmd.Args).To(Equal([]string{
				containerDir + "/bin/wsh",
				"--socket", containerDir + "/run/wshd.sock",
				"--readSignals",
				"--user", "alice",
				"--env", "env1=env1Value",
				"--env", "env2=env2Value",
				"/some/script",
			}))
		})

		It("configures the correct process signaller (LinkSignaller)", func() {
			_, err := container.Run(garden.ProcessSpec{
				User: "alice",
				Path: "/some/script",
			}, garden.ProcessIO{})
			Expect(err).ToNot(HaveOccurred())

			_, _, _, _, signaller := fakeProcessTracker.RunArgsForCall(0)
			Expect(signaller).To(BeAssignableToTypeOf(&process_tracker.LinkSignaller{}))
		})

		Context("when the container version is missing (an old container)", func() {
			BeforeEach(func() {
				containerVersion = linux_container.MissingVersion
			})

			It("configures the correct process signaller (NamespacedSignaller)", func() {
				_, err := container.Run(garden.ProcessSpec{
					User: "alice",
					Path: "/some/script",
				}, garden.ProcessIO{})
				Expect(err).ToNot(HaveOccurred())

				_, _, _, _, signaller := fakeProcessTracker.RunArgsForCall(0)
				Expect(signaller).To(BeAssignableToTypeOf(&process_tracker.NamespacedSignaller{}))
			})

			It("adds --pidfile argument in wsh", func() {
				_, err := container.Run(garden.ProcessSpec{
					User: "alice",
					Path: "/some/script",
				}, garden.ProcessIO{})
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeProcessTracker.RunCallCount()).To(Equal(1))
				_, ranCmd, _, _, _ := fakeProcessTracker.RunArgsForCall(0)
				Expect(strings.Join(ranCmd.Args, " ")).To(ContainSubstring(fmt.Sprintf("--pidfile %s/processes/1.pid", containerDir)))
			})
		})

		It("uses unique process IDs for each process", func() {
			_, err := container.Run(garden.ProcessSpec{
				User: "alice",
				Path: "/some/script",
			}, garden.ProcessIO{})
			Expect(err).ToNot(HaveOccurred())

			_, err = container.Run(garden.ProcessSpec{
				User: "alice",
				Path: "/some/script",
			}, garden.ProcessIO{})
			Expect(err).ToNot(HaveOccurred())

			id1, _, _, _, _ := fakeProcessTracker.RunArgsForCall(0)
			id2, _, _, _, _ := fakeProcessTracker.RunArgsForCall(1)

			Expect(id1).ToNot(Equal(id2))
		})

		It("should return an error when an environment variable is malformed", func() {
			_, err := container.Run(garden.ProcessSpec{
				User: "alice",
				Path: "/some/script",
				Env:  []string{"a"},
			}, garden.ProcessIO{})
			Expect(err).To(MatchError(HavePrefix("process: malformed environment")))
		})

		It("runs the script with environment variables", func() {
			_, err := container.Run(garden.ProcessSpec{
				User: "bob",
				Path: "/some/script",
				Env:  []string{"ESCAPED=kurt \"russell\"", "UNESCAPED=isaac\nhayes"},
			}, garden.ProcessIO{})

			Expect(err).ToNot(HaveOccurred())

			_, ranCmd, _, _, _ := fakeProcessTracker.RunArgsForCall(0)
			Expect(ranCmd.Args).To(Equal([]string{
				containerDir + "/bin/wsh",
				"--socket", containerDir + "/run/wshd.sock",
				"--readSignals",
				"--user", "bob",
				"--env", `ESCAPED=kurt "russell"`,
				"--env", "UNESCAPED=isaac\nhayes",
				"--env", "env1=env1Value",
				"--env", "env2=env2Value",
				"/some/script",
			}))
		})

		It("runs the script with the environment variables from the run taking precedence over the container environment variables", func() {
			_, err := container.Run(garden.ProcessSpec{
				User: "alice",
				Path: "/some/script",
				Env: []string{
					"env1=overridden",
				},
			}, garden.ProcessIO{})

			Expect(err).ToNot(HaveOccurred())

			_, ranCmd, _, _, _ := fakeProcessTracker.RunArgsForCall(0)
			Expect(ranCmd.Args).To(Equal([]string{
				containerDir + "/bin/wsh",
				"--socket", containerDir + "/run/wshd.sock",
				"--readSignals",
				"--user", "alice",
				"--env", "env1=overridden",
				"--env", "env2=env2Value",
				"/some/script",
			}))
		})

		It("runs the script with the working dir set if present", func() {
			_, err := container.Run(garden.ProcessSpec{
				User: "alice",
				Path: "/some/script",
				Dir:  "/some/dir",
			}, garden.ProcessIO{})

			Expect(err).ToNot(HaveOccurred())

			_, ranCmd, _, _, _ := fakeProcessTracker.RunArgsForCall(0)
			Expect(ranCmd.Args).To(Equal([]string{
				containerDir + "/bin/wsh",
				"--socket", containerDir + "/run/wshd.sock",
				"--readSignals",
				"--user", "alice",
				"--env", "env1=env1Value",
				"--env", "env2=env2Value",
				"--dir", "/some/dir",
				"/some/script",
			}))
		})

		It("runs the script with a TTY if present", func() {
			ttySpec := &garden.TTYSpec{
				WindowSize: &garden.WindowSize{
					Columns: 123,
					Rows:    456,
				},
			}

			_, err := container.Run(garden.ProcessSpec{
				User: "alice",
				Path: "/some/script",
				TTY:  ttySpec,
			}, garden.ProcessIO{})

			Expect(err).ToNot(HaveOccurred())

			_, _, _, tty, _ := fakeProcessTracker.RunArgsForCall(0)
			Expect(tty).To(Equal(ttySpec))
		})

		Describe("streaming", func() {
			JustBeforeEach(func() {
				fakeProcessTracker.RunStub = func(processID string, cmd *exec.Cmd, io garden.ProcessIO, tty *garden.TTYSpec, signaller process_tracker.Signaller) (garden.Process, error) {
					writing := new(sync.WaitGroup)
					writing.Add(1)

					go func() {
						defer writing.Done()
						defer GinkgoRecover()

						_, err := fmt.Fprintf(io.Stdout, "hi out\n")
						Expect(err).ToNot(HaveOccurred())

						_, err = fmt.Fprintf(io.Stderr, "hi err\n")
						Expect(err).ToNot(HaveOccurred())
					}()

					process := new(wfakes.FakeProcess)

					process.IDReturns(processID)

					process.WaitStub = func() (int, error) {
						writing.Wait()
						return 123, nil
					}

					return process, nil
				}
			})

			It("streams stderr and stdout and exit status", func() {
				stdout := gbytes.NewBuffer()
				stderr := gbytes.NewBuffer()

				process, err := container.Run(garden.ProcessSpec{
					User: "alice",
					Path: "/some/script",
				}, garden.ProcessIO{
					Stdout: stdout,
					Stderr: stderr,
				})
				Expect(err).ToNot(HaveOccurred())

				Expect(process.ID()).To(Equal("1"))

				Eventually(stdout).Should(gbytes.Say("hi out\n"))
				Eventually(stderr).Should(gbytes.Say("hi err\n"))

				Expect(process.Wait()).To(Equal(123))
			})
		})

		It("only sets the given rlimits", func() {
			_, err := container.Run(garden.ProcessSpec{
				User: "alice",
				Path: "/some/script",
				Limits: garden.ResourceLimits{
					As:      uint64ptr(1),
					Cpu:     uint64ptr(3),
					Fsize:   uint64ptr(5),
					Memlock: uint64ptr(7),
					Nice:    uint64ptr(9),
					Nproc:   uint64ptr(11),
					Rtprio:  uint64ptr(13),
					Stack:   uint64ptr(15),
				},
			}, garden.ProcessIO{})

			Expect(err).ToNot(HaveOccurred())

			_, ranCmd, _, _, _ := fakeProcessTracker.RunArgsForCall(0)
			Expect(ranCmd.Path).To(Equal(containerDir + "/bin/wsh"))

			Expect(ranCmd.Args).To(Equal([]string{
				containerDir + "/bin/wsh",
				"--socket", containerDir + "/run/wshd.sock",
				"--readSignals",
				"--user", "alice",
				"--env", "env1=env1Value",
				"--env", "env2=env2Value",
				"/some/script",
			}))

			Expect(ranCmd.Env).To(Equal([]string{
				"RLIMIT_AS=1",
				"RLIMIT_CPU=3",
				"RLIMIT_FSIZE=5",
				"RLIMIT_MEMLOCK=7",
				"RLIMIT_NICE=9",
				"RLIMIT_NPROC=11",
				"RLIMIT_RTPRIO=13",
				"RLIMIT_STACK=15",
			}))
		})

		Context("when the user is not set", func() {
			It("returns an error", func() {
				_, err := container.Run(garden.ProcessSpec{
					Path: "whoami",
					Args: []string{},
				}, garden.ProcessIO{})
				Expect(err).To(MatchError(ContainSubstring("A User for the process to run as must be specified")))
			})
		})

		Context("when spawning fails", func() {
			disaster := errors.New("oh no!")

			JustBeforeEach(func() {
				fakeProcessTracker.RunReturns(nil, disaster)
			})

			It("returns the error", func() {
				_, err := container.Run(garden.ProcessSpec{
					Path: "/some/script",
					User: "root",
				}, garden.ProcessIO{})
				Expect(err).To(Equal(disaster))
			})
		})
	})

	Describe("Attaching", func() {
		Context("to a started process", func() {
			JustBeforeEach(func() {
				fakeProcessTracker.AttachStub = func(id string, io garden.ProcessIO) (garden.Process, error) {
					writing := new(sync.WaitGroup)
					writing.Add(1)

					go func() {
						defer writing.Done()
						defer GinkgoRecover()

						_, err := fmt.Fprintf(io.Stdout, "hi out\n")
						Expect(err).ToNot(HaveOccurred())

						_, err = fmt.Fprintf(io.Stderr, "hi err\n")
						Expect(err).ToNot(HaveOccurred())
					}()

					process := new(wfakes.FakeProcess)

					process.IDReturns("42")

					process.WaitStub = func() (int, error) {
						writing.Wait()
						return 123, nil
					}

					return process, nil
				}
			})

			It("streams stderr and stdout and exit status", func() {
				stdout := gbytes.NewBuffer()
				stderr := gbytes.NewBuffer()

				process, err := container.Attach("1", garden.ProcessIO{
					Stdout: stdout,
					Stderr: stderr,
				})
				Expect(err).ToNot(HaveOccurred())

				pid, _ := fakeProcessTracker.AttachArgsForCall(0)
				Expect(pid).To(Equal("1"))

				Expect(process.ID()).To(Equal("42"))

				Eventually(stdout).Should(gbytes.Say("hi out\n"))
				Eventually(stderr).Should(gbytes.Say("hi err\n"))

				Expect(process.Wait()).To(Equal(123))
			})
		})

		Context("when attaching fails", func() {
			disaster := errors.New("oh no!")

			JustBeforeEach(func() {
				fakeProcessTracker.AttachReturns(nil, disaster)
			})

			It("returns the error", func() {
				_, err := container.Attach("42", garden.ProcessIO{})
				Expect(err).To(Equal(disaster))
			})
		})
	})

})

func uint64ptr(n uint64) *uint64 {
	return &n
}
