package protocol

import (
	"bytes"
	"fmt"
	"testing"
)

func TestPacketRoundTrip(t *testing.T) {
	payload := []byte("hello-steam")
	var buf bytes.Buffer
	if err := WritePacket(&buf, payload); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPacket(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("%q", got)
	}
}

func TestMsgHdrRoundTrip(t *testing.T) {
	h := MsgHdr{Msg: EMsgChannelEncryptResponse, TargetJobID: 1, SourceJobID: 2}
	raw := h.Serialize()
	got, off, err := ParseMsgHdr(raw)
	if err != nil || off != msgHdrSize {
		t.Fatal(err, off)
	}
	if got != h {
		t.Fatalf("%+v", got)
	}
}

func TestMsgProtoRoundTrip(t *testing.T) {
	m := &MsgProto{
		Msg: EMsgClientLogon,
		Header: ProtoBufHeader{
			SteamID: 117093590311632896,
		},
		Body: ClientLogonBody{ProtocolVersion: 65580, ClientPackageVersion: 1561159470}.Serialize(),
	}
	raw := m.Serialize()
	// Match ValvePython anonymous ClientLogon wire hex exactly.
	want := "8a1500800900000009000000000000a00108ac800428aeceb5e805"
	got := fmt.Sprintf("%x", raw)
	if got != want {
		t.Fatalf("wire mismatch\n got %s\nwant %s", got, want)
	}
	parsed, err := ParseMsgProto(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Msg != EMsgClientLogon {
		t.Fatalf("msg %d", parsed.Msg)
	}
	if parsed.Header.SteamID != m.Header.SteamID {
		t.Fatalf("steamid %d", parsed.Header.SteamID)
	}
}

func TestProtoHeaderFixed64(t *testing.T) {
	h := ProtoBufHeader{SteamID: 117093590311632896}
	b := h.Serialize()
	// field 1, wiretype 1 (fixed64) => key = (1<<3)|1 = 9
	if b[0] != 0x09 {
		t.Fatalf("key byte %x want 09", b[0])
	}
	got, err := ParseProtoBufHeader(b)
	if err != nil || got.SteamID != h.SteamID {
		t.Fatalf("%+v %v", got, err)
	}
}
