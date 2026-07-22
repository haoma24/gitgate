//go:build !windows

package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

// socketPath returns the Unix socket path for IPC.
func socketPath(home string) string {
	return filepath.Join(home, "daemon.sock")
}

// runIPCListener listens on a Unix socket for incoming push events.
func (d *Daemon) runIPCListener() {
	sockPath := socketPath(d.home)

	// Remove stale socket
	_ = os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		d.logger.Error("failed to start IPC listener", "error", err)
		return
	}
	defer ln.Close()

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

// sendToSocket sends data to the daemon's Unix socket.
func sendToSocket(home string, data []byte) error {
	sockPath := socketPath(home)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("daemon not running (cannot connect to %s): %w", sockPath, err)
	}
	defer conn.Close()

	_, err = conn.Write(data)
	return err
}
