package marshal

import (
	"errors"
	"fmt"
	"strconv"
)

type MarshalledObject struct {
	MajorVersion byte
	MinorVersion byte

	data        []byte
	symbolCache *[]string
	objectCache *[]*MarshalledObject
	size        int
}

type marshalledObjectType byte

var TypeMismatch = errors.New("gorails/marshal: an attempt to implicitly typecast a marshalled object")
var IncompleteData = errors.New("gorails/marshal: incomplete data")

type UnsupportedType struct {
	type_char byte
}

func (e UnsupportedType) Error() string {
	return fmt.Sprintf("gorails/marshal: An unsupported type %q is nested within a map or array", e.type_char)
}

const (
	TYPE_UNKNOWN            marshalledObjectType = 0
	TYPE_NIL                marshalledObjectType = 1
	TYPE_BOOL               marshalledObjectType = 2
	TYPE_INTEGER            marshalledObjectType = 3
	TYPE_FLOAT              marshalledObjectType = 4
	TYPE_STRING             marshalledObjectType = 5
	TYPE_ARRAY              marshalledObjectType = 6
	TYPE_MAP                marshalledObjectType = 7
	TYPE_USER_DEFINED       marshalledObjectType = 8
	TYPE_INSTANCE_VARIABLES marshalledObjectType = 9
	TYPE_OBJECT_INSTANCE    marshalledObjectType = 10
)

func newMarshalledObject(major_version, minor_version byte, data []byte, symbolCache *[]string, objectCache *[]*MarshalledObject) *MarshalledObject {
	return newMarshalledObjectWithSize(major_version, minor_version, data, len(data), symbolCache, objectCache)
}

func newMarshalledObjectWithSize(major_version, minor_version byte, data []byte, size int, symbolCache *[]string, objectCache *[]*MarshalledObject) *MarshalledObject {
	return &(MarshalledObject{major_version, minor_version, data, symbolCache, objectCache, size})
}

func CreateMarshalledObject(serialized_data []byte) *MarshalledObject {
	symbolCache := []string{}
	objectCache := []*MarshalledObject{}
	return newMarshalledObject(serialized_data[0], serialized_data[1], serialized_data[2:], &symbolCache, &objectCache)
}

func (obj *MarshalledObject) GetType() marshalledObjectType {
	if len(obj.data) == 0 {
		return TYPE_UNKNOWN
	}

	if ref := obj.resolveObjectLink(); ref != nil {
		return ref.GetType()
	}

	switch obj.data[0] {
	case '0':
		return TYPE_NIL
	case 'T', 'F':
		return TYPE_BOOL
	case 'i':
		return TYPE_INTEGER
	case 'f':
		return TYPE_FLOAT
	case ':', ';', '"':
		return TYPE_STRING
	case 'u':
		return TYPE_USER_DEFINED
	case 'I':
		if len(obj.data) > 1 && obj.data[1] == '"' {
			return TYPE_STRING
		} else {
			return TYPE_INSTANCE_VARIABLES
		}
	case 'o':
		return TYPE_OBJECT_INSTANCE
	case '[':
		return TYPE_ARRAY
	case '{':
		return TYPE_MAP
	}

	return TYPE_UNKNOWN
}

func (obj *MarshalledObject) GetAsBool() (value bool, err error) {
	err = assertType(obj, TYPE_BOOL)
	if err != nil {
		return
	}

	value, _ = parseBool(obj.data)

	return
}

func (obj *MarshalledObject) GetAsInteger() (value int64, err error) {
	err = assertType(obj, TYPE_INTEGER)
	if err != nil {
		return
	}

	value, _ = parseInt(obj.data[1:])

	return
}

func (obj *MarshalledObject) GetAsFloat() (value float64, err error) {
	err = assertType(obj, TYPE_FLOAT)
	if err != nil {
		return
	}

	str, _ := parseString(obj.data[1:])
	value, err = strconv.ParseFloat(str, 64)

	return
}

func (obj *MarshalledObject) GetAsString() (value string, err error) {
	if ref := obj.resolveObjectLink(); ref != nil {
		return ref.GetAsString()
	}

	err = assertType(obj, TYPE_STRING)
	if err != nil {
		return
	}

	obj.cacheObject(obj)

	var cache []string
	if obj.data[0] == ':' {
		value, _ = parseString(obj.data[1:])
		obj.cacheSymbols(value)
	} else if obj.data[0] == ';' {
		ref_index, _ := parseInt(obj.data[1:])
		cache := *(obj.symbolCache)
		value = cache[ref_index]
	} else if obj.data[0] == '"' {
		value, _ = parseString(obj.data[1:])
	} else {
		value, _, cache = parseStringWithEncoding(obj.data[2:])
		obj.cacheSymbols(cache...)
	}

	return
}

func (obj *MarshalledObject) GetAsArray() (value []*MarshalledObject, err error) {
	if ref := obj.resolveObjectLink(); ref != nil {
		return ref.GetAsArray()
	}

	err = assertType(obj, TYPE_ARRAY)
	if err != nil {
		return
	}

	obj.cacheObject(obj)

	array_size, offset := parseInt(obj.data[1:])
	offset += 1

	value = make([]*MarshalledObject, array_size)
	for i := int64(0); i < array_size; i++ {
		value_size, err := newMarshalledObjectWithSize(
			obj.MajorVersion,
			obj.MinorVersion,
			obj.data[offset:],
			0,
			obj.symbolCache,
			obj.objectCache,
		).getSize()

		if err != nil {
			return nil, err
		}

		value[i] = newMarshalledObject(
			obj.MajorVersion,
			obj.MinorVersion,
			obj.data[offset:offset+value_size],
			obj.symbolCache,
			obj.objectCache,
		)
		obj.cacheObject(value[i])
		offset += value_size
	}

	obj.size = offset

	return
}

func (obj *MarshalledObject) GetAsMap() (value map[string]*MarshalledObject, err error) {
	if ref := obj.resolveObjectLink(); ref != nil {
		return ref.GetAsMap()
	}

	err = assertType(obj, TYPE_MAP)
	if err != nil {
		return
	}

	obj.cacheObject(obj)

	pairs, err := obj.getMaplike(true)
	if err != nil {
		return
	}

	value = make(map[string]*MarshalledObject, len(pairs))
	for k, v := range pairs {
		value[k.ToString()] = v
	}
	return
}

// type-char (optional), integer (number of pairs), key, value, key, value, ...
func (obj *MarshalledObject) getMaplike(hasType bool) (value map[*MarshalledObject]*MarshalledObject, err error) {
	var map_size int64
	var offset int
	if hasType {
		map_size, offset = parseInt(obj.data[1:])
		offset += 1
	} else {
		map_size, offset = parseInt(obj.data)
	}

	value = make(map[*MarshalledObject]*MarshalledObject, map_size)
	for i := int64(0); i < map_size; i++ {
		k := newMarshalledObject(
			obj.MajorVersion,
			obj.MinorVersion,
			obj.data[offset:],
			obj.symbolCache,
			obj.objectCache,
		)
		obj.cacheObject(k)
		key_size, err := k.getSize()
		if err != nil {
			return nil, err
		}
		offset += key_size

		value_size, err := newMarshalledObjectWithSize(
			obj.MajorVersion,
			obj.MinorVersion,
			obj.data[offset:],
			0,
			obj.symbolCache,
			obj.objectCache,
		).getSize()

		if err != nil {
			return nil, err
		}

		v := newMarshalledObject(
			obj.MajorVersion,
			obj.MinorVersion,
			obj.data[offset:offset+value_size],
			obj.symbolCache,
			obj.objectCache,
		)
		obj.cacheObject(v)
		value[k] = v

		offset += value_size
	}

	obj.size = offset

	return
}

func assertType(obj *MarshalledObject, expected_type marshalledObjectType) (err error) {
	if obj.GetType() != expected_type {
		err = TypeMismatch
	}

	return
}

func (obj *MarshalledObject) getSize() (size int, err error) {
	header_size, data_size := 0, 0

	if len(obj.data) > 0 && obj.data[0] == '@' {
		header_size = 1
		_, data_size = parseInt(obj.data[1:])
		return header_size + data_size, nil
	}

	switch obj.GetType() {
	case TYPE_NIL, TYPE_BOOL:
		header_size = 0
		data_size = 1
	case TYPE_INTEGER:
		header_size = 1
		_, data_size = parseInt(obj.data[header_size:])
	case TYPE_STRING, TYPE_FLOAT:
		header_size = 1

		if obj.data[0] == ';' {
			_, data_size = parseInt(obj.data[header_size:])
		} else {
			var cache []string

			if obj.data[0] == 'I' {
				header_size += 1
				_, data_size, cache = parseStringWithEncoding(obj.data[header_size:])
				obj.cacheSymbols(cache...)
			} else {
				var symbol string
				symbol, data_size = parseString(obj.data[header_size:])
				obj.cacheSymbols(symbol)
			}
		}

	case TYPE_USER_DEFINED:
		class_name := newMarshalledObject(
			obj.MajorVersion,
			obj.MinorVersion,
			obj.data[1:],
			obj.symbolCache,
			obj.objectCache,
		)
		class_name_len, err := class_name.getSize()
		if err != nil {
			return 0, err
		}
		byte_sequence := obj.data[1+class_name_len:]
		sequence_length, int_length := parseInt(byte_sequence)

		header_size = 1
		data_size = class_name_len + int_length + int(sequence_length)

	case TYPE_INSTANCE_VARIABLES, TYPE_OBJECT_INSTANCE:
		main_obj := newMarshalledObject(
			obj.MajorVersion,
			obj.MinorVersion,
			obj.data[1:],
			obj.symbolCache,
			obj.objectCache,
		)
		main_obj_len, err := main_obj.getSize()
		if err != nil {
			return 0, err
		}
		ivars := newMarshalledObject(
			obj.MajorVersion,
			obj.MinorVersion,
			obj.data[1+main_obj_len:],
			obj.symbolCache,
			obj.objectCache,
		)
		_, err = ivars.getMaplike(false)
		if err != nil {
			return 0, err
		}
		header_size = 1
		data_size = main_obj_len + ivars.size
	case TYPE_ARRAY:
		if obj.size == 0 {
			_, err := obj.GetAsArray()
			if err != nil {
				return 0, err
			} else {
				return obj.size, nil
			}
		}
	case TYPE_MAP:
		if obj.size == 0 {
			_, err := obj.GetAsMap()
			if err != nil {
				return 0, err
			} else {
				return obj.size, nil
			}
		}
	case TYPE_UNKNOWN:
		return 0, UnsupportedType{obj.data[0]}
	}

	return header_size + data_size, nil
}

func (obj *MarshalledObject) cacheSymbols(symbols ...string) {
	if len(symbols) == 0 {
		return
	}

	cache := *(obj.symbolCache)

	known := make(map[string]struct{})
	for _, symbol := range cache {
		known[symbol] = struct{}{}
	}

	for _, symbol := range symbols {
		_, exists := known[symbol]

		if !exists {
			cache = append(cache, symbol)
		}
	}

	*(obj.symbolCache) = cache
}

func (obj *MarshalledObject) cacheObject(object *MarshalledObject) {
	if len(object.data) > 0 && (object.data[0] == '@' || object.data[0] == ':' || object.data[0] == ';') {
		return
	}
	if t := obj.GetType(); !(t == TYPE_STRING || t == TYPE_ARRAY || t == TYPE_MAP) {
		return
	}

	cache := *(obj.objectCache)

	for _, o := range cache {
		if object == o {
			return
		}
	}
	cache = append(cache, object)

	*(obj.objectCache) = cache
}

func (obj *MarshalledObject) ToString() (str string) {
	switch obj.GetType() {
	case TYPE_NIL:
		str = "<nil>"
	case TYPE_BOOL:
		v, _ := obj.GetAsBool()

		if v {
			str = "true"
		} else {
			str = "false"
		}
	case TYPE_INTEGER:
		v, _ := obj.GetAsInteger()
		str = strconv.FormatInt(v, 10)
	case TYPE_STRING:
		str, _ = obj.GetAsString()
	case TYPE_FLOAT:
		v, _ := obj.GetAsFloat()
		str = strconv.FormatFloat(v, 'f', -1, 64)
	}

	return
}

func (obj *MarshalledObject) resolveObjectLink() *MarshalledObject {
	if len(obj.data) > 0 && obj.data[0] == '@' {
		idx, _ := parseInt(obj.data[1:])
		cache := *(obj.objectCache)

		if int(idx) < len(cache) {
			return cache[idx]
		}
	}

	return nil
}

func parseBool(data []byte) (bool, int) {
	return data[0] == 'T', 1
}

func parseInt(data []byte) (int64, int) {
	if data[0] > 0x05 && data[0] < 0xfb {
		value := int64(data[0])

		if value > 0x7f {
			return -(0xff ^ value + 1) + 5, 1
		} else {
			return value - 5, 1
		}
	} else if data[0] <= 0x05 {
		value := int64(0)
		i := data[0]

		for ; i > 0; i-- {
			value = value<<8 + int64(data[i])
		}

		return value, int(data[0] + 1)
	} else {
		value := int64(0)
		i := 0xff - data[0] + 1

		for ; i > 0; i-- {
			value = value<<8 + (0xff - int64(data[i]))
		}

		return -(value + 1), int(0xff - data[0] + 2)
	}
}

func parseString(data []byte) (string, int) {
	length, header_size := parseInt(data)
	size := int(length) + header_size

	return string(data[header_size:size]), size
}

func parseStringWithEncoding(data []byte) (string, int, []string) {
	cache := make([]string, 0)
	value, size := parseString(data)

	if len(data) > size+1 && (data[size+1] == ':' || data[size+1] == ';') {
		if data[size+1] == ';' {
			_, enc_size := parseInt(data[size+2:])
			size += enc_size + 1
		} else {
			enc_symbol, enc_size := parseString(data[size+2:])
			size += enc_size + 1
			cache = append(cache, enc_symbol)
		}

		if data[size+1] == '"' {
			encoding, enc_name_size := parseString(data[size+2:])
			_ = encoding
			size += enc_name_size + 1
		} else {
			_, enc_name_size := parseBool(data[size+1:])
			size += enc_name_size
		}

		size += 1
	}

	return value, size, cache
}
