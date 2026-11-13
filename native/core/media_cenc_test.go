package core

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"testing"
)

func cencTestBox(kind string, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	box := binary.BigEndian.AppendUint32(nil, uint32(len(body)+8))
	return append(append(box, kind...), body...)
}

func cencTestWords(values ...uint32) []byte {
	var data []byte
	for _, value := range values {
		data = binary.BigEndian.AppendUint32(data, value)
	}
	return data
}

func cencTestFile(key []byte, spans [][]uint32, chunkOffsets [2]uint32) ([]byte, []byte, int) {
	block, _ := aes.NewCipher(key)
	sizes := []uint32{40, 33, 50}
	auxiliary := cencTestWords(0, 3)
	if spans != nil {
		auxiliary = cencTestWords(2, 3)
	}
	var clear, protected [][]byte
	for index, size := range sizes {
		sample := make([]byte, size)
		for position := range sample {
			sample[position] = byte(index*61 + position*7 + 1)
		}
		vector := []byte{byte(index + 1), 2, 3, 4, 5, 6, 7, 0xff}
		auxiliary = append(auxiliary, vector...)
		layout := []uint32{0, size}
		if spans != nil {
			layout = spans[index]
			auxiliary = binary.BigEndian.AppendUint16(auxiliary, uint16(len(layout)/2))
			for span := 0; span < len(layout); span += 2 {
				auxiliary = binary.BigEndian.AppendUint16(auxiliary, uint16(layout[span]))
				auxiliary = binary.BigEndian.AppendUint32(auxiliary, layout[span+1])
			}
		}
		encrypted := append([]byte{}, sample...)
		stream := cipher.NewCTR(block, append(vector, make([]byte, 8)...))
		position := uint32(0)
		for span := 0; span < len(layout); span += 2 {
			position += layout[span]
			stream.XORKeyStream(encrypted[position:position+layout[span+1]], encrypted[position:position+layout[span+1]])
			position += layout[span+1]
		}
		clear, protected = append(clear, sample), append(protected, encrypted)
	}
	protection := cencTestBox("sinf",
		cencTestBox("frma", []byte("avc1")),
		cencTestBox("schm", cencTestWords(0), []byte("cenc"), cencTestWords(0x10000)),
		cencTestBox("schi", cencTestBox("tenc", []byte{0, 0, 0, 0, 0, 0, 1, 8}, make([]byte, 16))))
	table := cencTestBox("stbl",
		cencTestBox("stsd", cencTestWords(0, 1), cencTestBox("encv", make([]byte, 78), protection)),
		cencTestBox("stsz", cencTestWords(0, 0, 3), cencTestWords(sizes...)),
		cencTestBox("stsc", cencTestWords(0, 2, 1, 2, 1, 2, 1, 1)),
		cencTestBox("stco", cencTestWords(0, 2, chunkOffsets[0], chunkOffsets[1])),
		cencTestBox("senc", auxiliary))
	head := append(cencTestBox("ftyp", []byte("isom")), cencTestBox("moov", cencTestBox("trak", cencTestBox("mdia", cencTestBox("minf", table))))...)
	gap := []byte("gap between chunks")
	plain := bytes.Join([][]byte{clear[0], clear[1], gap, clear[2]}, nil)
	file := append(append([]byte{}, head...), cencTestBox("mdat", protected[0], protected[1], gap, protected[2])...)
	return file, plain, len(head) + 8
}

func TestCENCLayoutDecryptsAnyRange(t *testing.T) {
	key := []byte("0123456789abcdef")
	for name, spans := range map[string][][]uint32{
		"整帧加密": nil,
		"分段加密": {{5, 35}, {3, 10, 4, 16}, {0, 50}},
	} {
		_, _, media := cencTestFile(key, spans, [2]uint32{})
		second := uint32(media + 40 + 33 + len("gap between chunks"))
		file, plain, _ := cencTestFile(key, spans, [2]uint32{uint32(media), second})
		moov := append([]byte{}, file[12:media-8]...)
		layout, err := newCENCLayout(moov, 12, int64(len(file)), key)
		if err != nil {
			t.Fatalf("%s：%v", name, err)
		}
		for _, step := range []int{1, 7, 16, 33, len(file)} {
			output := append([]byte{}, file...)
			for offset := 0; offset < len(output); offset += step {
				layout.apply(int64(offset), output[offset:min(len(output), offset+step)])
			}
			if !bytes.Equal(output[media:], plain) {
				t.Fatalf("%s 按 %d 字节分段解密后内容不对", name, step)
			}
			if !bytes.Contains(output[:media], []byte("avc1")) || bytes.Contains(output[:media], []byte("encv")) || bytes.Contains(output[:media], []byte("sinf")) || bytes.Contains(output[:media], []byte("senc")) {
				t.Fatalf("%s 按 %d 字节分段后文件头仍带加密标记", name, step)
			}
			if len(output) != len(file) {
				t.Fatalf("%s 改变了文件长度", name)
			}
		}
	}
}

func TestCENCRequestRange(t *testing.T) {
	for header, expected := range map[string][3]int64{
		"":              {0, 99, 1},
		"bytes=10-19":   {10, 19, 1},
		"bytes=90-":     {90, 99, 1},
		"bytes=90-500":  {90, 99, 1},
		"bytes=-10":     {90, 99, 1},
		"bytes=100-":    {0, 0, 0},
		"bytes=5-1":     {0, 0, 0},
		"bytes=0-1,5-9": {0, 0, 0},
		"items=0-1":     {0, 0, 0},
	} {
		start, end, valid := cencRequestRange(header, 100)
		if valid != (expected[2] == 1) || valid && (start != expected[0] || end != expected[1]) {
			t.Errorf("%q 解析成 %d-%d %v", header, start, end, valid)
		}
	}
}
