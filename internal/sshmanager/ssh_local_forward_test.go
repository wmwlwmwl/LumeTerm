package sshmanager

import (
	"context"
	"net"
	"testing"
	"time"

	"lumeterm/internal/tcpforward"

	"golang.org/x/crypto/ssh"
)

type fakeManagerForwarder struct {
	addr   net.Addr
	closed bool
}

func (f *fakeManagerForwarder) Addr() net.Addr { return f.addr }
func (f *fakeManagerForwarder) Close() error {
	f.closed = true
	return nil
}

func TestSSHManagerPortForwardLifecycle(t *testing.T) {
	forwarder := &fakeManagerForwarder{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2200}}
	mgr := &SSHManager{portForwards: make(map[string]*managedPortForward)}
	mgr.portForwards["pf-1"] = &managedPortForward{id: "pf-1", kind: "local", forwarder: forwarder}

	infos := mgr.ListPortForwards()
	if len(infos) != 1 || infos[0].ID != "pf-1" || infos[0].Addr != "127.0.0.1:2200" {
		t.Fatalf("unexpected forward info: %#v", infos)
	}
	if err := mgr.StopPortForward("pf-1"); err != nil {
		t.Fatal(err)
	}
	if !forwarder.closed || len(mgr.portForwards) != 1 {
		t.Fatal("stopped forward should close but remain persisted")
	}
	if entry := mgr.portForwards["pf-1"]; entry == nil || entry.enabled || entry.forwarder != nil {
		t.Fatalf("unexpected stopped entry: %#v", entry)
	}
	if err := mgr.DeletePortForward("pf-1"); err != nil {
		t.Fatal(err)
	}
	if len(mgr.portForwards) != 0 {
		t.Fatal("forward record was not deleted")
	}
}

func TestTCPForwardPackageIsUsableByManagerBoundary(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	forwarder, err := tcpforward.StartLocal(context.Background(), &managerDialer{conn: clientConn}, "127.0.0.1:0", "example.com:80")
	if err != nil {
		t.Fatal(err)
	}
	defer forwarder.Close()
	conn, err := net.Dial("tcp", forwarder.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	_ = serverConn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := serverConn.Read(buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ok" {
		t.Fatalf("payload = %q", buf)
	}
}

type managerDialer struct{ conn net.Conn }

func (d *managerDialer) Dial(string, string) (net.Conn, error) { return d.conn, nil }

func TestStartPortForwardDoesNotHoldManagerLock(t *testing.T) {
	client := &ssh.Client{}
	mgr := &SSHManager{
		clients:      map[string]*sshClientEntry{"conn": {Client: client}},
		portForwards: make(map[string]*managedPortForward),
	}
	started := make(chan struct{})
	continueStart := make(chan struct{})
	result := make(chan string, 1)
	go func() {
		id, err := mgr.startPortForward("conn", "local", "127.0.0.1:0", "example.com:22", func(got *ssh.Client) (sshPortForward, error) {
			if got != client {
				t.Errorf("captured client = %p, want %p", got, client)
			}
			close(started)
			<-continueStart
			return &fakeManagerForwarder{}, nil
		})
		if err != nil {
			t.Errorf("startPortForward error: %v", err)
		}
		result <- id
	}()

	<-started
	lockReleased := make(chan struct{})
	go func() {
		mgr.mu.Lock()
		mgr.mu.Unlock()
		close(lockReleased)
	}()
	select {
	case <-lockReleased:
	case <-time.After(time.Second):
		t.Fatal("startPortForward held manager lock while creating forward")
	}
	close(continueStart)
	if id := <-result; id == "" {
		t.Fatal("startPortForward returned empty id")
	}
	if len(mgr.portForwards) != 1 {
		t.Fatalf("port forward count = %d, want 1", len(mgr.portForwards))
	}
}

func TestStartPortForwardDropsForwardAfterDisconnect(t *testing.T) {
	client := &ssh.Client{}
	forwarder := &fakeManagerForwarder{}
	mgr := &SSHManager{
		clients:      map[string]*sshClientEntry{"conn": {Client: client}},
		portForwards: make(map[string]*managedPortForward),
	}
	started := make(chan struct{})
	continueStart := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := mgr.startPortForward("conn", "remote", "127.0.0.1:8080", "127.0.0.1:80", func(*ssh.Client) (sshPortForward, error) {
			close(started)
			<-continueStart
			return forwarder, nil
		})
		result <- err
	}()

	<-started
	mgr.mu.Lock()
	delete(mgr.clients, "conn")
	mgr.mu.Unlock()
	close(continueStart)

	if err := <-result; err == nil {
		t.Fatal("stale forward creation unexpectedly succeeded")
	}
	if !forwarder.closed {
		t.Fatal("stale forwarder was not closed")
	}
	if len(mgr.portForwards) != 0 {
		t.Fatal("stale forward was registered")
	}
}
