//go:build windows

package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
	"path/filepath"
)

// pipeName returns the Windows named pipe path for IPC.
func pipeName(home string) string {
	// Named pipes must start with \\.\pipe\
	// Use a hash of home to create a unique pipe name
	return `\\.\pipe\gitgate-daemon`
}

// runIPCListener listens on a Windows named pipe for incoming push events.
func (d *Daemon) runIPCListener() {
	pipe := pipeName(d.home)

	cfg := &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;WD)", // Allow all users to connect
		MessageMode:        false,
		InputBufferSize:    4096,
		OutputBufferSize:   4096,
	}

	ln, err := winio.ListenPipe(pipe, cfg)
	if err != nil {
		d.logger.Error("failed to start IPC listener", "pipe", pipe, "error", err)
		return
	}
	defer ln.Close()

	d.logger.Info("IPC listening on named pipe", "pipe", pipe)

	for {
		select {
		case <-d.ctx.Done():
			return
		default:
		}

		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-d.ctx.Done():
				return
			default:
				d.logger.Warn("IPC accept error", "error", err)
				continue
			}
		}

		go d.handleIPCConn(conn)
	}
}

// handleIPCConn reads a PushEvent from an IPC connection and enqueues it.
func (d *Daemon) handleIPCConn(conn net.Conn) {
	defer conn.Close()

	data, err := io.ReadAll(io.LimitReader(conn, 4096))
	if err != nil {
		d.logger.Warn("failed to read IPC message", "error", err)
		return
	}

	var event PushEvent
	if err := json.Unmarshal(data, &event); err != nil {
		d.logger.Warn("failed to parse IPC message", "error", err)
		return
	}

	if err := d.EnqueuePush(event); err != nil {
		d.logger.Error("failed to enqueue push", "error", err)
	}
}

// sendToSocket sends data to the daemon's named pipe.
func sendToSocket(home string, data []byte) error {
	pipe := pipeName(home)
	timeout := 5 * time.Second

	conn, err := winio.DialPipe(pipe, &timeout)
	if err != nil {
		return fmt.Errorf("daemon not running (cannot connect to %s): %w", pipe, err)
	}
	defer conn.Close()

	_, err = conn.Write(data)
	return err
}

// socketPath is a no-op on Windows (named pipes are used instead).
func socketPath(home string) string {
	return filepath.Join(home, "daemon.pipe")
}
