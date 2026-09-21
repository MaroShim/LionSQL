package memengine

import (
	"github.com/vmihailenco/msgpack/v5"
)

// MarshalMsgPack serializes any value into high-performance MessagePack binary format.
func MarshalMsgPack(v any) ([]byte, error) {
	return msgpack.Marshal(v)
}

// UnmarshalMsgPack deserializes MessagePack binary data into the target pointer value.
func UnmarshalMsgPack(data []byte, target any) error {
	return msgpack.Unmarshal(data, target)
}
