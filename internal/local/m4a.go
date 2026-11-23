package local

import (
	"encoding/binary"
	"io"
	"math"
)

type mediaBox struct {
	kind       string
	start, end int64
}

// nextMediaBox reads an ISO base media box header within its parent.
func nextMediaBox(r io.ReaderAt, start, end int64) (mediaBox, bool) {
	if end-start < 8 {
		return mediaBox{}, false
	}
	var header [16]byte
	if _, err := r.ReadAt(header[:8], start); err != nil {
		return mediaBox{}, false
	}
	length := uint64(binary.BigEndian.Uint32(header[:4]))
	body := start + 8
	switch length {
	case 0:
		length = uint64(end - start)
	case 1:
		if end-start < 16 {
			return mediaBox{}, false
		}
		if _, err := r.ReadAt(header[8:], body); err != nil {
			return mediaBox{}, false
		}
		length = binary.BigEndian.Uint64(header[8:])
		body += 8
	}
	if length < uint64(body-start) || length > uint64(end-start) {
		return mediaBox{}, false
	}
	return mediaBox{kind: string(header[4:8]), start: body, end: start + int64(length)}, true
}

func (box mediaBox) child(r io.ReaderAt, kind string) mediaBox {
	for start := box.start; start < box.end; {
		child, ok := nextMediaBox(r, start, box.end)
		if !ok {
			break
		}
		if child.kind == kind {
			return child
		}
		start = child.end
	}
	return mediaBox{}
}

func (box mediaBox) read(r io.ReaderAt, data []byte, offset int64) bool {
	if offset < 0 || offset > box.end-box.start-int64(len(data)) {
		return false
	}
	_, err := r.ReadAt(data, box.start+offset)
	return err == nil
}

type m4aInfo struct {
	duration int64
	lossless bool
}

// m4aTrack reads audio metadata only. The platform player decides whether
// its decoder can open the sample entry.
func m4aTrack(r io.ReaderAt, size int64) (m4aInfo, bool) {
	movie := (mediaBox{end: size}).child(r, "moov")
	for start := movie.start; start < movie.end; {
		track, ok := nextMediaBox(r, start, movie.end)
		if !ok {
			break
		}
		start = track.end
		if track.kind != "trak" {
			continue
		}
		media := track.child(r, "mdia")
		var handler [4]byte
		if !media.child(r, "hdlr").read(r, handler[:], 8) || string(handler[:]) != "soun" {
			continue
		}
		entries := media.child(r, "minf").child(r, "stbl").child(r, "stsd")
		entry, _ := nextMediaBox(r, entries.start+8, entries.end)
		ticks, scale := mediaTime(r, media.child(r, "mdhd"))
		_, movieScale := mediaTime(r, movie.child(r, "mvhd"))
		if length, ok := editListLength(r, track.child(r, "edts").child(r, "elst")); ok && movieScale != 0 {
			ticks, scale = length, movieScale
		}
		return m4aInfo{
			duration: mediaMilliseconds(ticks, scale),
			lossless: entry.kind == "alac" || entry.kind == "fLaC",
		}, true
	}
	return m4aInfo{}, false
}

func mediaTime(r io.ReaderAt, header mediaBox) (ticks, scale uint64) {
	var data [12]byte
	if !header.read(r, data[:1], 0) {
		return 0, 0
	}
	switch data[0] {
	case 0:
		if !header.read(r, data[:8], 12) {
			return 0, 0
		}
		scale = uint64(binary.BigEndian.Uint32(data[:4]))
		ticks = uint64(binary.BigEndian.Uint32(data[4:8]))
		if ticks == math.MaxUint32 {
			ticks = 0
		}
	case 1:
		if !header.read(r, data[:], 20) {
			return 0, 0
		}
		scale = uint64(binary.BigEndian.Uint32(data[:4]))
		ticks = binary.BigEndian.Uint64(data[4:])
		if ticks == math.MaxUint64 {
			ticks = 0
		}
	default:
		return 0, 0
	}
	return ticks, scale
}

// Edit lengths use the movie timescale. In AAC an edit commonly excludes
// the encoder delay from the playable duration.
func editListLength(r io.ReaderAt, box mediaBox) (uint64, bool) {
	var data [8]byte
	if !box.read(r, data[:], 0) {
		return 0, false
	}
	version := data[0]
	count := int64(binary.BigEndian.Uint32(data[4:]))
	var stride int64
	switch version {
	case 0:
		stride = 12
	case 1:
		stride = 20
	default:
		return 0, false
	}
	if count == 0 || count > (box.end-box.start-8)/stride {
		return 0, false
	}
	var length uint64
	for i := range count {
		offset := 8 + i*stride
		var segment uint64
		if version == 0 {
			if !box.read(r, data[:4], offset) {
				return 0, false
			}
			segment = uint64(binary.BigEndian.Uint32(data[:4]))
		} else {
			if !box.read(r, data[:], offset) {
				return 0, false
			}
			segment = binary.BigEndian.Uint64(data[:])
		}
		if segment > math.MaxUint64-length {
			return 0, false
		}
		length += segment
	}
	return length, true
}

func mediaMilliseconds(ticks, scale uint64) int64 {
	if scale == 0 || ticks/scale >= math.MaxInt64/1000 {
		return 0
	}
	return int64(ticks/scale*1000 + ticks%scale*1000/scale)
}
