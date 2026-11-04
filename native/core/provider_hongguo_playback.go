package core

import (
	"crypto/aes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/bits"
	"regexp"
	"strconv"
	"strings"
)

var hongguoNumericID = regexp.MustCompile(`^[0-9]{1,32}$`)

var hongguoQualityNumber = regexp.MustCompile(`[0-9]+`)

func decodeHongguoBase64(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	if err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.Strict().DecodeString(value)
}

func hongguoContentKey(value string) ([]byte, error) {
	if len(value) > 1024 {
		return nil, errors.New("红果媒体密钥数据过长")
	}
	raw, err := decodeHongguoBase64(value)
	if err != nil || len(raw) < 3 {
		return nil, errors.New("红果媒体密钥编码无效")
	}
	tagLength := int(raw[0]^raw[1]^raw[2]) - 48
	contentLength := len(raw) - tagLength - 1
	if tagLength < 1 || contentLength < 33 || contentLength >= len(raw) {
		return nil, errors.New("红果媒体密钥结构无效")
	}
	seed := raw[len(raw)-tagLength-2] ^ raw[len(raw)-tagLength-1]
	tag := make([]byte, tagLength)
	for index := range tag {
		tag[index] = raw[len(raw)-tagLength+index] ^ seed
	}
	if string(tag) == "app_v2" || string(tag) == "web_v2" {
		return nil, errors.New("红果媒体密钥版本暂不支持")
	}
	decoded := make([]byte, contentLength)
	previousEven, previousOdd := byte(250), byte(85)
	for index, current := range raw[1 : 1+contentLength] {
		previous := previousEven
		if index%2 == 0 {
			previousEven = current
		} else {
			previous = previousOdd
			previousOdd = current
		}
		decoded[index] = byte(int(previous^current) - 21 - bits.OnesCount(uint(index)))
	}
	padding, err := strconv.ParseUint(string(decoded[:1]), 36, 8)
	if err != nil || contentLength-int(padding)-1 != 32 {
		return nil, errors.New("红果媒体密钥内容无效")
	}
	key, err := hex.DecodeString(string(decoded[1:33]))
	if err != nil || len(key) != aes.BlockSize {
		return nil, fmt.Errorf("红果媒体密钥不是有效的 AES-128 密钥")
	}
	return key, nil
}
