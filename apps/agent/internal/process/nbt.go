package process

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
)

// Minimal NBT reader: decodes a (gzip-compressed or raw) NBT file into a tree
// of Go values. Compounds become map[string]any, lists become []any, and
// scalars become int64 / float64 / string / []byte. Enough to read Minecraft
// player .dat files without pulling in an external dependency.

const (
	tagEnd       = 0
	tagByte      = 1
	tagShort     = 2
	tagInt       = 3
	tagLong      = 4
	tagFloat     = 5
	tagDouble    = 6
	tagByteArray = 7
	tagString    = 8
	tagList      = 9
	tagCompound  = 10
	tagIntArray  = 11
	tagLongArray = 12
)

type nbtReader struct {
	r io.Reader
}

func (n *nbtReader) read(p []byte) error {
	_, err := io.ReadFull(n.r, p)
	return err
}

func (n *nbtReader) u8() (byte, error) {
	var b [1]byte
	err := n.read(b[:])
	return b[0], err
}

func (n *nbtReader) u16() (uint16, error) {
	var b [2]byte
	if err := n.read(b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b[:]), nil
}

func (n *nbtReader) i32() (int32, error) {
	var b [4]byte
	if err := n.read(b[:]); err != nil {
		return 0, err
	}
	return int32(binary.BigEndian.Uint32(b[:])), nil
}

func (n *nbtReader) i64() (int64, error) {
	var b [8]byte
	if err := n.read(b[:]); err != nil {
		return 0, err
	}
	return int64(binary.BigEndian.Uint64(b[:])), nil
}

func (n *nbtReader) str() (string, error) {
	l, err := n.u16()
	if err != nil {
		return "", err
	}
	if l == 0 {
		return "", nil
	}
	b := make([]byte, l)
	if err := n.read(b); err != nil {
		return "", err
	}
	return string(b), nil
}

func (n *nbtReader) payload(tag byte) (any, error) {
	switch tag {
	case tagByte:
		v, err := n.u8()
		return int64(int8(v)), err
	case tagShort:
		var b [2]byte
		if err := n.read(b[:]); err != nil {
			return nil, err
		}
		return int64(int16(binary.BigEndian.Uint16(b[:]))), nil
	case tagInt:
		v, err := n.i32()
		return int64(v), err
	case tagLong:
		return n.i64()
	case tagFloat:
		v, err := n.i32()
		return float64(math.Float32frombits(uint32(v))), err
	case tagDouble:
		v, err := n.i64()
		return math.Float64frombits(uint64(v)), err
	case tagByteArray:
		l, err := n.i32()
		if err != nil {
			return nil, err
		}
		b := make([]byte, l)
		return b, n.read(b)
	case tagString:
		return n.str()
	case tagList:
		et, err := n.u8()
		if err != nil {
			return nil, err
		}
		l, err := n.i32()
		if err != nil {
			return nil, err
		}
		out := make([]any, 0, l)
		for i := int32(0); i < l; i++ {
			v, err := n.payload(et)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case tagCompound:
		out := map[string]any{}
		for {
			t, err := n.u8()
			if err != nil {
				return nil, err
			}
			if t == tagEnd {
				break
			}
			name, err := n.str()
			if err != nil {
				return nil, err
			}
			v, err := n.payload(t)
			if err != nil {
				return nil, err
			}
			out[name] = v
		}
		return out, nil
	case tagIntArray:
		l, err := n.i32()
		if err != nil {
			return nil, err
		}
		out := make([]any, 0, l)
		for i := int32(0); i < l; i++ {
			v, err := n.i32()
			if err != nil {
				return nil, err
			}
			out = append(out, int64(v))
		}
		return out, nil
	case tagLongArray:
		l, err := n.i32()
		if err != nil {
			return nil, err
		}
		out := make([]any, 0, l)
		for i := int32(0); i < l; i++ {
			v, err := n.i64()
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unknown nbt tag %d", tag)
	}
}

// parseNBTFile reads a (possibly gzip-compressed) NBT file and returns its root
// compound as a map.
func parseNBTFile(path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	br := bufio.NewReader(f)
	var r io.Reader = br
	// Player .dat files are gzip-compressed; sniff the magic and fall back to
	// raw NBT if it isn't.
	if magic, err := br.Peek(2); err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	}

	n := &nbtReader{r: r}
	tag, err := n.u8()
	if err != nil {
		return nil, err
	}
	if tag != tagCompound {
		return nil, fmt.Errorf("nbt root is not a compound (tag %d)", tag)
	}
	if _, err := n.str(); err != nil { // root name (usually empty)
		return nil, err
	}
	root, err := n.payload(tagCompound)
	if err != nil {
		return nil, err
	}
	m, _ := root.(map[string]any)
	return m, nil
}
