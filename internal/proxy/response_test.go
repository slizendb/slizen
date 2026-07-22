package proxy

import (
	"net"
	"reflect"
	"testing"

	"github.com/tidwall/redcon"
)

type fakeConn struct {
	writes  []string
	closed  bool
	context interface{}
}

func FuzzWriteAny(f *testing.F) {
	seeds := []struct {
		kind    uint8
		payload []byte
		number  int64
	}{
		{kind: 0},
		{kind: 1, payload: []byte("OK")},
		{kind: 2, payload: []byte{0x00, 0xff, 0x80, '\r', '\n'}},
		{kind: 3, number: -1},
		{kind: 4, number: 42},
		{kind: 5, number: -1},
		{kind: 6, number: 1},
		{kind: 7, payload: []byte("nested"), number: 2},
		{kind: 8, payload: []byte("fallback")},
	}
	for _, seed := range seeds {
		f.Add(seed.kind, seed.payload, seed.number)
	}

	f.Fuzz(func(t *testing.T, kind uint8, payload []byte, number int64) {
		var value any
		switch kind % 9 {
		case 0:
			value = nil
		case 1:
			value = string(payload)
		case 2:
			value = payload
		case 3:
			value = int(number)
		case 4:
			value = number
		case 5:
			value = uint64(number)
		case 6:
			value = number%2 == 0
		case 7:
			value = []interface{}{string(payload), number, nil, payload}
		case 8:
			value = struct{ Value string }{Value: string(payload)}
		}

		conn := &fakeConn{}
		writeAny(conn, value)
		if len(conn.writes) == 0 {
			t.Fatalf("writeAny produced no response for kind %d", kind%9)
		}
	})
}

func (f *fakeConn) RemoteAddr() string             { return "test" }
func (f *fakeConn) Close() error                   { f.closed = true; return nil }
func (f *fakeConn) WriteError(msg string)          { f.writes = append(f.writes, "-"+msg) }
func (f *fakeConn) WriteString(str string)         { f.writes = append(f.writes, "+"+str) }
func (f *fakeConn) WriteBulk(bulk []byte)          { f.writes = append(f.writes, "$"+string(bulk)) }
func (f *fakeConn) WriteBulkString(bulk string)    { f.writes = append(f.writes, "$"+bulk) }
func (f *fakeConn) WriteInt(num int)               { f.writes = append(f.writes, ":"+string(rune('0'+num))) }
func (f *fakeConn) WriteInt64(num int64)           { f.writes = append(f.writes, ":"+string(rune('0'+num))) }
func (f *fakeConn) WriteUint64(num uint64)         { f.writes = append(f.writes, ":"+string(rune('0'+num))) }
func (f *fakeConn) WriteArray(count int)           { f.writes = append(f.writes, "*"+string(rune('0'+count))) }
func (f *fakeConn) WriteNull()                     { f.writes = append(f.writes, "_") }
func (f *fakeConn) WriteRaw(data []byte)           { f.writes = append(f.writes, string(data)) }
func (f *fakeConn) WriteAny(any interface{})       {}
func (f *fakeConn) Context() interface{}           { return f.context }
func (f *fakeConn) SetContext(v interface{})       { f.context = v }
func (f *fakeConn) SetReadBuffer(bytes int)        {}
func (f *fakeConn) Detach() redcon.DetachedConn    { return nil }
func (f *fakeConn) ReadPipeline() []redcon.Command { return nil }
func (f *fakeConn) PeekPipeline() []redcon.Command { return nil }
func (f *fakeConn) NetConn() net.Conn              { return nil }

func TestWriteAnyConvertsRedisTypes(t *testing.T) {
	conn := &fakeConn{}
	writeAny(conn, []interface{}{"OK", int64(2), nil, []byte("bulk")})
	want := []string{"*4", "+OK", ":2", "_", "$bulk"}
	if !reflect.DeepEqual(conn.writes, want) {
		t.Fatalf("writes = %#v, want %#v", conn.writes, want)
	}
}
