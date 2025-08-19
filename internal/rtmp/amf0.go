package rtmp

import (
	"encoding/binary"
	"fmt"
	"math"
)

const (
	AMF0_NUMBER       = 0x00
	AMF0_BOOLEAN      = 0x01
	AMF0_STRING       = 0x02
	AMF0_OBJECT       = 0x03
	AMF0_NULL         = 0x05
	AMF0_UNDEFINED    = 0x06
	AMF0_ECMA_ARRAY   = 0x08
	AMF0_OBJECT_END   = 0x09
	AMF0_STRICT_ARRAY = 0x0A
	AMF0_DATE         = 0x0B
	AMF0_LONG_STRING  = 0x0C
)

// AMF0Encoder encodes AMF0 data
type AMF0Encoder struct {
	data []byte
}

// NewAMF0Encoder creates a new AMF0 encoder
func NewAMF0Encoder() *AMF0Encoder {
	return &AMF0Encoder{
		data: make([]byte, 0),
	}
}

// EncodeNumber encodes a number
func (e *AMF0Encoder) EncodeNumber(val float64) {
	e.data = append(e.data, AMF0_NUMBER)
	bits := math.Float64bits(val)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, bits)
	e.data = append(e.data, buf...)
}

// EncodeBoolean encodes a boolean
func (e *AMF0Encoder) EncodeBoolean(val bool) {
	e.data = append(e.data, AMF0_BOOLEAN)
	if val {
		e.data = append(e.data, 0x01)
	} else {
		e.data = append(e.data, 0x00)
	}
}

// EncodeString encodes a string
func (e *AMF0Encoder) EncodeString(val string) {
	if len(val) > 0xFFFF {
		e.data = append(e.data, AMF0_LONG_STRING)
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, uint32(len(val)))
		e.data = append(e.data, buf...)
	} else {
		e.data = append(e.data, AMF0_STRING)
		buf := make([]byte, 2)
		binary.BigEndian.PutUint16(buf, uint16(len(val)))
		e.data = append(e.data, buf...)
	}
	e.data = append(e.data, []byte(val)...)
}

// EncodeNull encodes null
func (e *AMF0Encoder) EncodeNull() {
	e.data = append(e.data, AMF0_NULL)
}

// EncodeUndefined encodes undefined
func (e *AMF0Encoder) EncodeUndefined() {
	e.data = append(e.data, AMF0_UNDEFINED)
}

// AMF0Property represents a key-value pair for ordered encoding
type AMF0Property struct {
	Key   string
	Value interface{}
}

// EncodeECMAArray encodes an ECMA array from a map (unordered)
func (e *AMF0Encoder) EncodeECMAArray(val map[string]interface{}) {
	e.data = append(e.data, AMF0_ECMA_ARRAY)
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, uint32(len(val)))
	e.data = append(e.data, buf...)
	for k, v := range val {
		e.encodeString(k)
		e.EncodeValue(v)
	}
	e.data = append(e.data, 0x00, 0x00, AMF0_OBJECT_END)
}

// EncodeECMAArrayOrdered encodes an ECMA array from a slice of properties (ordered)
func (e *AMF0Encoder) EncodeECMAArrayOrdered(val []AMF0Property) {
	e.data = append(e.data, AMF0_ECMA_ARRAY)
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, uint32(len(val)))
	e.data = append(e.data, buf...)
	for _, p := range val {
		e.encodeString(p.Key)
		e.EncodeValue(p.Value)
	}
	e.data = append(e.data, 0x00, 0x00, AMF0_OBJECT_END)
}

// EncodeValue encodes a value based on its type
func (e *AMF0Encoder) EncodeValue(val interface{}) {
	switch v := val.(type) {
	case float64:
		e.EncodeNumber(v)
	case bool:
		e.EncodeBoolean(v)
	case string:
		e.EncodeString(v)
	case nil:
		e.EncodeNull()
	case map[string]interface{}:
		e.EncodeECMAArray(v)
	case []AMF0Property:
		e.EncodeECMAArrayOrdered(v)
	default:
		e.EncodeUndefined()
	}
}

// encodeString encodes a string without type marker
func (e *AMF0Encoder) encodeString(val string) {
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, uint16(len(val)))
	e.data = append(e.data, buf...)
	e.data = append(e.data, []byte(val)...)
}

// Bytes returns the encoded bytes
func (e *AMF0Encoder) Bytes() []byte {
	return e.data
}

// AMF0Decoder decodes AMF0 data
type AMF0Decoder struct {
	data []byte
	pos  int
}

// NewAMF0Decoder creates a new AMF0 decoder
func NewAMF0Decoder(data []byte) *AMF0Decoder {
	return &AMF0Decoder{
		data: data,
		pos:  0,
	}
}

// DecodeNumber decodes a number
func (d *AMF0Decoder) DecodeNumber() (float64, error) {
	if d.pos+9 > len(d.data) {
		return 0, fmt.Errorf("not enough data to decode number")
	}
	if d.data[d.pos] != AMF0_NUMBER {
		return 0, fmt.Errorf("expected number marker, got %d", d.data[d.pos])
	}
	d.pos++
	bits := binary.BigEndian.Uint64(d.data[d.pos:])
	d.pos += 8
	return math.Float64frombits(bits), nil
}

// DecodeBoolean decodes a boolean
func (d *AMF0Decoder) DecodeBoolean() (bool, error) {
	if d.pos+2 > len(d.data) {
		return false, fmt.Errorf("not enough data to decode boolean")
	}
	if d.data[d.pos] != AMF0_BOOLEAN {
		return false, fmt.Errorf("expected boolean marker, got %d", d.data[d.pos])
	}
	d.pos++
	val := d.data[d.pos] != 0
	d.pos++
	return val, nil
}

// DecodeString decodes a string
func (d *AMF0Decoder) DecodeString() (string, error) {
	if d.pos+1 > len(d.data) {
		return "", fmt.Errorf("not enough data to decode string")
	}

	marker := d.data[d.pos]
	d.pos++

	var length int
	switch marker {
	case AMF0_STRING:
		if d.pos+2 > len(d.data) {
			return "", fmt.Errorf("not enough data to decode string length")
		}
		length = int(binary.BigEndian.Uint16(d.data[d.pos:]))
		d.pos += 2
	case AMF0_LONG_STRING:
		if d.pos+4 > len(d.data) {
			return "", fmt.Errorf("not enough data to decode long string length")
		}
		length = int(binary.BigEndian.Uint32(d.data[d.pos:]))
		d.pos += 4
	default:
		return "", fmt.Errorf("expected string marker, got %d", marker)
	}

	if d.pos+length > len(d.data) {
		return "", fmt.Errorf("not enough data to decode string content")
	}

	val := string(d.data[d.pos : d.pos+length])
	d.pos += length
	return val, nil
}

// DecodeNull decodes null
func (d *AMF0Decoder) DecodeNull() error {
	if d.pos+1 > len(d.data) {
		return fmt.Errorf("not enough data to decode null")
	}
	if d.data[d.pos] != AMF0_NULL {
		return fmt.Errorf("expected null marker, got %d", d.data[d.pos])
	}
	d.pos++
	return nil
}

// DecodeUndefined decodes undefined
func (d *AMF0Decoder) DecodeUndefined() error {
	if d.pos+1 > len(d.data) {
		return fmt.Errorf("not enough data to decode undefined")
	}
	if d.data[d.pos] != AMF0_UNDEFINED {
		return fmt.Errorf("expected undefined marker, got %d", d.data[d.pos])
	}
	d.pos++
	return nil
}

// DecodeECMAArray decodes an ECMA array
func (d *AMF0Decoder) DecodeECMAArray() (map[string]interface{}, error) {
	if d.pos+5 > len(d.data) {
		return nil, fmt.Errorf("not enough data to decode ECMA array")
	}
	if d.data[d.pos] != AMF0_ECMA_ARRAY {
		return nil, fmt.Errorf("expected ECMA array marker, got %d", d.data[d.pos])
	}
	d.pos++

	count := binary.BigEndian.Uint32(d.data[d.pos:])
	d.pos += 4

	result := make(map[string]interface{})
	for i := uint32(0); i < count; i++ {
		if d.pos+2 > len(d.data) {
			return nil, fmt.Errorf("not enough data to decode ECMA array key length")
		}

		keyLen := binary.BigEndian.Uint16(d.data[d.pos:])
		d.pos += 2

		if d.pos+int(keyLen) > len(d.data) {
			return nil, fmt.Errorf("not enough data to decode ECMA array key")
		}

		key := string(d.data[d.pos : d.pos+int(keyLen)])
		d.pos += int(keyLen)

		val, err := d.DecodeValue()
		if err != nil {
			return nil, err
		}

		result[key] = val
	}

	// Skip object end marker
	if d.pos+3 > len(d.data) {
		return nil, fmt.Errorf("not enough data to decode ECMA array end marker")
	}
	d.pos += 3

	return result, nil
}

// DecodeValue decodes a value based on its type marker
func (d *AMF0Decoder) DecodeValue() (interface{}, error) {
	if d.pos >= len(d.data) {
		return nil, fmt.Errorf("no more data to decode")
	}

	marker := d.data[d.pos]
	switch marker {
	case AMF0_NUMBER:
		return d.DecodeNumber()
	case AMF0_BOOLEAN:
		return d.DecodeBoolean()
	case AMF0_STRING:
		fallthrough
	case AMF0_LONG_STRING:
		return d.DecodeString()
	case AMF0_NULL:
		return nil, d.DecodeNull()
	case AMF0_UNDEFINED:
		return nil, d.DecodeUndefined()
	case AMF0_ECMA_ARRAY:
		return d.DecodeECMAArray()
	default:
		return nil, fmt.Errorf("unsupported AMF0 type marker: %d", marker)
	}
}
