// +-------------------------------------------------------------------------
// | Copyright (C) 2017 Yunify, Inc.
// +-------------------------------------------------------------------------
// | Licensed under the Apache License, Version 2.0 (the "License");
// | you may not use this work except in compliance with the License.
// | You may obtain a copy of the License in the LICENSE file, or at:
// |
// | http://www.apache.org/licenses/LICENSE-2.0
// |
// | Unless required by applicable law or agreed to in writing, software
// | distributed under the License is distributed on an "AS IS" BASIS,
// | WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// | See the License for the specific language governing permissions and
// | limitations under the License.
// +-------------------------------------------------------------------------

package client

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime/debug"
	"strings"
	"time"

	"github.com/yunify/qsftpd/context"
	"github.com/yunify/qsftpd/transfer"
	"github.com/yunify/qsftpd/utils"
)

// Handler driver handles the file system access logic.
type Handler struct {
	id            string           // id of the client
	conn          net.Conn         // TCP connection
	writer        *bufio.Writer    // Writer on the TCP connection
	reader        *bufio.Reader    // Reader on the TCP connection
	user          string           // Authenticated user
	path          string           // Current path
	command       string           // Command received on the connection
	param         string           // Param of the FTP command
	connectedAt   time.Time        // Date of connection
	ctxRnfr       string           // Rename from
	ctxRest       int64            // Restart point
	transfer      transfer.Handler // Transfer connection
	transferTLS   bool             // Use TLS for transfer connection
	driver        Driver           // Client handling driver
	driverFactory func() Driver    // Factory to create driver
}

// Path provides the current working directory of the client.
func (c *Handler) Path() string {
	return c.path
}

// SetPath changes the current working directory.
func (c *Handler) SetPath(path string) {
	c.path = path
}

// HandleCommands reads the stream of commands.
func (c *Handler) HandleCommands() {
	defer c.end()

	for {
		if c.reader == nil {
			context.Logger.Debugf("Clean disconnect: ftp.disconnect, ID: %s, Clean: %t", c.id, true)
			return
		}

		line, err := c.reader.ReadString('\n')

		if err != nil {
			if err == io.EOF {
				context.Logger.Debugf("TCP disconnect: ftp.disconnect, ID: %s, Clean: %t", c.id, false)
			} else {
				context.Logger.Errorf("Read error: ftp.read_error, ID: %s, Error: %v", c.id, err)
			}
			return
		}

		context.Logger.Debugf("FTP RECV: ftp.cmd_recv, ID: %s, Line: %v", c.id, line)

		c.handleCommand(line)
	}
}

// TransferOpen opens transfer with handler
func (c *Handler) TransferOpen() (net.Conn, error) {
	if c.transfer == nil {
		c.WriteMessage(550, "No connection declared")
		return nil, errors.New("No connection declared")
	}
	c.WriteMessage(150, "Using transfer connection")
	conn, err := c.transfer.Open()
	if err == nil {
		context.Logger.Debugf("FTP Transfer connection opened: ftp.transfer_open, ID: %s, RemoteAddr: %s, LocalAddr: %s", c.id, conn.RemoteAddr().String(), conn.LocalAddr().String())
	} else {
		context.Logger.Errorf("FTP Transfer connection open failed: %v: ", err)
	}

	return conn, err
}

// TransferClose closes transfer with handler
func (c *Handler) TransferClose() {
	if c.transfer != nil {
		c.WriteMessage(226, "Closing transfer connection")
		c.transfer.Close()
		c.transfer = nil
		context.Logger.Debugf("FTP Transfer connection closed: ftp.transfer_close. ID: %s", c.id)
	}
}

// handleCommand takes care of executing the received line.
func (c *Handler) handleCommand(line string) {
	command, param := utils.ParseLine(line)
	c.command = strings.ToUpper(command)
	c.param = param

	cmdDesc, ok := commandsMap[c.command]
	if !ok {
		c.WriteMessage(500, "Unknown command")
		return
	}

	if cmdDesc == nil {
		c.WriteMessage(500, c.command+" command not supported")
		return
	}

	if c.driver == nil && !cmdDesc.Open {
		c.WriteMessage(530, "Please login with USER and PASS")
		return
	}

	// Let's prepare to recover in case there's a command error.
	defer func() {
		if r := recover(); r != nil {
			context.Logger.Errorf("Internel error: %v, Trace: %s", r, debug.Stack())
			c.WriteMessage(500, fmt.Sprintf("Internal error: %s", r))
		}
	}()
	cmdDesc.Fn(c)
}

// WriteMessage writes server response
func (c *Handler) WriteMessage(code int, message string) {
	c.writeLine(fmt.Sprintf("%d %s", code, message))
}

func (c *Handler) end() {
	if c.transfer != nil {
		c.transfer.Close()
	}
}

func (c *Handler) disconnect() {
	if c.transfer != nil {
		c.transfer.Close()
	}
	c.conn.Close()
}

func (c *Handler) writeLine(line string) {
	context.Logger.Debugf("FTP SEND: ftp.cmd_send, ID: %s, Line: %s", c.id, line)
	c.writer.Write([]byte(line))
	c.writer.Write([]byte("\r\n"))
	c.writer.Flush()
}

// NewHandler initializes a client handler when someone connects.
func NewHandler(id string, connection net.Conn, driverFactory func() Driver) *Handler {

	p := &Handler{
		id:            id,
		conn:          connection,
		writer:        bufio.NewWriter(connection),
		reader:        bufio.NewReader(connection),
		connectedAt:   time.Now().UTC(),
		path:          "/",
		driverFactory: driverFactory,
	}

	// Just respecting the existing logic here, this could be probably be dropped at some point.
	return p
}
