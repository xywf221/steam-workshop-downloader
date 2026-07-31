// Package protocol implements Steam CM wire framing and message headers.
package protocol

// EMsg is a Steam message type id (with optional high proto bit on the wire).
type EMsg uint32

// Selected EMsg values used by the workshop downloader.
const (
	EMsgMulti                   EMsg = 1
	EMsgServiceMethod                EMsg = 146
	EMsgServiceMethodResponse        EMsg = 147
	EMsgServiceMethodCallFromClient  EMsg = 151
	EMsgServiceMethodSendToClient    EMsg = 152
	EMsgChannelEncryptRequest        EMsg = 1303
	EMsgChannelEncryptResponse       EMsg = 1304
	EMsgChannelEncryptResult         EMsg = 1305
	EMsgClientHeartBeat              EMsg = 703
	EMsgClientLogOnResponse          EMsg = 751
	EMsgClientCMList                 EMsg = 783
	EMsgClientGetDepotDecryptionKey  EMsg = 5438
	EMsgClientGetDepotDecryptionKeyResponse EMsg = 5439
	EMsgClientLogon                  EMsg = 5514
)

// ProtoBit is set on the on-wire emsg when the body is protobuf-framed.
const ProtoBit uint32 = 0x80000000

// IsProto reports whether the raw emsg dword has the proto bit set.
func IsProto(raw uint32) bool { return raw&ProtoBit != 0 }

// ClearProtoBit strips the proto bit.
func ClearProtoBit(raw uint32) EMsg { return EMsg(raw &^ ProtoBit) }

// SetProtoBit sets the proto bit on an EMsg.
func SetProtoBit(msg EMsg) uint32 { return uint32(msg) | ProtoBit }
