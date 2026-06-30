package proxy

import (
	"fmt"

	"github.com/tidwall/redcon"
)

func writeAny(conn redcon.Conn, value any) {
	switch v := value.(type) {
	case nil:
		conn.WriteNull()
	case string:
		conn.WriteString(v)
	case []byte:
		conn.WriteBulk(v)
	case int:
		conn.WriteInt(v)
	case int64:
		conn.WriteInt64(v)
	case uint64:
		conn.WriteUint64(v)
	case bool:
		if v {
			conn.WriteInt(1)
		} else {
			conn.WriteInt(0)
		}
	case []interface{}:
		conn.WriteArray(len(v))
		for _, item := range v {
			writeAny(conn, item)
		}
	default:
		conn.WriteBulkString(fmt.Sprint(v))
	}
}

func writeUpstreamError(conn redcon.Conn) {
	conn.WriteError("ERR upstream request failed")
}
