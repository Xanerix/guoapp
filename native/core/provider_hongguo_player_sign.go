package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"math/bits"
	"net/http"
	"strconv"
	"time"
)

// hongguoPlayerNonce is the 11-byte random nonce used by the player (novelread)
// Argus/Ladon signing. It mirrors the shape of the comment nonce but is scoped
// to the player endpoints so comment signatures never leak into them.
type hongguoPlayerNonce [11]byte

func newHongguoPlayerNonce() (hongguoPlayerNonce, error) {
	var nonce hongguoPlayerNonce
	_, err := rand.Read(nonce[:])
	return nonce, err
}

// signHongguoPlayerRequest signs a regular (non-comment) 红果 App request with
// X-Gorgon / X-Khronos / X-SS-Req-Ticket plus the Argus551 + Ladon headers the
// player endpoints (/novel/player/video_model/v1/, /novel/player/video_detail/v1/)
// now require. The comment path keeps its own signer untouched.
func signHongguoPlayerRequest(request *http.Request, body []byte, now time.Time, installID string) {
	stamp := uint32(now.Unix())
	// X-Gorgon (keep the existing gorgon body, the server still validates it).
	queryHash := md5.Sum([]byte(request.URL.RawQuery))
	var payload [20]byte
	copy(payload[:4], queryHash[:4])
	if body != nil {
		bodyHash := md5.Sum(body)
		copy(payload[4:8], bodyHash[:4])
		request.Header.Set("X-SS-STUB", hex.EncodeToString(bodyHash[:]))
	}
	copy(payload[12:16], []byte{0, 6, 11, 28})
	binary.BigEndian.PutUint32(payload[16:], stamp)
	key := [...]byte{0x44, 0xb9, 0xb9, 0xd9, 0xa4, 0xae, 0xf9, 0xfc, 0xa4, 0x93, 0xaa, 0x75, 0x7c, 0xa3, 0xc2, 0xc4, 0xa4, 0x96, 0x93, 0x8f}
	for index := range payload {
		payload[index] ^= key[index]
	}
	for index := range payload {
		mixed := bits.RotateLeft8(payload[index], 4) ^ payload[(index+1)%len(payload)]
		payload[index] = bits.Reverse8(mixed) ^ 0xff ^ byte(len(payload))
	}
	signature := append([]byte{0x84, 0x04, 0x40, 0x1c, 0, 0}, payload[:]...)
	request.Header.Set("X-Khronos", strconv.FormatUint(uint64(stamp), 10))
	request.Header.Set("X-Gorgon", hex.EncodeToString(signature))
	request.Header.Set("X-SS-Req-Ticket", strconv.FormatInt(now.UnixMilli(), 10))

	// Argus551 + Ladon for the player endpoints.
	nonce, err := newHongguoPlayerNonce()
	if err != nil {
		return
	}
	request.Header.Set("X-Argus", hongguoPlayerArgus(request.URL.RawQuery, body, stamp, installID, nonce))
	request.Header.Set("X-Ladon", hongguoPlayerLadon(request.URL.Query().Get("aid"), stamp, nonce[2:6]))
}

// hongguoPlayerArgus builds the X-Argus header for the player (aid=8662,
// novelread 7.3.5.32). The wire format reuses the same protobuf + SM3 + Feistel
// construction as the comment argus, but with player-scoped identity fields so
// the server accepts it on /novel/player/* paths. "Argus551" refers to the SDK
// version carried in field 1.
func hongguoPlayerArgus(query string, body []byte, timestamp uint32, installID string, nonce hongguoPlayerNonce) string {
	key, _ := hex.DecodeString("ac1adaae95a7af94a5114ab3b3a97dd80050aa0a39314c40528caec95256c28c")
	var message []byte
	varint := func(field, value uint64) {
		message = binary.AppendUvarint(message, field*8)
		message = binary.AppendUvarint(message, value)
	}
	bytesField := func(field uint64, value []byte) {
		message = binary.AppendUvarint(message, field*8+2)
		message = binary.AppendUvarint(message, uint64(len(value)))
		message = append(message, value...)
	}
	// Argus551 SDK version (field 1).
	varint(1, 0x20200551*2)
	varint(2, 2)
	varint(3, uint64(binary.LittleEndian.Uint32(nonce[6:10])))
	bytesField(4, []byte("8662")) // aid for novelread
	bytesField(6, []byte(installID))
	bytesField(7, []byte("7.3.5.32")) // version_name for the current build
	bytesField(8, []byte("v04.07.01-ml-android"))
	varint(9, 135135744)
	bytesField(10, make([]byte, 8))
	varint(12, uint64(timestamp)*2)
	bodyHash := hongguoSM3(body)
	queryHash := hongguoSM3([]byte(query))
	bytesField(13, bodyHash[:6])
	bytesField(14, queryHash[:6])
	nested := []byte{8, 2 + (nonce[10]%49)*2}
	for _, field := range []uint64{2, 3, 4} {
		nested = binary.AppendUvarint(nested, field*8)
		nested = binary.AppendUvarint(nested, 1388734)
	}
	bytesField(15, nested)
	bytesField(20, []byte("none"))
	varint(21, 738)
	data := hongguoCommentPad(message)
	seed := append(append(append([]byte(nil), key...), 0x3c, 0xcc, 0x56, 0x7b), key...)
	derived := hongguoSM3(seed)
	var rounds [72]uint64
	for i := 0; i < 4; i++ {
		rounds[i] = binary.LittleEndian.Uint64(derived[i*8:])
	}
	for i := 4; i < len(rounds); i++ {
		mixed := bits.RotateLeft64(rounds[i-1], -3) ^ rounds[i-3]
		mixed ^= bits.RotateLeft64(mixed, -1)
		rounds[i] = ^rounds[i-4] ^ mixed ^ (uint64(0x3dc94c3a046d678b)>>uint((i-4)%62))&1 ^ 3
	}
	for offset := 0; offset < len(data); offset += 16 {
		left, right := binary.LittleEndian.Uint64(data[offset:]), binary.LittleEndian.Uint64(data[offset+8:])
		for _, round := range rounds {
			left, right = right, left^(bits.RotateLeft64(right, 1)&bits.RotateLeft64(right, 8))^bits.RotateLeft64(right, 2)^round
		}
		binary.LittleEndian.PutUint64(data[offset:], left)
		binary.LittleEndian.PutUint64(data[offset+8:], right)
	}
	xor := []byte{0xd0, 0x4f, 0xfd, 0xff, 0xd0, 0x4f, 0xfd, 0xff}
	for i := range data {
		data[i] ^= xor[i%8]
	}
	data = append(xor, data...)
	for left, right := 0, len(data)-1; left < right; left, right = left+1, right-1 {
		data[left], data[right] = data[right], data[left]
	}
	container := append([]byte{0xa6, 0xe7, 0x83, 0xee, 0x70, 0x01, 0x10, 0x09, 0x18}, data...)
	container = hongguoCommentPad(append(container, 0x56, 0x7b))
	aesKey, iv := md5.Sum(key[:16]), md5.Sum(key[16:])
	block, _ := aes.NewCipher(aesKey[:])
	cipher.NewCBCEncrypter(block, iv[:]).CryptBlocks(container, container)
	return base64.StdEncoding.EncodeToString(append([]byte{0x3c, 0xcc}, container...))
}

// hongguoPlayerLadon reuses the Ladon block cipher with the player's aid. The
// timestamp-derived payload matches what the server expects for aid=8662.
func hongguoPlayerLadon(aid string, timestamp uint32, prefix []byte) string {
	hash := md5.Sum(append(append([]byte(nil), prefix...), aid...))
	digest := []byte(hex.EncodeToString(hash[:]))
	left, right := binary.LittleEndian.Uint64(digest), binary.LittleEndian.Uint64(digest[8:])
	queue := []uint64{binary.LittleEndian.Uint64(digest[16:]), binary.LittleEndian.Uint64(digest[24:])}
	var keys [34]uint64
	for i := range keys {
		keys[i] = left
		mixed := (bits.RotateLeft64(right, -8) + left) ^ uint64(i)
		queue = append(queue, mixed)
		left = mixed ^ bits.RotateLeft64(left, 3)
		right, queue = queue[0], queue[1:]
	}
	payload := []byte(strconv.FormatUint(uint64(timestamp), 10) + "-" + aid)
	data := hongguoCommentPad(payload)
	for offset := 0; offset < len(data); offset += 16 {
		left, right := binary.LittleEndian.Uint64(data[offset:]), binary.LittleEndian.Uint64(data[offset+8:])
		for _, key := range keys {
			right = key ^ (left + bits.RotateLeft64(right, -8))
			left = right ^ bits.RotateLeft64(left, 3)
		}
		binary.LittleEndian.PutUint64(data[offset:], left)
		binary.LittleEndian.PutUint64(data[offset+8:], right)
	}
	return base64.StdEncoding.EncodeToString(append(append([]byte(nil), prefix...), data...))
}
