package main

import (
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"

	"code.cloudfoundry.org/garden-linux/container_daemon"
	"code.cloudfoundry.org/garden-linux/container_daemon/unix_socket"
	"code.cloudfoundry.org/garden-linux/containerizer"
	"code.cloudfoundry.org/garden-linux/containerizer/system"
	"code.cloudfoundry.org/garden-linux/sysinfo"
	"github.com/cloudfoundry/gunk/command_runner/linux_command_runner"
)

func init() {
	runtime.LockOSThread()
}

func main() {
	libPath := flag.String("lib", "./lib", "Directory containing hooks")
	rootFsPath := flag.String("root", "", "Directory that will become root in the new mount namespace")
	runPath := flag.String("run", "./run", "Directory where server socket is placed")
	userNsFlag := flag.String("userns", "enabled", "If specified, use user namespacing")
	title := flag.String("title", "", "")
	flag.Parse()

	if *rootFsPath == "" {
		missing("--root")
	}

	binPath, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		fmt.Fprintf(os.Stderr, "wshd: obtain absolute path: %s", err)
		os.Exit(6)
	}

	socketPath := path.Join(*runPath, "wshd.sock")

	privileged := false
	if *userNsFlag == "" || *userNsFlag == "disabled" {
		privileged = true
	}

	containerReader, hostWriter, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "wshd: create pipe: %s", err)
		os.Exit(5)
	}

	hostReader, containerWriter, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "wshd: create pipe: %s", err)
		os.Exit(4)
	}

	sync := &containerizer.PipeSynchronizer{
		Reader: hostReader,
		Writer: hostWriter,
	}

	listener, err := unix_socket.NewListenerFromPath(socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wshd: create listener: %s", err)
		os.Exit(8)
	}

	socketFile, err := listener.File()
	if err != nil {
		fmt.Fprintf(os.Stderr, "wshd: obtain listener file: %s", err)
		os.Exit(9)
	}

	beforeCloneInitializer := &system.Initializer{Steps: []system.StepRunner{
		&containerizer.FuncStep{
			(&container_daemon.RlimitsManager{}).Init,
		},
	}}

	maxUID := sysinfo.Min(sysinfo.MustGetMaxValidUID(), sysinfo.MustGetMaxValidGID())
	cz := containerizer.Containerizer{
		BeforeCloneInitializer: beforeCloneInitializer,
		InitBinPath:            path.Join(binPath, "initc"),
		InitArgs: []string{
			"--root", *rootFsPath,
			"--config", path.Join(*libPath, "../etc/config"),
			"--title", *title,
		},
		Execer: &system.NamespacingExecer{
			CommandRunner: linux_command_runner.New(),
			ExtraFiles:    []*os.File{containerReader, containerWriter, socketFile},
			Privileged:    privileged,
			MaxUID:        maxUID,
		},
		Signaller: sync,
		Waiter:    sync,
		// Temporary until we merge the hook scripts functionality in Golang
		CommandRunner: linux_command_runner.New(),
		LibPath:       *libPath,
		RootfsPath:    *rootFsPath,
	}

	err = cz.Create()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create container: %s", err)
		os.Exit(2)
	}
}

func missing(flagName string) {
	fmt.Fprintf(os.Stderr, "%s is required\n", flagName)
	flag.Usage()
	os.Exit(1)
}
