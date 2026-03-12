// Package redis implements a minimal Redis client using raw RESP protocol.
// Only the commands needed for cache and locking are supported.
// No external dependencies.
package redis

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const connectTimeout = 10 * time.Second

// Conn is a low-level Redis connection.
type Conn struct {
	conn net.Conn
	r    *bufio.Reader
}

// Dial connects to a Redis server parsed from a URL.
// Format: redis://:password@host:port/key-path
func Dial(u *url.URL) (*Conn, error) {
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "6379"
	}
	var password string
	if u.User != nil {
		password, _ = u.User.Password()
	}
	return DialAddr(net.JoinHostPort(host, port), password)
}

// DialAddr connects to host:port with optional password.
func DialAddr(addr, password string) (*Conn, error) {
	dialer := net.Dialer{Timeout: connectTimeout}
	nc, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("redis: connect %s: %w", addr, err)
	}
	c := &Conn{conn: nc, r: bufio.NewReader(nc)}

	if password != "" {
		if err := c.Do("AUTH", password); err != nil {
			nc.Close()
			return nil, fmt.Errorf("redis: AUTH: %w", err)
		}
	}
	return c, nil
}

// Close closes the connection.
func (c *Conn) Close() error { return c.conn.Close() }

// Do sends a command and reads a simple (+OK/-ERR) reply. Returns nil
// for +OK, an error for -ERR replies or network errors.
func (c *Conn) Do(args ...string) error {
	if err := c.Send(args...); err != nil {
		return err
	}
	_, err := c.ReadStatus()
	return err
}

// Send writes a RESP array command without reading a reply.
func (c *Conn) Send(args ...string) error {
	var buf []byte
	buf = append(buf, '*')
	buf = strconv.AppendInt(buf, int64(len(args)), 10)
	buf = append(buf, '\r', '\n')
	for _, arg := range args {
		buf = append(buf, '$')
		buf = strconv.AppendInt(buf, int64(len(arg)), 10)
		buf = append(buf, '\r', '\n')
		buf = append(buf, arg...)
		buf = append(buf, '\r', '\n')
	}
	_, err := c.conn.Write(buf)
	return err
}

// SendBinary writes a RESP command where one argument is binary data.
// All arguments before binaryIdx are strings; the argument at binaryIdx
// is raw bytes; remaining arguments are strings.
func (c *Conn) SendBinary(args []string, binaryIdx int, data []byte) error {
	total := len(args)
	var buf []byte
	buf = append(buf, '*')
	buf = strconv.AppendInt(buf, int64(total), 10)
	buf = append(buf, '\r', '\n')

	for i, arg := range args {
		if i == binaryIdx {
			// Write header for binary arg
			buf = append(buf, '$')
			buf = strconv.AppendInt(buf, int64(len(data)), 10)
			buf = append(buf, '\r', '\n')
			// Flush header, then write data separately to avoid huge alloc
			if _, err := c.conn.Write(buf); err != nil {
				return err
			}
			if _, err := c.conn.Write(data); err != nil {
				return err
			}
			buf = buf[:0]
			buf = append(buf, '\r', '\n')
		} else {
			buf = append(buf, '$')
			buf = strconv.AppendInt(buf, int64(len(arg)), 10)
			buf = append(buf, '\r', '\n')
			buf = append(buf, arg...)
			buf = append(buf, '\r', '\n')
		}
	}
	_, err := c.conn.Write(buf)
	return err
}

// ReadStatus reads a +OK or -ERR reply. Returns the status string.
func (c *Conn) ReadStatus() (string, error) {
	line, err := c.readLine()
	if err != nil {
		return "", err
	}
	if len(line) == 0 {
		return "", fmt.Errorf("redis: empty reply")
	}
	switch line[0] {
	case '+':
		return line[1:], nil
	case '-':
		return "", fmt.Errorf("redis: %s", line[1:])
	default:
		return "", fmt.Errorf("redis: unexpected reply %q", line)
	}
}

// ReadBulk reads a $<len>\r\n<data>\r\n bulk string.
// Returns (nil, nil) for $-1 (key not found).
func (c *Conn) ReadBulk() ([]byte, error) {
	line, err := c.readLine()
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, fmt.Errorf("redis: empty reply")
	}
	switch line[0] {
	case '$':
		n, err := strconv.ParseInt(line[1:], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("redis: bad bulk length: %w", err)
		}
		if n < 0 {
			return nil, nil // key doesn't exist
		}
		data := make([]byte, n+2) // +2 for trailing \r\n
		if _, err := io.ReadFull(c.r, data); err != nil {
			return nil, fmt.Errorf("redis: read bulk: %w", err)
		}
		return data[:n], nil
	case '-':
		return nil, fmt.Errorf("redis: %s", line[1:])
	default:
		return nil, fmt.Errorf("redis: unexpected reply %q for bulk", line)
	}
}

// ReadInt reads a :<integer>\r\n reply.
func (c *Conn) ReadInt() (int64, error) {
	line, err := c.readLine()
	if err != nil {
		return 0, err
	}
	if len(line) == 0 {
		return 0, fmt.Errorf("redis: empty reply")
	}
	switch line[0] {
	case ':':
		return strconv.ParseInt(line[1:], 10, 64)
	case '-':
		return 0, fmt.Errorf("redis: %s", line[1:])
	default:
		return 0, fmt.Errorf("redis: unexpected reply %q for int", line)
	}
}

// ReadAny reads any RESP reply line. Used when the reply type varies
// (e.g. SET NX returns +OK or $-1).
func (c *Conn) ReadAny() (string, error) {
	line, err := c.readLine()
	if err != nil {
		return "", err
	}
	if len(line) == 0 {
		return "", fmt.Errorf("redis: empty reply")
	}
	if line[0] == '-' {
		return "", fmt.Errorf("redis: %s", line[1:])
	}
	return line, nil
}

func (c *Conn) readLine() (string, error) {
	line, err := c.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// URLKey extracts the key from a Redis URL path (strips leading /).
func URLKey(u *url.URL) string {
	return strings.TrimPrefix(u.Path, "/")
}
