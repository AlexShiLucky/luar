package luar

// Those functions are meant to be registered in Lua to manipulate proxies.

import (
	"reflect"

	"github.com/aarzilli/golua/lua"
)

// ArrayToTable defines 'luar.array2table' when 'Init' is called.
func ArrayToTable(L *lua.State) int {
	return CopyArrayToTable(L, reflect.ValueOf(mustUnwrapProxy(L, 1)))
}

// TODO: What is this for?
func MakeChannel(L *lua.State) int {
	ch := make(chan interface{})
	makeValueProxy(L, reflect.ValueOf(ch), cChannelMeta)
	return 1
}

// MakeMap defines 'luar.map' when 'Init' is called.
func MakeMap(L *lua.State) int {
	m := reflect.MakeMap(tmap)
	makeValueProxy(L, m, cMapMeta)
	return 1
}

// MakeSlice defines 'luar.slice' when 'Init' is called.
func MakeSlice(L *lua.State) int {
	n := L.OptInteger(1, 0)
	s := reflect.MakeSlice(tslice, n, n+1)
	makeValueProxy(L, s, cSliceMeta)
	return 1
}

// MapToTable defines 'luar.map2table' when 'Init' is called.
func MapToTable(L *lua.State) int {
	return CopyMapToTable(L, reflect.ValueOf(mustUnwrapProxy(L, 1)))
}

// ProxyRaw defines 'luar.raw' when 'Init' is called.
func ProxyRaw(L *lua.State) int {
	v := mustUnwrapProxy(L, 1)
	val := reflect.ValueOf(v)
	tp := predeclaredScalarType(val.Type())
	if tp != nil {
		val = val.Convert(tp)
		GoToLua(L, nil, val, false)
	} else {
		L.PushNil()
	}
	return 1
}

// ProxyType re-defines 'type' when 'Init' is called.
//
// It behaves like Lua's "type" except for proxies for which it returns
// 'table<TYPE>', 'string<TYPE>' or 'number<TYPE>' with TYPE being the go type.
func ProxyType(L *lua.State) int {
	if !isValueProxy(L, 1) {
		L.PushString(L.LTypename(1))
		return 1
	}
	val := mustUnwrapProxy(L, 1)
	if val == nil {
		L.PushNil()
		return 1
	}

	v := reflect.ValueOf(val)
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.Struct:
		L.PushString("table<" + v.Type().String() + ">")
		return 1
	case reflect.String:
		L.PushString("string<" + v.Type().String() + ">")
		return 1
	}

	if numericKind(v) != reflect.Invalid {
		L.PushString("number<" + v.Type().String() + ">")
		return 1
	}

	L.PushString("userdata<" + v.Type().String() + ">")
	return 1
}

// SliceAppend defines 'luar.append' when 'Init' is called.
func SliceAppend(L *lua.State) int {
	slice, _ := valueOfProxy(L, 1)
	val := reflect.ValueOf(LuaToGo(L, nil, 2))
	newslice := reflect.Append(slice, val)
	makeValueProxy(L, newslice, cSliceMeta)
	return 1
}

// SliceSub defines 'luar.sub' when 'Init' is called.
func SliceSub(L *lua.State) int {
	slice, _ := valueOfProxy(L, 1)
	i1, i2 := L.ToInteger(2), L.ToInteger(3)
	newslice := slice.Slice(i1-1, i2)
	makeValueProxy(L, newslice, cSliceMeta)
	return 1
}

// SliceToTable defines 'luar.slice2table' when 'Init' is called.
func SliceToTable(L *lua.State) int {
	return CopySliceToTable(L, reflect.ValueOf(mustUnwrapProxy(L, 1)))
}

// StructToTable defines 'luar.struct2table' when 'Init' is called.
func StructToTable(L *lua.State) int {
	return CopyStructToTable(L, reflect.ValueOf(mustUnwrapProxy(L, 1)))
}
