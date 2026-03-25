package gitnaturalapi

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
)

const (
	ObjectTypeCommit   = 1
	ObjectTypeTree     = 2
	ObjectTypeBlob     = 3
	ObjectTypeTag      = 4
	ObjectTypeOfsDelta = 6
	ObjectTypeRefDelta = 7
)

type ParsedObject struct {
	Type   int
	Size   int
	Data   []byte
	Offset int
	Hash   string
}

type PackfileResult struct {
	Version int
	Count   int
	Objects map[string]*ParsedObject
}

func ParsePackfile(data []byte) (*PackfileResult, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("packfile too short")
	}

	header := string(data[0:4])
	if header != "PACK" {
		return nil, fmt.Errorf("invalid packfile header: %s", header)
	}

	version := int(binary.BigEndian.Uint32(data[4:8]))
	if version != 2 {
		return nil, fmt.Errorf("unsupported packfile version: %d", version)
	}

	count := int(binary.BigEndian.Uint32(data[8:12]))

	objects := make(map[string]*ParsedObject)
	pos := 12

	for i := 0; i < count; i++ {
		obj, newPos, err := parsePackObject(data, pos, objects)
		if err != nil {
			return nil, fmt.Errorf("error parsing object %d/%d: %w", i+1, count, err)
		}
		objects[obj.Hash] = obj
		pos = newPos
	}

	return &PackfileResult{
		Version: version,
		Count:   count,
		Objects: objects,
	}, nil
}

func parsePackObject(data []byte, startPos int, objects map[string]*ParsedObject) (*ParsedObject, int, error) {
	pos := startPos
	offset := startPos

	b := data[pos]
	pos++
	objType := int((b >> 4) & 0x07)
	size := int(b & 0x0f)
	shift := 4

	for b&0x80 != 0 {
		b = data[pos]
		pos++
		size |= int(b&0x7f) << shift
		shift += 7
	}

	var objData []byte
	var err error

	switch objType {
	case ObjectTypeOfsDelta:
		var actualType int
		objData, pos, actualType, err = parseOfsDelta(data, pos, offset, objects)
		if err != nil {
			return nil, 0, err
		}
		objType = actualType
	case ObjectTypeRefDelta:
		var actualType int
		objData, pos, actualType, err = parseRefDelta(data, pos, objects)
		if err != nil {
			return nil, 0, err
		}
		objType = actualType
	case ObjectTypeCommit, ObjectTypeTree, ObjectTypeBlob, ObjectTypeTag:
		objData, pos, err = zlibDecompress(data, pos)
		if err != nil {
			return nil, 0, err
		}
	default:
		return nil, 0, fmt.Errorf("unknown object type: %d", objType)
	}

	hash, err := computeObjectHash(objType, objData)
	if err != nil {
		return nil, 0, err
	}

	return &ParsedObject{
		Type:   objType,
		Size:   size,
		Data:   objData,
		Offset: offset,
		Hash:   hash,
	}, pos, nil
}

func parseOfsDelta(data []byte, pos int, currentOffset int, objects map[string]*ParsedObject) ([]byte, int, int, error) {
	b := data[pos]
	pos++
	offset := int(b & 0x7f)

	for b&0x80 != 0 {
		offset++
		offset <<= 7
		b = data[pos]
		pos++
		offset += int(b & 0x7f)
	}

	baseOffset := currentOffset - offset
	baseObject, _, err := parsePackObject(data, baseOffset, objects)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to parse base object at offset %d: %w", baseOffset, err)
	}

	delta, newPos, err := zlibDecompress(data, pos)
	if err != nil {
		return nil, 0, 0, err
	}

	fullObj, err := applyDelta(delta, baseObject.Data)
	if err != nil {
		return nil, 0, 0, err
	}

	return fullObj, newPos, baseObject.Type, nil
}

func parseRefDelta(data []byte, pos int, objects map[string]*ParsedObject) ([]byte, int, int, error) {
	baseName := hex.EncodeToString(data[pos : pos+20])
	pos += 20

	delta, newPos, err := zlibDecompress(data, pos)
	if err != nil {
		return nil, 0, 0, err
	}

	baseObject, ok := objects[baseName]
	if !ok {
		return nil, 0, 0, fmt.Errorf("base object not found with name %s", baseName)
	}

	fullObj, err := applyDelta(delta, baseObject.Data)
	if err != nil {
		return nil, 0, 0, err
	}

	return fullObj, newPos, baseObject.Type, nil
}

func computeObjectHash(objType int, data []byte) (string, error) {
	var typeStr string
	switch objType {
	case ObjectTypeCommit:
		typeStr = "commit"
	case ObjectTypeTree:
		typeStr = "tree"
	case ObjectTypeBlob:
		typeStr = "blob"
	case ObjectTypeTag:
		typeStr = "tag"
	default:
		return "", fmt.Errorf("unknown type when computing object hash: %d", objType)
	}

	header := fmt.Sprintf("%s %d\x00", typeStr, len(data))
	h := sha1.New()
	h.Write([]byte(header))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func applyDelta(delta []byte, base []byte) ([]byte, error) {
	pos := 0

	_, bytesRead := readVariableInt(delta, pos)
	pos += bytesRead

	resultSize, bytesRead := readVariableInt(delta, pos)
	pos += bytesRead

	result := make([]byte, resultSize)
	resultOffset := 0

	for pos < len(delta) {
		cmd := delta[pos]
		pos++

		if cmd&0x80 != 0 {
			var copyOffset, copySize int

			if cmd&0x01 != 0 {
				copyOffset = int(delta[pos])
				pos++
			}
			if cmd&0x02 != 0 {
				copyOffset |= int(delta[pos]) << 8
				pos++
			}
			if cmd&0x04 != 0 {
				copyOffset |= int(delta[pos]) << 16
				pos++
			}
			if cmd&0x08 != 0 {
				copyOffset |= int(delta[pos]) << 24
				pos++
			}

			if cmd&0x10 != 0 {
				copySize = int(delta[pos])
				pos++
			}
			if cmd&0x20 != 0 {
				copySize |= int(delta[pos]) << 8
				pos++
			}
			if cmd&0x40 != 0 {
				copySize |= int(delta[pos]) << 16
				pos++
			}

			if copySize == 0 {
				copySize = 0x10000
			}

			copy(result[resultOffset:], base[copyOffset:copyOffset+copySize])
			resultOffset += copySize
		} else if cmd > 0 {
			copy(result[resultOffset:], delta[pos:pos+int(cmd)])
			pos += int(cmd)
			resultOffset += int(cmd)
		} else {
			return nil, fmt.Errorf("invalid delta command")
		}
	}

	return result, nil
}

func zlibDecompress(data []byte, pos int) ([]byte, int, error) {
	br := bytes.NewReader(data[pos:])
	r, err := zlib.NewReader(br)
	if err != nil {
		return nil, 0, fmt.Errorf("zlib init error: %w", err)
	}

	decompressed, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		return nil, 0, fmt.Errorf("zlib decompress error: %w", err)
	}

	newPos := len(data) - br.Len()
	return decompressed, newPos, nil
}

func readVariableInt(data []byte, pos int) (int, int) {
	value := 0
	shift := 0
	bytesRead := 0

	for {
		b := data[pos]
		pos++
		bytesRead++
		value |= int(b&0x7f) << shift
		shift += 7
		if b&0x80 == 0 {
			break
		}
	}

	return value, bytesRead
}
