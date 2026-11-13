package core

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"sort"
)

type mp4Box struct {
	kind  string
	start int
	body  int
	end   int
}

type cencSample struct {
	offset int64
	size   int64
	iv     [16]byte
	spans  []uint32
}

type cencLayout struct {
	block   cipher.Block
	start   int64
	moov    []byte
	total   int64
	samples []cencSample
}

var errCENCLayout = errors.New("加密视频的文件结构无法识别")

func mp4Boxes(data []byte, start, end int) ([]mp4Box, error) {
	var boxes []mp4Box
	for start < end {
		if end-start < 8 {
			return nil, errCENCLayout
		}
		size, head := uint64(binary.BigEndian.Uint32(data[start:])), 8
		if size == 1 {
			if end-start < 16 {
				return nil, errCENCLayout
			}
			size, head = binary.BigEndian.Uint64(data[start+8:]), 16
		} else if size == 0 {
			size = uint64(end - start)
		}
		if size < uint64(head) || size > uint64(end-start) {
			return nil, errCENCLayout
		}
		boxes = append(boxes, mp4Box{kind: string(data[start+4 : start+8]), start: start, body: start + head, end: start + int(size)})
		start += int(size)
	}
	return boxes, nil
}

func mp4Child(data []byte, start, end int, kind string) (mp4Box, bool) {
	boxes, err := mp4Boxes(data, start, end)
	if err != nil {
		return mp4Box{}, false
	}
	for _, box := range boxes {
		if box.kind == kind {
			return box, true
		}
	}
	return mp4Box{}, false
}

func mp4Table(data []byte, box mp4Box, skip, width int) ([]byte, int, error) {
	if box.end-box.body < skip+4 {
		return nil, 0, errCENCLayout
	}
	count := int(binary.BigEndian.Uint32(data[box.body+skip:]))
	rows := data[box.body+skip+4 : box.end]
	if count < 0 || width > 0 && count > len(rows)/width {
		return nil, 0, errCENCLayout
	}
	return rows, count, nil
}

func cencTrackSamples(moov []byte, track mp4Box) ([]cencSample, error) {
	table := track
	for _, kind := range []string{"mdia", "minf", "stbl"} {
		child, found := mp4Child(moov, table.body, table.end, kind)
		if !found {
			return nil, errCENCLayout
		}
		table = child
	}
	description, found := mp4Child(moov, table.body, table.end, "stsd")
	if !found || description.end-description.body < 8 {
		return nil, errCENCLayout
	}
	entries, err := mp4Boxes(moov, description.body+8, description.end)
	if err != nil || len(entries) == 0 {
		return nil, errCENCLayout
	}
	entry := entries[0]
	fixed := map[string]int{"encv": 78, "enca": 28}[entry.kind]
	if fixed == 0 {
		return nil, nil
	}
	if len(entries) != 1 || entry.end-entry.body < fixed {
		return nil, errCENCLayout
	}
	protection, found := mp4Child(moov, entry.body+fixed, entry.end, "sinf")
	if !found {
		return nil, errCENCLayout
	}
	format, hasFormat := mp4Child(moov, protection.body, protection.end, "frma")
	scheme, hasScheme := mp4Child(moov, protection.body, protection.end, "schm")
	information, hasInformation := mp4Child(moov, protection.body, protection.end, "schi")
	if !hasFormat || !hasScheme || !hasInformation || format.end-format.body < 4 || scheme.end-scheme.body < 8 {
		return nil, errCENCLayout
	}
	if string(moov[scheme.body+4:scheme.body+8]) != "cenc" {
		return nil, errors.New("视频用了暂不支持的加密方式 " + string(moov[scheme.body+4:scheme.body+8]))
	}
	encryption, found := mp4Child(moov, information.body, information.end, "tenc")
	if !found || encryption.end-encryption.body < 24 {
		return nil, errCENCLayout
	}
	vector := int(moov[encryption.body+7])
	if vector != 8 && vector != 16 {
		return nil, errCENCLayout
	}
	boxes := map[string]mp4Box{}
	children, err := mp4Boxes(moov, table.body, table.end)
	if err != nil {
		return nil, errCENCLayout
	}
	for _, child := range children {
		boxes[child.kind] = child
	}
	sizes, hasSizes := boxes["stsz"]
	runs, hasRuns := boxes["stsc"]
	auxiliary, hasAuxiliary := boxes["senc"]
	if !hasSizes || !hasRuns || !hasAuxiliary || sizes.end-sizes.body < 12 || auxiliary.end-auxiliary.body < 8 {
		return nil, errCENCLayout
	}
	uniform := binary.BigEndian.Uint32(moov[sizes.body+4:])
	sizeRows, count, err := mp4Table(moov, sizes, 8, 0)
	if err != nil || uniform == 0 && count > len(sizeRows)/4 {
		return nil, errCENCLayout
	}
	runRows, runCount, err := mp4Table(moov, runs, 4, 12)
	if err != nil {
		return nil, err
	}
	offsets, wide := boxes["stco"], 4
	if large, found := boxes["co64"]; found {
		offsets, wide = large, 8
	}
	offsetRows, chunkCount, err := mp4Table(moov, offsets, 4, wide)
	if offsets.kind == "" || err != nil {
		return nil, errCENCLayout
	}
	subsamples := moov[auxiliary.body+3]&2 != 0
	auxiliaryRows, auxiliaryCount, err := mp4Table(moov, auxiliary, 4, 0)
	if err != nil || auxiliaryCount != count {
		return nil, errCENCLayout
	}
	samples := make([]cencSample, 0, count)
	run := 0
	for chunk := 0; chunk < chunkCount && len(samples) < count; chunk++ {
		for run+1 < runCount && int(binary.BigEndian.Uint32(runRows[(run+1)*12:])) <= chunk+1 {
			run++
		}
		if runCount == 0 {
			return nil, errCENCLayout
		}
		offset := int64(binary.BigEndian.Uint32(offsetRows[chunk*wide:]))
		if wide == 8 {
			offset = int64(binary.BigEndian.Uint64(offsetRows[chunk*wide:]))
		}
		for range binary.BigEndian.Uint32(runRows[run*12+4:]) {
			if len(samples) == count {
				break
			}
			sample := cencSample{offset: offset, size: int64(uniform)}
			if uniform == 0 {
				sample.size = int64(binary.BigEndian.Uint32(sizeRows[len(samples)*4:]))
			}
			if len(auxiliaryRows) < vector {
				return nil, errCENCLayout
			}
			copy(sample.iv[:], auxiliaryRows[:vector])
			auxiliaryRows = auxiliaryRows[vector:]
			if subsamples {
				if len(auxiliaryRows) < 2 {
					return nil, errCENCLayout
				}
				spans := int(binary.BigEndian.Uint16(auxiliaryRows))
				auxiliaryRows = auxiliaryRows[2:]
				if len(auxiliaryRows) < spans*6 {
					return nil, errCENCLayout
				}
				sample.spans = make([]uint32, 0, spans*2)
				for range spans {
					sample.spans = append(sample.spans, uint32(binary.BigEndian.Uint16(auxiliaryRows)), binary.BigEndian.Uint32(auxiliaryRows[2:]))
					auxiliaryRows = auxiliaryRows[6:]
				}
			} else {
				sample.spans = []uint32{0, uint32(sample.size)}
			}
			samples = append(samples, sample)
			offset += sample.size
		}
	}
	if len(samples) != count {
		return nil, errCENCLayout
	}
	copy(moov[entry.start+4:], moov[format.body:format.body+4])
	for _, kind := range []string{"senc", "saiz", "saio"} {
		if box, found := boxes[kind]; found {
			copy(moov[box.start+4:], "free")
		}
	}
	copy(moov[protection.start+4:], "free")
	return samples, nil
}

func newCENCLayout(moov []byte, start, total int64, key []byte) (*cencLayout, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("视频解密密钥无效")
	}
	boxes, err := mp4Boxes(moov, 0, len(moov))
	if err != nil || len(boxes) != 1 || boxes[0].kind != "moov" {
		return nil, errCENCLayout
	}
	tracks, err := mp4Boxes(moov, boxes[0].body, boxes[0].end)
	if err != nil {
		return nil, err
	}
	layout := &cencLayout{block: block, start: start, moov: moov, total: total}
	for _, track := range tracks {
		if track.kind != "trak" {
			continue
		}
		samples, err := cencTrackSamples(moov, track)
		if err != nil {
			return nil, err
		}
		layout.samples = append(layout.samples, samples...)
	}
	sort.Slice(layout.samples, func(i, j int) bool { return layout.samples[i].offset < layout.samples[j].offset })
	return layout, nil
}

func (layout *cencLayout) keystream(sample *cencSample, position int64, data []byte) {
	counter := sample.iv
	low := binary.BigEndian.Uint64(counter[8:])
	blocks := uint64(position / aes.BlockSize)
	binary.BigEndian.PutUint64(counter[8:], low+blocks)
	if low+blocks < low {
		binary.BigEndian.PutUint64(counter[:8], binary.BigEndian.Uint64(counter[:8])+1)
	}
	stream := cipher.NewCTR(layout.block, counter[:])
	var skipped [aes.BlockSize]byte
	stream.XORKeyStream(skipped[:position%aes.BlockSize], skipped[:position%aes.BlockSize])
	stream.XORKeyStream(data, data)
}

func (layout *cencLayout) apply(offset int64, data []byte) {
	end := offset + int64(len(data))
	if low, high := max(offset, layout.start), min(end, layout.start+int64(len(layout.moov))); low < high {
		copy(data[low-offset:high-offset], layout.moov[low-layout.start:high-layout.start])
	}
	first := sort.Search(len(layout.samples), func(index int) bool {
		return layout.samples[index].offset+layout.samples[index].size > offset
	})
	for index := first; index < len(layout.samples) && layout.samples[index].offset < end; index++ {
		sample := &layout.samples[index]
		position, protected := sample.offset, int64(0)
		for span := 0; span+1 < len(sample.spans); span += 2 {
			position += int64(sample.spans[span])
			length := int64(sample.spans[span+1])
			if low, high := max(offset, position), min(end, position+length); low < high {
				layout.keystream(sample, protected+low-position, data[low-offset:high-offset])
			}
			position += length
			protected += length
		}
	}
}
