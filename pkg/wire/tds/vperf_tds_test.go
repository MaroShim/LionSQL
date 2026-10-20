package tds_test

import (
	"bytes"
	"testing"

	"lionsql/pkg/wire/tds"
)

func TestVPerf_WritePacketWithSize(t *testing.T) {
	tests := []struct {
		name       string
		packetSize uint32
		payloadLen int
	}{
		{"Default4096", 4096, 10000},
		{"Large8192", 8192, 20000},
		{"Max32768", 32768, 70000},
		{"FallbackSmall", 256, 1000}, // below 512, falls back to 4096
		{"FallbackLarge", 65536, 1000}, // above 32768, falls back to 4096
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := make([]byte, tt.payloadLen)
			for i := range payload {
				payload[i] = byte(i % 251)
			}

			var buf bytes.Buffer
			err := tds.WritePacketWithSize(&buf, tds.PacketTabular, 1, payload, tt.packetSize)
			if err != nil {
				t.Fatalf("WritePacketWithSize failed: %v", err)
			}

			// Read back using standard ReadPacket
			pkt, err := tds.ReadPacket(&buf)
			if err != nil {
				t.Fatalf("ReadPacket failed: %v", err)
			}

			if pkt.Type != tds.PacketTabular {
				t.Fatalf("expected packet type %d, got %d", tds.PacketTabular, pkt.Type)
			}
			if !bytes.Equal(pkt.Payload, payload) {
				t.Fatalf("reassembled payload does not match original (len %d vs %d)", len(pkt.Payload), len(payload))
			}
		})
	}
}

func TestVPerf_WriteUTF16LEDirect(t *testing.T) {
	testStrings := []string{
		"",
		"Hello, World!",
		"LionSQL Fast OLTP Engine",
		"안녕하세요 LionSQL 고성능 데이터베이스입니다",
		"Special chars: !@#$%^&*()_+~`|}{[]:;?><,./",
		"Surrogate pair: 𠮷野家 and 😃🎉",
	}

	for _, s := range testStrings {
		expected := tds.EncodeUTF16LE(s)

		var buf bytes.Buffer
		n := tds.WriteUTF16LEDirect(&buf, s)

		if n != len(expected) {
			t.Fatalf("for string %q: expected %d bytes, got %d", s, len(expected), n)
		}
		if !bytes.Equal(buf.Bytes(), expected) {
			t.Fatalf("for string %q: Direct bytes do not match EncodeUTF16LE", s)
		}
	}
}
