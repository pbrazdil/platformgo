package nats_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	gonats "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	platformnats "github.com/upcomers-org/platformgo/internal/adapters/nats"
	"github.com/upcomers-org/platformgo/internal/engine"
)

func TestJetStreamReconnectPreservesShardStream(t *testing.T) {
	serverBinary := os.Getenv("PLATFORMGO_TEST_NATS_SERVER_BIN")
	if serverBinary == "" {
		t.Skip("PLATFORMGO_TEST_NATS_SERVER_BIN is required for reconnect test")
	}
	port := availableTCPPort(t)
	storeDirectory := t.TempDir()
	server := startNATSServer(t, serverBinary, storeDirectory, port)
	t.Cleanup(func() {
		stopNATSServer(server)
	})

	url := fmt.Sprintf("nats://127.0.0.1:%d", port)
	connection, err := gonats.Connect(
		url,
		gonats.MaxReconnects(100),
		gonats.ReconnectWait(20*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("connect NATS: %v", err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatalf("create JetStream context: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	limits := platformnats.StreamLimits{
		Replicas:        1,
		MaxMessages:     100,
		MaxBytes:        1 << 20,
		MaxMessageBytes: 1 << 10,
		MaxAge:          time.Hour,
		DuplicateWindow: time.Minute,
	}
	if err := platformnats.EnsureEngineShardStream(ctx, js, 12, limits); err != nil {
		t.Fatalf("EnsureEngineShardStream: %v", err)
	}
	firstID := engine.IDFromSequence(engine.ID{}, 141)
	firstAck, err := js.Publish(
		ctx,
		"engine.input.12.control.v1",
		[]byte(`{"probe":"before-restart"}`),
		jetstream.WithMsgID(firstID.String()),
	)
	if err != nil || firstAck.Sequence != 1 {
		t.Fatalf("first publish = acknowledgment %+v error %v", firstAck, err)
	}

	stopNATSServer(server)
	server = startNATSServer(t, serverBinary, storeDirectory, port)
	waitForNATSStatus(t, connection, gonats.CONNECTED)

	secondID := engine.IDFromSequence(engine.ID{}, 142)
	secondAck, err := js.Publish(
		ctx,
		"engine.input.12.control.v1",
		[]byte(`{"probe":"after-restart"}`),
		jetstream.WithMsgID(secondID.String()),
	)
	if err != nil || secondAck.Sequence != 2 {
		t.Fatalf("second publish = acknowledgment %+v error %v", secondAck, err)
	}
}

func availableTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve TCP port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release TCP port: %v", err)
	}
	return port
}

func startNATSServer(
	t *testing.T,
	binary string,
	storeDirectory string,
	port int,
) *exec.Cmd {
	t.Helper()
	command := exec.Command(
		binary,
		"-js",
		"-sd",
		storeDirectory,
		"-a",
		"127.0.0.1",
		"-p",
		strconv.Itoa(port),
	)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		t.Fatalf("start nats-server: %v", err)
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return command
		}
		time.Sleep(20 * time.Millisecond)
	}
	stopNATSServer(command)
	t.Fatalf("nats-server did not listen on %s", address)
	return nil
}

func stopNATSServer(command *exec.Cmd) {
	if command == nil || command.Process == nil || command.ProcessState != nil {
		return
	}
	_ = command.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		<-done
	}
}

func waitForNATSStatus(
	t *testing.T,
	connection *gonats.Conn,
	status gonats.Status,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if connection.Status() == status {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf(
		"NATS connection status = %s, want %s",
		connection.Status(),
		status,
	)
}
