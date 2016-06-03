package endpoint

import (
	"errors"
	"log"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/ugorji/go/codec"
)

const msgpackRPCReq = 0
const msgpackRPCRsp = 1
const msgpackRPCNotify = 2

// Precompute the reflect type for error.  Can't use error directly
// because Typeof takes an empty interface value.  This is annoying.
var typeOfError = reflect.TypeOf((*error)(nil)).Elem()

type methodType struct {
	sync.Mutex // protects counters
	method     reflect.Method
	ArgType    reflect.Type
	ReplyType  reflect.Type
	numCalls   uint
}

type service struct {
	name   string                 // name of service
	rcvr   reflect.Value          // receiver of methods for the service
	typ    reflect.Type           // type of the receiver
	method map[string]*methodType // registered call methods
	notify map[string]*methodType //registered notify methods
}

type request struct {
	done  chan int
	msgid uint32
	rsp   interface{}
	err   error
}

type endpoint struct {
	conn       net.Conn
	mu         sync.Mutex
	closed     bool
	err        error
	msgid      uint32
	pendingmu  sync.Mutex
	pending    map[uint32]*request
	mpk        *codec.MsgpackHandle
	serviceMap map[string]*service
}

func newEndpoint(conn net.Conn, mpk *codec.MsgpackHandle) (ep *endpoint) {
	return &endpoint{
		conn:       conn,
		mpk:        mpk,
		pending:    make(map[uint32]*request),
		serviceMap: make(map[string]*service),
	}
}

// Is this an exported - upper case - name?
func isExported(name string) bool {
	rune, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(rune)
}

// Is this type exported or a builtin?
func isExportedOrBuiltinType(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	// PkgPath will be non-empty even for an exported type,
	// so we need to check the type name as well.
	return isExported(t.Name()) || t.PkgPath() == ""
}

func (ep *endpoint) register(rcvr interface{}, name string, useName bool) error {
	ep.mu.Lock()
	defer ep.mu.Unlock()
	if ep.serviceMap == nil {
		ep.serviceMap = make(map[string]*service)
	}
	s := new(service)
	s.typ = reflect.TypeOf(rcvr)
	s.rcvr = reflect.ValueOf(rcvr)
	sname := reflect.Indirect(s.rcvr).Type().Name()
	if useName {
		sname = name
	}
	if sname == "" {
		s := "rpc.Register: no service name for type " + s.typ.String()
		return errors.New(s)
	}
	if !isExported(sname) && !useName {
		s := "rpc.Register: type " + sname + " is not exported"
		log.Print(s)
		return errors.New(s)
	}
	if _, present := ep.serviceMap[sname]; present {
		return errors.New("rpc: service already defined: " + sname)
	}
	s.name = sname

	// Install the methods
	s.method = suitableMethods(s.typ, true)

	if len(s.method) == 0 {
		str := ""

		// To help the user, see if a pointer receiver would work.
		method := suitableMethods(reflect.PtrTo(s.typ), false)
		if len(method) != 0 {
			str = "rpc.Register: type " + sname + " has no exported methods of suitable type (hint: pass a pointer to value of that type)"
		} else {
			str = "rpc.Register: type " + sname + " has no exported methods of suitable type"
		}
		log.Print(str)
		return errors.New(str)
	}
	ep.serviceMap[s.name] = s
	return nil
}

// suitableMethods returns suitable Rpc methods of typ, it will report
// error using log if reportErr is true.
func suitableMethods(typ reflect.Type, reportErr bool) map[string]*methodType {
	methods := make(map[string]*methodType)
	for m := 0; m < typ.NumMethod(); m++ {
		method := typ.Method(m)
		mtype := method.Type
		mname := method.Name
		// Method must be exported.
		if method.PkgPath != "" {
			continue
		}
		// Second arg must be a pointer.
		replyType := mtype.In(2)
		if replyType.Kind() != reflect.Ptr {
			if reportErr {
				log.Println("method", mname, "reply type not a pointer:", replyType)
			}
			continue
		}
		// Reply type must be exported.
		if !isExportedOrBuiltinType(replyType) {
			if reportErr {
				log.Println("method", mname, "reply type not exported:", replyType)
			}
			continue
		}
		// Method needs one out.
		if mtype.NumOut() != 1 {
			if reportErr {
				log.Println("method", mname, "has wrong number of outs:", mtype.NumOut())
			}
			continue
		}
		// The return type of the method must be error.
		if returnType := mtype.Out(0); returnType != typeOfError {
			if reportErr {
				log.Println("method", mname, "returns", returnType.String(), "not error")
			}
			continue
		}
		methods[mname] = &methodType{method: method, ArgType: argType, ReplyType: replyType}
	}
	return methods
}

func (ep *endpoint) send(reqobj []interface{}) (err error) {
	enc := codec.NewEncoder(ep.conn, ep.mpk)
	ep.mu.Lock()
	defer ep.mu.Unlock()
	if ep.closed {
		err = ep.err
		return
	}
	err = enc.Encode(reqobj)
	return
}

func (ep *endpoint) Call(method string, params ...interface{}) (rsp interface{}, err error) {
	msgid := atomic.AddUint32(&ep.msgid, 1)
	reqobj := []interface{}{msgpackRPCReq, msgid, method, params}
	ep.pendingmu.Lock()
	if ep.closed {
		ep.pendingmu.Unlock()
		err = ep.err
		return
	}
	req := &request{
		done:  make(chan int),
		msgid: msgid,
		rsp:   nil,
		err:   nil,
	}
	ep.pending[msgid] = req
	ep.pendingmu.Unlock()
	err = ep.send(reqobj)
	if err != nil {
		ep.pendingmu.Lock()
		delete(ep.pending, req.msgid)
		ep.pendingmu.Unlock()
		return
	}
	<-req.done
	rsp = req.rsp
	err = req.err
	return
}

func (ep *endpoint) Notify(method string, params ...interface{}) (err error) {
	reqobj := []interface{}{msgpackRPCNotify, method, params}
	err = ep.send(reqobj)
	return err
}

func (ep *endpoint) Register(svc interface{}) (err error) {
	return
}

func (ep *endpoint) RegisterName(svc interface{}, name string) (err error) {
	return
}

func (ep *endpoint) RegisterMethod(svc interface{}) (err error) {
	return
}

func (ep *endpoint) RegisterMethodName(method interface{}, name string) (err error) {
	return
}

func (ep *endpoint) Reading(closed chan int) (err error) {
	return
}
