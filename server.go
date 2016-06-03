package endpoint

import (
	"net"

	"github.com/ugorji/go/codec"
)

type ServerConn struct {
	ep     *endpoint
	conn   net.Conn
	closed chan int
}

func NewServerConn(conn net.Conn, mpk *codec.MsgpackHandle) *ServerConn {
	return &ServerConn{
		conn: conn,
		ep:   newEndpoint(conn, mpk),
	}
}

func (sc *ServerConn) Serve() error {
	return sc.ep.Reading(sc.closed)
}

func (sc *ServerConn) Call(method string, params ...interface{}) (rsp interface{}, err error) {
	return sc.ep.Call(method, params)
}

func (sc *ServerConn) Notify(method string, params ...interface{}) (err error) {
	return sc.ep.Notify(method, params)
}

func (sc *ServerConn) Register(svc interface{}) (err error) {
	return sc.ep.Register(svc)
}

func (sc *ServerConn) Close() {
	close(sc.closed)
	sc.conn.Close()
}
