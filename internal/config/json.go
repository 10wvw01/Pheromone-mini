package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"triggerEngine/internal/contract"
)

const (
	MaxFileBytes   = 1 << 20
	maxStringBytes = 4096
)

// functionRef accepts a full identity or a legacy unqualified function name.
type functionRef contract.FunctionIdentity

func (ref *functionRef) UnmarshalJSON(data []byte) error {
	if data[0] == '"' {
		var name string
		if err := json.Unmarshal(data, &name); err != nil {
			return err
		}
		*ref = functionRef{FuncName: name}
		return nil
	}
	var id contract.FunctionIdentity
	if err := json.Unmarshal(data, &id); err != nil {
		return err
	}
	if !safeName(id.SOName) || blank(id.FuncName) {
		return fmt.Errorf("function reference object requires valid so_name and func_name")
	}
	*ref = functionRef(id)
	return nil
}

// decode 按 V0.6 附录 B §B.5 先校验 JSON 的字段名、重复字段、类型和边界，再解码到目标结构。
func decode(data []byte, dst any) error {
	if len(data) > MaxFileBytes || !utf8.Valid(data) {
		return fmt.Errorf("app.json exceeds the size limit or contains invalid UTF-8")
	}
	// 保留数字原文，使整数边界校验不受浮点舍入影响。
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := checkValue(dec, reflect.TypeOf(dst).Elem(), "$"); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("expected a single JSON object")
	}
	return json.Unmarshal(data, dst)
}

// checkValue 按目标类型检查单个 JSON 值，递归校验对象字段和数组元素。
func checkValue(dec *json.Decoder, typ reflect.Type, path string) error {
	token, err := dec.Token()
	if err != nil {
		return fmt.Errorf("%s: invalid JSON at byte %d", path, dec.InputOffset())
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == reflect.TypeFor[functionRef]() {
		if name, ok := token.(string); ok && len(name) <= maxStringBytes {
			return nil
		}
	}
	// 使用配置声明的类型检查输入，避免隐式类型转换。
	switch typ.Kind() {
	case reflect.Struct:
		if token == json.Delim('{') {
			return checkObject(dec, typ, path)
		}
	case reflect.Slice:
		if token == json.Delim('[') {
			for i := 0; dec.More(); i++ {
				if err := checkValue(dec, typ.Elem(), path+"["+strconv.Itoa(i)+"]"); err != nil {
					return err
				}
			}
			return closeJSON(dec, ']', path)
		}
	case reflect.String:
		if s, ok := token.(string); ok && len(s) <= maxStringBytes {
			return nil
		}
	case reflect.Bool:
		if _, ok := token.(bool); ok {
			return nil
		}
	case reflect.Float64:
		if n, ok := token.(json.Number); ok {
			if _, err := strconv.ParseFloat(n.String(), 64); err == nil {
				return nil
			}
		}
	case reflect.Uint8, reflect.Uint32, reflect.Uint64:
		if n, ok := token.(json.Number); ok {
			if _, err := strconv.ParseUint(n.String(), 10, typ.Bits()); err == nil {
				return nil
			}
		}
	}
	return fmt.Errorf("%s: invalid type, length or numeric range", path)
}

// checkObject 按结构体的 JSON 标签检查字段名，拒绝未知字段和重复字段。
func checkObject(dec *json.Decoder, typ reflect.Type, path string) error {
	// 根据标签建立区分大小写的字段名索引。
	fields := make(map[string]reflect.Type, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fields[strings.Split(field.Tag.Get("json"), ",")[0]] = field.Type
	}
	seen := make(map[string]bool, typ.NumField())
	for dec.More() {
		token, err := dec.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return fmt.Errorf("%s: invalid field at byte %d", path, dec.InputOffset())
		}
		field, ok := fields[name]
		if !ok {
			return fmt.Errorf("%s: unknown field at byte %d", path, dec.InputOffset())
		}
		next := path + "." + name
		if seen[name] {
			return fmt.Errorf("%s: duplicate field", next)
		}
		seen[name] = true
		if err := checkValue(dec, field, next); err != nil {
			return err
		}
	}
	return closeJSON(dec, '}', path)
}

// closeJSON 读取并校验对象或数组的结束符。
func closeJSON(dec *json.Decoder, want json.Delim, path string) error {
	if token, err := dec.Token(); err != nil || token != want {
		return fmt.Errorf("%s: invalid JSON at byte %d", path, dec.InputOffset())
	}
	return nil
}
