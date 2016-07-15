package bandwidth_manager

import (
	"bytes"
	"fmt"
	"os/exec"
	"path"
	"regexp"
	"strconv"

	"code.cloudfoundry.org/garden"
	"code.cloudfoundry.org/garden-linux/logging"
	"github.com/cloudfoundry/gunk/command_runner"
	"code.cloudfoundry.org/lager"
)

var IN_RATE_PATTERN = regexp.MustCompile(`qdisc tbf [0-9a-f]+: root refcnt \d+ rate (\d+)([KMG]?)bit burst (\d+)([KMG]?)b`)
var OUT_RATE_PATTERN = regexp.MustCompile(`police 0x[0-9a-f]+ rate (\d+)([KMG]?)bit burst (\d+)([KMG]?)b`)

type ContainerBandwidthManager struct {
	containerPath string
	containerID   string

	runner command_runner.CommandRunner
}

func New(containerPath, containerID string, runner command_runner.CommandRunner) *ContainerBandwidthManager {
	return &ContainerBandwidthManager{
		containerPath: containerPath,
		containerID:   containerID,

		runner: runner,
	}
}

func (m *ContainerBandwidthManager) SetLimits(
	logger lager.Logger,
	limits garden.BandwidthLimits,
) error {
	runner := logging.Runner{
		CommandRunner: m.runner,
		Logger:        logger,
	}

	setRate := exec.Command(path.Join(m.containerPath, "net_rate.sh"))
	setRate.Env = []string{
		fmt.Sprintf("BURST=%d", limits.BurstRateInBytesPerSecond),
		fmt.Sprintf("RATE=%d", limits.RateInBytesPerSecond*8),
	}

	return runner.Run(setRate)
}

func (m *ContainerBandwidthManager) GetLimits(logger lager.Logger) (garden.ContainerBandwidthStat, error) {
	limits := garden.ContainerBandwidthStat{}

	runner := logging.Runner{
		CommandRunner: m.runner,
		Logger:        logger,
	}

	egressOut := new(bytes.Buffer)

	egress := exec.Command(path.Join(m.containerPath, "net.sh"), "get_egress_info")
	egress.Env = []string{"ID=" + m.containerID}
	egress.Stdout = egressOut

	err := runner.Run(egress)
	if err != nil {
		return limits, err
	}

	matches := IN_RATE_PATTERN.FindStringSubmatch(string(egressOut.Bytes()))
	if matches != nil {
		inRate, err := strconv.ParseUint(matches[1], 10, 0)
		if err != nil {
			return limits, err
		}

		inBurst, err := strconv.ParseUint(matches[3], 10, 0)
		if err != nil {
			return limits, err
		}

		inRateUnit := matches[2]
		inBurstUnit := matches[4]

		limits.InRate = convertUnits(inRate, inRateUnit) / 8
		limits.InBurst = convertUnits(inBurst, inBurstUnit)
	}

	ingressOut := new(bytes.Buffer)

	ingress := exec.Command(path.Join(m.containerPath, "net.sh"), "get_ingress_info")
	ingress.Env = []string{"ID=" + m.containerID}
	ingress.Stdout = ingressOut

	err = runner.Run(ingress)
	if err != nil {
		return limits, err
	}

	matches = OUT_RATE_PATTERN.FindStringSubmatch(string(ingressOut.Bytes()))
	if matches != nil {
		outRate, err := strconv.ParseUint(matches[1], 10, 0)
		if err != nil {
			return limits, err
		}

		outBurst, err := strconv.ParseUint(matches[3], 10, 0)
		if err != nil {
			return limits, err
		}

		outRateUnit := matches[2]
		outBurstUnit := matches[4]

		limits.OutRate = convertUnits(outRate, outRateUnit) / 8
		limits.OutBurst = convertUnits(outBurst, outBurstUnit)
	}

	return limits, err
}

func convertUnits(num uint64, unit string) uint64 {
	switch unit {
	case "K":
		return num * 1024
	case "M":
		return num * (1024 ^ 2)
	case "G":
		return num * (1024 ^ 3)
	default:
		return num
	}
}
