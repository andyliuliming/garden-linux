package networking_test

import (
	"os/exec"

	"code.cloudfoundry.org/garden"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

var _ = Describe("MTU size", func() {
	var container garden.Container

	BeforeEach(func() {
		client = startGarden("-mtu=6789")

		var err error

		container, err = client.Create(garden.ContainerSpec{})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		err := client.Destroy(container.Handle())
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("container's network interface", func() {
		It("has the correct MTU size", func() {
			stdout := gbytes.NewBuffer()
			stderr := gbytes.NewBuffer()

			process, err := container.Run(garden.ProcessSpec{
				User: "alice",
				Path: "ifconfig",
				Args: []string{containerIfName(container)},
			}, garden.ProcessIO{
				Stdout: stdout,
				Stderr: stderr,
			})
			Expect(err).ToNot(HaveOccurred())
			rc, err := process.Wait()
			Expect(err).ToNot(HaveOccurred())
			Expect(rc).To(Equal(0))

			Expect(stdout.Contents()).To(ContainSubstring(" MTU:6789 "))
		})
	})

	Describe("hosts's network interface for a container", func() {
		It("has the correct MTU size", func() {
			out, err := exec.Command("ifconfig", hostIfName(container)).Output()
			Expect(err).ToNot(HaveOccurred())

			Expect(out).To(ContainSubstring(" MTU:6789 "))
		})
	})

})
