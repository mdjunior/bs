// Copyright 2015 bs authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package log

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/fsouza/go-dockerclient"
	dTesting "github.com/fsouza/go-dockerclient/testing"
	"gopkg.in/check.v1"
)

var _ = check.Suite(S{})

func Test(t *testing.T) {
	check.TestingT(t)
}

type S struct{}

func (S) TestLogForwarderStartCachedAppName(c *check.C) {
	addr, err := net.ResolveUDPAddr("udp", "0.0.0.0:0")
	c.Assert(err, check.IsNil)
	udpConn, err := net.ListenUDP("udp", addr)
	c.Assert(err, check.IsNil)
	lf := LogForwarder{
		BindAddress:      "udp://0.0.0.0:59317",
		ForwardAddresses: []string{"udp://" + udpConn.LocalAddr().String()},
		DockerEndpoint:   "",
		AppNameEnvVar:    "",
	}
	err = lf.Start()
	c.Assert(err, check.IsNil)
	defer func() {
		func() {
			defer func() {
				recover()
			}()
			lf.server.Kill()
		}()
		lf.server.Wait()
	}()
	lf.appNameCache.Add("contid1", "myappname")
	conn, err := net.Dial("udp", "127.0.0.1:59317")
	c.Assert(err, check.IsNil)
	defer conn.Close()
	msg := []byte("<30>2015-06-05T16:13:47Z myhost docker/contid1: mymsg\n")
	_, err = conn.Write(msg)
	c.Assert(err, check.IsNil)
	buffer := make([]byte, 1024)
	udpConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := udpConn.Read(buffer)
	c.Assert(err, check.IsNil)
	c.Assert(buffer[:n], check.DeepEquals, []byte("<30>2015-06-05T16:13:47Z contid1 myappname: mymsg\n"))
}

func (S) TestLogForwarderStartDockerAppName(c *check.C) {
	addr, err := net.ResolveUDPAddr("udp", "0.0.0.0:0")
	c.Assert(err, check.IsNil)
	udpConn, err := net.ListenUDP("udp", addr)
	c.Assert(err, check.IsNil)
	dockerServer, err := dTesting.NewServer("127.0.0.1:0", nil, nil)
	c.Assert(err, check.IsNil)
	lf := LogForwarder{
		BindAddress:      "udp://0.0.0.0:59317",
		ForwardAddresses: []string{"udp://" + udpConn.LocalAddr().String()},
		DockerEndpoint:   dockerServer.URL(),
		AppNameEnvVar:    "APPNAMEVAR=",
	}
	err = lf.Start()
	c.Assert(err, check.IsNil)
	defer func() {
		func() {
			defer func() {
				recover()
			}()
			lf.server.Kill()
		}()
		lf.server.Wait()
	}()
	dockerClient, err := docker.NewClient(dockerServer.URL())
	c.Assert(err, check.IsNil)
	err = dockerClient.PullImage(docker.PullImageOptions{Repository: "myimg"}, docker.AuthConfiguration{})
	c.Assert(err, check.IsNil)
	config := docker.Config{
		Image: "myimg",
		Cmd:   []string{"mycmd"},
		Env:   []string{"ENV1=val1", "APPNAMEVAR=coolappname"},
	}
	opts := docker.CreateContainerOptions{Name: "myContName", Config: &config}
	cont, err := dockerClient.CreateContainer(opts)
	c.Assert(err, check.IsNil)
	conn, err := net.Dial("udp", "127.0.0.1:59317")
	c.Assert(err, check.IsNil)
	defer conn.Close()
	msg := []byte(fmt.Sprintf("<30>2015-06-05T16:13:47Z myhost docker/%s: mymsg\n", cont.ID))
	_, err = conn.Write(msg)
	c.Assert(err, check.IsNil)
	buffer := make([]byte, 1024)
	udpConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := udpConn.Read(buffer)
	c.Assert(err, check.IsNil)
	expected := []byte(fmt.Sprintf("<30>2015-06-05T16:13:47Z %s coolappname: mymsg\n", cont.ID))
	c.Assert(buffer[:n], check.DeepEquals, expected)
	cached, ok := lf.appNameCache.Get(cont.ID)
	c.Assert(ok, check.Equals, true)
	c.Assert(cached.(string), check.Equals, "coolappname")
}

func (S) TestLogForwarderStartBindError(c *check.C) {
	lf := LogForwarder{
		BindAddress: "xudp://0.0.0.0:59317",
	}
	err := lf.Start()
	c.Assert(err, check.ErrorMatches, `invalid protocol "xudp", expected tcp or udp`)
}

func (S) TestLogForwarderForwardConnError(c *check.C) {
	lf := LogForwarder{
		BindAddress:      "udp://0.0.0.0:59317",
		ForwardAddresses: []string{"xudp://127.0.0.1:1234"},
	}
	err := lf.Start()
	c.Assert(err, check.ErrorMatches, `unable to connect to "xudp://127.0.0.1:1234": dial xudp: unknown network xudp`)
	lf = LogForwarder{
		BindAddress:      "udp://0.0.0.0:59317",
		ForwardAddresses: []string{"tcp://localhost:99999"},
	}
	err = lf.Start()
	c.Assert(err, check.ErrorMatches, `unable to connect to "tcp://localhost:99999": dial tcp: invalid port 99999`)
}