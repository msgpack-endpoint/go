package endpoint

import (
	"net"

	"github.com/ugorji/go/codec"
)

type Client struct {
	mpk    *codec.MsgpackHandle
	ep     *endpoint
	conn   net.Conn
	err    error
	closed chan int
}

func NewClient(conn net.Conn, handle *codec.MsgpackHandle) (c *Client) {
	c = &Client{
		mpk:  handle,
		conn: conn,
	}
	c.ep = newEndpoint(c.conn, c.mpk)
	go func() {
		c.err = c.ep.Reading(c.closed)
	}()
	return
}

func (c *Client) Call(method string, params ...interface{}) (rsp interface{}, err error) {
	return c.ep.Call(method, params)
}

func (c *Client) Notify(method string, params ...interface{}) (err error) {
	return c.ep.Notify(method, params)
}

func (c *Client) Register(svc interface{}) (err error) {
	return c.ep.Register(svc)
}

func (c *Client) Close() {
	close(c.closed)
	c.conn.Close()
}
