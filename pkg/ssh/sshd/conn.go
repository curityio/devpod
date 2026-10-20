package sshd

import (
	"context"
	"errors"
	"net"
	"time"
)

type serverConn struct {
	net.Conn

	idleTimeout   time.Duration
	maxDeadline   time.Time
	closeCanceler context.CancelFunc
}

func (c *serverConn) Write(p []byte) (n int, err error) {
	c.updateDeadline()
	n, err = c.Conn.Write(p)
	if isNetError(err) && c.closeCanceler != nil {
		c.closeCanceler()
	}
	return
}

func (c *serverConn) Read(b []byte) (n int, err error) {
	c.updateDeadline()
	n, err = c.Conn.Read(b)
	if isNetError(err) && c.closeCanceler != nil {
		c.closeCanceler()
	}
	return
}

func (c *serverConn) Close() (err error) {
	err = c.Conn.Close()
	if c.closeCanceler != nil {
		c.closeCanceler()
	}
	return
}

func (c *serverConn) updateDeadline() {
	switch {
	case c.idleTimeout > 0:
		idleDeadline := time.Now().Add(c.idleTimeout)
		if idleDeadline.Unix() < c.maxDeadline.Unix() || c.maxDeadline.IsZero() {
			_ = c.Conn.SetDeadline(idleDeadline)
			return
		}
		fallthrough
	default:
		_ = c.Conn.SetDeadline(c.maxDeadline)
	}
}

func isNetError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr)
}
