package server_test

import (
	"errors"
	"io/ioutil"
	"net"
	"os"
	"path"
	"time"

	"code.google.com/p/gogoprotobuf/proto"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/vito/garden/backend"
	"github.com/vito/garden/backend/fakebackend"
	"github.com/vito/garden/messagereader"
	protocol "github.com/vito/garden/protocol"
	"github.com/vito/garden/server"
)

var _ = Describe("The Warden server", func() {
	It("listens on the given socket path", func() {
		tmpdir, err := ioutil.TempDir(os.TempDir(), "warden-server-test")
		Expect(err).ToNot(HaveOccured())

		socketPath := path.Join(tmpdir, "warden.sock")

		wardenServer := server.New(socketPath, fakebackend.New())

		err = wardenServer.Start()
		Expect(err).ToNot(HaveOccured())

		Eventually(ErrorDialingUnix(socketPath)).ShouldNot(HaveOccured())
	})

	Context("when starting fails", func() {
		It("returns the error", func() {
			tmpfile, err := ioutil.TempFile(os.TempDir(), "warden-server-test")
			Expect(err).ToNot(HaveOccured())

			wardenServer := server.New(
				// weird scenario: /foo/X/warden.sock with X being a file
				path.Join(tmpfile.Name(), "warden.sock"),
				fakebackend.New(),
			)

			err = wardenServer.Start()
			Expect(err).To(HaveOccured())
		})
	})

	Context("when a client connects", func() {
		var socketPath string
		var serverBackend *fakebackend.FakeBackend

		var serverConnection net.Conn

		BeforeEach(func() {
			tmpdir, err := ioutil.TempDir(os.TempDir(), "warden-server-test")
			Expect(err).ToNot(HaveOccured())

			socketPath = path.Join(tmpdir, "warden.sock")
			serverBackend = fakebackend.New()

			wardenServer := server.New(socketPath, serverBackend)

			err = wardenServer.Start()
			Expect(err).ToNot(HaveOccured())

			Eventually(ErrorDialingUnix(socketPath)).ShouldNot(HaveOccured())

			serverConnection, err = net.Dial("unix", socketPath)
			Expect(err).ToNot(HaveOccured())
		})

		writeMessages := func(message proto.Message) {
			num, err := protocol.Messages(message).WriteTo(serverConnection)
			Expect(err).ToNot(HaveOccured())
			Expect(num).ToNot(Equal(0))
		}

		readResponse := func(response proto.Message) {
			err := messagereader.ReadMessage(serverConnection, response)
			Expect(err).ToNot(HaveOccured())
		}

		Context("and the client sends a PingRequest", func() {
			It("sends a PongResponse", func(done Done) {
				writeMessages(&protocol.PingRequest{})
				readResponse(&protocol.PingResponse{})
				close(done)
			}, 1.0)
		})

		Context("and the client sends a EchoRequest", func() {
			It("sends an EchoResponse with the same message", func(done Done) {
				message := proto.String("Hello, world!")

				writeMessages(&protocol.EchoRequest{Message: message})

				var response protocol.EchoResponse
				readResponse(&response)

				Expect(response.GetMessage()).To(Equal(*message))

				close(done)
			}, 1.0)
		})

		Context("and the client sends a CreateRequest", func() {
			It("sends a CreateResponse with the created handle", func(done Done) {
				writeMessages(&protocol.CreateRequest{
					Handle: proto.String("some-handle"),
				})

				var response protocol.CreateResponse
				readResponse(&response)

				Expect(response.GetHandle()).To(Equal("some-handle"))

				close(done)
			}, 1.0)

			It("creates the container with the spec from the request", func(done Done) {
				var bindMountMode protocol.CreateRequest_BindMount_Mode

				bindMountMode = protocol.CreateRequest_BindMount_RW

				writeMessages(&protocol.CreateRequest{
					Handle:    proto.String("some-handle"),
					GraceTime: proto.Uint32(42),
					Network:   proto.String("some-network"),
					Rootfs:    proto.String("/path/to/rootfs"),
					BindMounts: []*protocol.CreateRequest_BindMount{
						{
							SrcPath: proto.String("/bind/mount/src"),
							DstPath: proto.String("/bind/mount/dst"),
							Mode:    &bindMountMode,
						},
					},
				})

				var response protocol.CreateResponse
				readResponse(&response)

				container, found := serverBackend.CreatedContainers[response.GetHandle()]
				Expect(found).To(BeTrue())

				Expect(container.Spec).To(Equal(backend.ContainerSpec{
					Handle:     "some-handle",
					GraceTime:  time.Duration(42 * time.Second),
					Network:    "some-network",
					RootFSPath: "/path/to/rootfs",
					BindMounts: []backend.BindMount{
						{
							SrcPath: "/bind/mount/src",
							DstPath: "/bind/mount/dst",
							Mode:    backend.BindMountModeRW,
						},
					},
				}))

				close(done)
			}, 1.0)

			Context("when creating the container fails", func() {
				BeforeEach(func() {
					serverBackend.ContainerCreationError = errors.New("oh no!")
				})

				It("sends a WardenError response", func(done Done) {
					writeMessages(&protocol.CreateRequest{
						Handle: proto.String("some-handle"),
					})

					var response protocol.CreateResponse
					err := messagereader.ReadMessage(serverConnection, &response)
					Expect(err).To(Equal(&messagereader.WardenError{Message: "oh no!"}))

					close(done)
				}, 1.0)
			})
		})
	})
})

func ErrorDialingUnix(socketPath string) func() error {
	return func() error {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			conn.Close()
		}

		return err
	}
}
