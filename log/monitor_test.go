// Copyright 2017 bs authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package log

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/check.v1"
	"gopkg.in/mcuadros/go-syslog.v2/format"
)

const (
	logEntries = `
{"log":"msg1\n","stream":"stderr","time":"2017-03-21T21:28:22.0Z"}
{"log":"msg2\n","stream":"stdout","time":"2017-03-21T21:28:32.0Z"}
{"log":"msg3\n","stream":"stderr","time":"2017-03-21T21:28:42.0Z"}
`
	singleEntry = `
{"log":"msg-single\n","stream":"stderr","time":"2017-03-21T21:28:52.0Z"}
`
)

type testHandler struct {
	parts chan format.LogParts
}

func (h *testHandler) Handle(logParts format.LogParts, _ int64, err error) {
	h.parts <- logParts
}

func partsTimeout(c *check.C, ch chan format.LogParts) format.LogParts {
	select {
	case data := <-ch:
		return data
	case <-time.After(5 * time.Second):
		c.Fatal("timeout waiting for channel")
	}
	return nil
}

func stopWaitTimeout(c *check.C, m *fileMonitor) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.stop()
		m.wait()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		c.Fatal("timeout waiting for stop wait")
	}
}

func withTempFile(c *check.C) string {
	f, err := ioutil.TempFile("", "bs-file-monitor")
	c.Assert(err, check.IsNil)
	_, err = f.Write([]byte(logEntries))
	c.Assert(err, check.IsNil)
	err = f.Close()
	c.Assert(err, check.IsNil)
	return f.Name()
}

func (s *S) TestFileMonitorRun(c *check.C) {
	fName := withTempFile(c)
	defer os.Remove(fName)
	th := &testHandler{parts: make(chan format.LogParts)}
	m, err := newFileMonitor(th, fName, "cont1")
	c.Assert(err, check.IsNil)
	err = m.start()
	c.Assert(err, check.IsNil)
	m.run()
	defer stopWaitTimeout(c, m)
	ts0, _ := time.Parse(time.RFC3339, "2017-03-21T21:28:22Z")
	expectedMessages := []rawLogParts{
		{content: []byte("msg1"), ts: ts0, container: []byte("cont1"), priority: []byte("27")},
		{content: []byte("msg2"), ts: ts0.Add(10 * time.Second), container: []byte("cont1"), priority: []byte("30")},
		{content: []byte("msg3"), ts: ts0.Add(20 * time.Second), container: []byte("cont1"), priority: []byte("27")},
	}
	for _, expected := range expectedMessages {
		parts := partsTimeout(c, th.parts)
		c.Check(parts["parts"], check.DeepEquals, &expected)
	}
	c.Assert(m.alive(), check.Equals, true)
}

func (s *S) TestFileMonitorRunOnTruncate(c *check.C) {
	fName := withTempFile(c)
	defer os.Remove(fName)
	th := &testHandler{parts: make(chan format.LogParts, 100)}
	m, err := newFileMonitor(th, fName, "cont1")
	c.Assert(err, check.IsNil)
	err = m.start()
	c.Assert(err, check.IsNil)
	m.run()
	defer stopWaitTimeout(c, m)
	ts0, _ := time.Parse(time.RFC3339, "2017-03-21T21:28:22Z")
	expectedMessages := []rawLogParts{
		{content: []byte("msg1"), ts: ts0, container: []byte("cont1"), priority: []byte("27")},
		{content: []byte("msg2"), ts: ts0.Add(10 * time.Second), container: []byte("cont1"), priority: []byte("30")},
		{content: []byte("msg3"), ts: ts0.Add(20 * time.Second), container: []byte("cont1"), priority: []byte("27")},
	}
	for _, expected := range expectedMessages {
		parts := partsTimeout(c, th.parts)
		c.Check(parts["parts"], check.DeepEquals, &expected)
	}
	for i := 0; i < 100; i++ {
		err = ioutil.WriteFile(fName, []byte(singleEntry), 0600)
		c.Assert(err, check.IsNil)
	}
	expectedMessages = []rawLogParts{
		{content: []byte("msg-single"), ts: ts0.Add(30 * time.Second), container: []byte("cont1"), priority: []byte("27")},
	}
	for _, expected := range expectedMessages {
		parts := partsTimeout(c, th.parts)
		c.Check(parts["parts"], check.DeepEquals, &expected)
	}
	c.Assert(m.alive(), check.Equals, true)
}

func (s *S) TestFileMonitorRunRestartShouldNotRepeatLines(c *check.C) {
	updatePosInterval = 10 * time.Millisecond
	defer func() { updatePosInterval = 5 * time.Second }()
	fName := withTempFile(c)
	defer os.Remove(fName)
	th := &testHandler{parts: make(chan format.LogParts, 10)}
	m, err := newFileMonitor(th, fName, "cont1")
	c.Assert(err, check.IsNil)
	m.posFile = fName + ".pos"
	err = m.start()
	c.Assert(err, check.IsNil)
	m.run()
	defer func() {
		stopWaitTimeout(c, m)
	}()
	ts0, _ := time.Parse(time.RFC3339, "2017-03-21T21:28:22Z")
	expectedMessages := []rawLogParts{
		{content: []byte("msg1"), ts: ts0, container: []byte("cont1"), priority: []byte("27")},
		{content: []byte("msg2"), ts: ts0.Add(10 * time.Second), container: []byte("cont1"), priority: []byte("30")},
		{content: []byte("msg3"), ts: ts0.Add(20 * time.Second), container: []byte("cont1"), priority: []byte("27")},
	}
	for _, expected := range expectedMessages {
		parts := partsTimeout(c, th.parts)
		c.Check(parts["parts"], check.DeepEquals, &expected)
	}
	for {
		_, err := os.Stat(m.posFile)
		if err == nil {
			break
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-time.After(5 * time.Second):
			c.Fatal("timeout waiting for pos file")
		}
	}
	stopWaitTimeout(c, m)
	f, err := os.OpenFile(fName, os.O_WRONLY|os.O_APPEND, 0600)
	c.Assert(err, check.IsNil)
	defer f.Close()
	_, err = f.Write([]byte(singleEntry))
	c.Assert(err, check.IsNil)
	m, err = newFileMonitor(th, fName, "cont1")
	c.Assert(err, check.IsNil)
	m.posFile = fName + ".pos"
	err = m.start()
	c.Assert(err, check.IsNil)
	m.run()
	parts := partsTimeout(c, th.parts)
	c.Check(parts["parts"], check.DeepEquals, &rawLogParts{
		content:   []byte("msg-single"),
		ts:        ts0.Add(30 * time.Second),
		container: []byte("cont1"),
		priority:  []byte("27"),
	})
}

func (s *S) TestFileMonitorAlive(c *check.C) {
	fName := withTempFile(c)
	defer os.Remove(fName)
	th := &testHandler{parts: make(chan format.LogParts, 10)}
	m, err := newFileMonitor(th, fName, "cont1")
	c.Assert(err, check.IsNil)
	err = m.start()
	c.Assert(err, check.IsNil)
	m.run()
	defer stopWaitTimeout(c, m)
	m.cmd.Process.Kill()
	for {
		if !m.alive() {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-time.After(5 * time.Second):
			c.Fatal("timeout waiting for not alive")
		}
	}
	c.Assert(m.alive(), check.Equals, false)
}

func (s *S) TestLogEntryFromName(c *check.C) {
	tests := []struct {
		in  string
		out logFileEntry
	}{
		{
			in: "kube-addon-manager-minikube_kube-system_POD-cb1e80138062646f91f08090e6d5e872e83e32d227ff137e621109ac58b515f6.log",
			out: logFileEntry{
				podName:       "kube-addon-manager-minikube",
				namespace:     "kube-system",
				containerName: "POD",
				containerID:   "cb1e80138062646f91f08090e6d5e872e83e32d227ff137e621109ac58b515f6",
			},
		},
		{
			in: "kube-addon-manager-minikube_kube-system_kube-addon-manager-009fe350fb558575aa8c396f9aed216978e2c46aa9d9601d85df4c0c44eff251.log",
			out: logFileEntry{
				podName:       "kube-addon-manager-minikube",
				namespace:     "kube-system",
				containerName: "kube-addon-manager",
				containerID:   "009fe350fb558575aa8c396f9aed216978e2c46aa9d9601d85df4c0c44eff251",
			},
		},
		{
			in: "myapp-web-2453793373-cbk0k_default_POD-b166a7daa5511a7dc39a861785b00a2799bbab0b45079b0f4de78bbc537d4717.log",
			out: logFileEntry{
				podName:       "myapp-web-2453793373-cbk0k",
				namespace:     "default",
				containerName: "POD",
				containerID:   "b166a7daa5511a7dc39a861785b00a2799bbab0b45079b0f4de78bbc537d4717",
			},
		},
		{
			in: "myapp-web-2453793373-cbk0k_default_myapp-web-e50ac4567691092729a360a3a8fdc9741e81030dd3f8e90633c71cba88e32f6b.log",
			out: logFileEntry{
				podName:       "myapp-web-2453793373-cbk0k",
				namespace:     "default",
				containerName: "myapp-web",
				containerID:   "e50ac4567691092729a360a3a8fdc9741e81030dd3f8e90633c71cba88e32f6b",
			},
		},
		{
			in: "node-container-big-sibling-pool-pool3-7fz9h_default_POD-a5211d198958eb5c06cb2505a21edf50b7527fe7646b955ea9b14db65e387e2e.log",
			out: logFileEntry{
				podName:       "node-container-big-sibling-pool-pool3-7fz9h",
				namespace:     "default",
				containerName: "POD",
				containerID:   "a5211d198958eb5c06cb2505a21edf50b7527fe7646b955ea9b14db65e387e2e",
			},
		},
		{
			in: "node-container-big-sibling-pool-pool3-7fz9h_default_big-sibling-c040b6047b48cb5eacf2977bca9a40074e77f90bb6133d069cba71d44349c263.log",
			out: logFileEntry{
				podName:       "node-container-big-sibling-pool-pool3-7fz9h",
				namespace:     "default",
				containerName: "big-sibling",
				containerID:   "c040b6047b48cb5eacf2977bca9a40074e77f90bb6133d069cba71d44349c263",
			},
		},
	}
	for i, tt := range tests {
		c.Assert(logEntryFromName(tt.in), check.DeepEquals, tt.out, check.Commentf("test %d", i))
	}
}

func (s *S) TestKubernetesLogStreamerWatch(c *check.C) {
	dirName, err := ioutil.TempDir("", "bs-kube-log")
	c.Assert(err, check.IsNil)
	defer os.RemoveAll(dirName)
	th := &testHandler{parts: make(chan format.LogParts)}
	streamer, err := newKubeLogStreamer(th, dirName, dirName)
	c.Assert(err, check.IsNil)
	go streamer.watch()
	defer streamer.stop()
	name := filepath.Join(dirName, "myapp-web-2453793373-cbk0k_default_myapp-web-e50ac4567691092729a360a3a8fdc9741e81030dd3f8e90633c71cba88e32f6b.log")
	err = ioutil.WriteFile(name, []byte(singleEntry), 0600)
	c.Assert(err, check.IsNil)
	parts := partsTimeout(c, th.parts)
	ts0, _ := time.Parse(time.RFC3339, "2017-03-21T21:28:52Z")
	c.Check(parts["parts"], check.DeepEquals, &rawLogParts{
		content:   []byte("msg-single"),
		ts:        ts0,
		container: []byte("e50ac4567691092729a360a3a8fdc9741e81030dd3f8e90633c71cba88e32f6b"),
		priority:  []byte("27"),
	})
}

func (s *S) TestKubernetesLogStreamerWatchIgnoredFiles(c *check.C) {
	dirName, err := ioutil.TempDir("", "bs-kube-log")
	c.Assert(err, check.IsNil)
	defer os.RemoveAll(dirName)
	th := &testHandler{parts: make(chan format.LogParts)}
	streamer, err := newKubeLogStreamer(th, dirName, dirName)
	c.Assert(err, check.IsNil)
	go streamer.watch()
	defer streamer.stop()
	name := filepath.Join(dirName, "pod1_kube-system_contName1-contID1.log")
	err = ioutil.WriteFile(name, []byte(singleEntry), 0600)
	c.Assert(err, check.IsNil)
	select {
	case <-th.parts:
		c.Fatal("no parts expected")
	case <-time.After(500 * time.Millisecond):
	}
	name = filepath.Join(dirName, "pod2_default_POD-contID2.log")
	err = ioutil.WriteFile(name, []byte(singleEntry), 0600)
	c.Assert(err, check.IsNil)
	select {
	case <-th.parts:
		c.Fatal("no parts expected")
	case <-time.After(500 * time.Millisecond):
	}
	name = filepath.Join(dirName, "pod3_default_contName2-contID3.log")
	err = ioutil.WriteFile(name, []byte(singleEntry), 0600)
	c.Assert(err, check.IsNil)
	parts := partsTimeout(c, th.parts)
	ts0, _ := time.Parse(time.RFC3339, "2017-03-21T21:28:52Z")
	c.Check(parts["parts"], check.DeepEquals, &rawLogParts{
		content:   []byte("msg-single"),
		ts:        ts0,
		container: []byte("contID3"),
		priority:  []byte("27"),
	})
}

func (s *S) TestKubernetesLogStreamerWatchKilledWatcher(c *check.C) {
	dirName, err := ioutil.TempDir("", "bs-kube-log")
	c.Assert(err, check.IsNil)
	defer os.RemoveAll(dirName)
	th := &testHandler{parts: make(chan format.LogParts)}
	streamer, err := newKubeLogStreamer(th, dirName, dirName)
	c.Assert(err, check.IsNil)
	go streamer.watch()
	defer streamer.stop()
	name := filepath.Join(dirName, "myapp-web-2453793373-cbk0k_default_myapp-web-e50ac4567691092729a360a3a8fdc9741e81030dd3f8e90633c71cba88e32f6b.log")
	err = ioutil.WriteFile(name, []byte(singleEntry), 0600)
	c.Assert(err, check.IsNil)
	parts := partsTimeout(c, th.parts)
	ts0, _ := time.Parse(time.RFC3339, "2017-03-21T21:28:52Z")
	c.Check(parts["parts"], check.DeepEquals, &rawLogParts{
		content:   []byte("msg-single"),
		ts:        ts0,
		container: []byte("e50ac4567691092729a360a3a8fdc9741e81030dd3f8e90633c71cba88e32f6b"),
		priority:  []byte("27"),
	})
	streamer.monitors["e50ac4567691092729a360a3a8fdc9741e81030dd3f8e90633c71cba88e32f6b"].stop()
	err = ioutil.WriteFile(name, []byte(singleEntry), 0600)
	c.Assert(err, check.IsNil)
	parts = partsTimeout(c, th.parts)
	c.Check(parts["parts"], check.DeepEquals, &rawLogParts{
		content:   []byte("msg-single"),
		ts:        ts0,
		container: []byte("e50ac4567691092729a360a3a8fdc9741e81030dd3f8e90633c71cba88e32f6b"),
		priority:  []byte("27"),
	})
}

func (s *S) TestKubernetesLogStreamerWatchRemoveOld(c *check.C) {
	dirName, err := ioutil.TempDir("", "bs-kube-log")
	c.Assert(err, check.IsNil)
	defer os.RemoveAll(dirName)
	th := &testHandler{parts: make(chan format.LogParts)}
	streamer, err := newKubeLogStreamer(th, dirName, dirName)
	c.Assert(err, check.IsNil)
	name := filepath.Join(dirName, "myapp-web-2453793373-cbk0k_default_myapp-web-e50ac4567691092729a360a3a8fdc9741e81030dd3f8e90633c71cba88e32f6b.log")
	err = ioutil.WriteFile(name, []byte(singleEntry), 0600)
	c.Assert(err, check.IsNil)
	streamer.watchOnce()
	parts := partsTimeout(c, th.parts)
	ts0, _ := time.Parse(time.RFC3339, "2017-03-21T21:28:52Z")
	c.Check(parts["parts"], check.DeepEquals, &rawLogParts{
		content:   []byte("msg-single"),
		ts:        ts0,
		container: []byte("e50ac4567691092729a360a3a8fdc9741e81030dd3f8e90633c71cba88e32f6b"),
		priority:  []byte("27"),
	})
	err = os.Remove(name)
	c.Assert(err, check.IsNil)
	streamer.watchOnce()
	c.Assert(streamer.monitors, check.HasLen, 0)
}

func (s *S) TestKubernetesLogStreamerDirNotFound(c *check.C) {
	th := &testHandler{parts: make(chan format.LogParts)}
	_, err := newKubeLogStreamer(th, "/some/invalid/path", "")
	c.Assert(err, check.Equals, errNoLogDirectory)
}
