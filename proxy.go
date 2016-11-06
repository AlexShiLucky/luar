package luar

import (
	"fmt"
	"math"
	"reflect"
	"sync"
	"unsafe"

	"github.com/aarzilli/golua/lua"
)

// Lua proxy objects for Go slices, maps and structs
type valueProxy struct {
	value reflect.Value
	t     reflect.Type
}

const (
	cNumberMeta    = "numberMT"
	cStringMeta    = "stringMT"
	cSliceMeta     = "sliceMT"
	cMapMeta       = "mapMT"
	cStructMeta    = "structMT"
	cInterfaceMeta = "interfaceMT"
	cChannelMeta   = "ChannelMT"
)

var proxyMap = map[*valueProxy]reflect.Value{}
var proxymu = &sync.Mutex{}

func isPointerToPrimitive(v reflect.Value) bool {
	return v.Kind() == reflect.Ptr && v.Elem().IsValid() && types[int(v.Elem().Kind())] != nil
}

func makeValueProxy(L *lua.State, val reflect.Value, proxyMT string) {
	rawptr := L.NewUserdata(unsafe.Sizeof(valueProxy{}))
	ptr := (*valueProxy)(rawptr)
	ptr.value = val
	ptr.t = val.Type()
	proxymu.Lock()
	proxyMap[ptr] = val
	proxymu.Unlock()
	L.LGetMetaTable(proxyMT)
	L.SetMetaTable(-2)
}

func valueOnStack(L *lua.State, idx int) (reflect.Value, reflect.Type) {
	if isValueProxy(L, idx) {
		return valueOfProxy(L, idx)
	}

	switch L.Type(idx) {
	case lua.LUA_TNUMBER:
		v := L.ToNumber(idx)
		return reflect.ValueOf(v), reflect.TypeOf(v)
	case lua.LUA_TSTRING:
		v := L.ToString(idx)
		return reflect.ValueOf(v), reflect.TypeOf(v)
	}
	return reflect.Value{}, nil
}

func valueOfProxy(L *lua.State, idx int) (reflect.Value, reflect.Type) {
	vp := (*valueProxy)(L.ToUserdata(idx))
	return vp.value, vp.t
}

func isValueProxy(L *lua.State, idx int) bool {
	res := false
	if L.IsUserdata(idx) {
		L.GetMetaTable(idx)
		if !L.IsNil(-1) {
			L.GetField(-1, "luago.value")
			res = !L.IsNil(-1)
			L.Pop(1)
		}
		L.Pop(1)
	}
	return res
}

func unwrapProxy(L *lua.State, idx int) interface{} {
	if isValueProxy(L, idx) {
		v, _ := valueOfProxy(L, idx)
		return v.Interface()
	}
	return nil
}

func unwrapProxyOrComplain(L *lua.State, idx int) interface{} {
	if isValueProxy(L, idx) {
		v, _ := valueOfProxy(L, idx)
		return v.Interface()
	}
	RaiseError(L, fmt.Sprintf("arg #%d is not a Go object!", idx))
	return nil
}

func callGoMethod(L *lua.State, name string, st reflect.Value) {
	ret := st.MethodByName(name)
	if !ret.IsValid() {
		T := st.Type()
		// Could not resolve this method. Perhaps it's defined on the pointer?
		if T.Kind() != reflect.Ptr {
			if st.CanAddr() { // easy if we can get a pointer directly
				st = st.Addr()
			} else { // otherwise have to create and initialize one...
				VP := reflect.New(T)
				VP.Elem().Set(st)
				st = VP
			}
		}
		ret = st.MethodByName(name)
		assertValid(L, ret, st, name, "method")
	}
	L.PushGoFunction(GoLuaFunc(L, ret))
}

// TODO: What is this for?
func GoLua(L *lua.State) int {
	go func() {
		LT := L.NewThread()
		L.PushValue(1)
		lua.XMove(L, LT, 1)
		res := LT.Resume(0)
		for res != 0 {
			if res == 2 {
				emsg := LT.ToString(-1)
				RaiseError(LT, emsg)
			}
			ch, t := valueOfProxy(LT, -2)

			if LT.ToBoolean(-1) { // send on a channel
				val := reflect.ValueOf(LuaToGo(LT, t.Elem(), -3))
				ch.Send(val)
				res = LT.Resume(0)
			} else { // receive on a channel
				val, ok := ch.Recv()
				GoToLua(LT, t.Elem(), val, false)
				LT.PushBoolean(ok)
				res = LT.Resume(2)
			}
		}
	}()
	return 0
}

// InitProxies sets up a Lua state for using Go<->Lua proxies.
// This need not be called if the Lua state was created with Init().
// This function is useful if you want to set up your Lua state manually, e.g.
// with a custom allocator.
func InitProxies(L *lua.State) {
	flagValue := func() {
		L.SetMetaMethod("__tostring", proxy__tostring)
		L.SetMetaMethod("__gc", proxy__gc)
		L.SetMetaMethod("__eq", proxy__eq)
		L.PushBoolean(true)
		L.SetField(-2, "luago.value")
		L.Pop(1)
	}

	L.NewMetaTable(cNumberMeta)
	L.SetMetaMethod("__index", interface__index)
	L.SetMetaMethod("__lt", number__lt)
	L.SetMetaMethod("__add", number__add)
	L.SetMetaMethod("__sub", number__sub)
	L.SetMetaMethod("__mul", number__mul)
	L.SetMetaMethod("__div", number__div)
	L.SetMetaMethod("__mod", number__mod)
	L.SetMetaMethod("__pow", number__pow)
	L.SetMetaMethod("__unm", number__unm)
	flagValue()

	L.NewMetaTable(cStringMeta)
	// TODO: String indexing?
	L.SetMetaMethod("__index", interface__index)
	L.SetMetaMethod("__len", string__len)
	L.SetMetaMethod("__lt", string__lt)
	L.SetMetaMethod("__concat", string__concat)
	flagValue()

	L.NewMetaTable(cSliceMeta)
	L.SetMetaMethod("__index", slice__index)
	L.SetMetaMethod("__newindex", slice__newindex)
	L.SetMetaMethod("__len", slicemap__len)
	L.SetMetaMethod("__ipairs", slice__ipairs)
	L.SetMetaMethod("__pairs", slice__ipairs)
	flagValue()

	L.NewMetaTable(cMapMeta)
	L.SetMetaMethod("__index", map__index)
	L.SetMetaMethod("__newindex", map__newindex)
	L.SetMetaMethod("__len", slicemap__len)
	L.SetMetaMethod("__ipairs", map__ipairs)
	L.SetMetaMethod("__pairs", map__pairs)
	flagValue()

	L.NewMetaTable(cStructMeta)
	L.SetMetaMethod("__index", struct__index)
	L.SetMetaMethod("__newindex", struct__newindex)
	flagValue()

	L.NewMetaTable(cInterfaceMeta)
	L.SetMetaMethod("__index", interface__index)
	flagValue()

	L.NewMetaTable(cChannelMeta)
	L.NewTable()
	L.PushGoFunction(channel_send)
	L.SetField(-2, "Send")
	L.PushGoFunction(channel_recv)
	L.SetField(-2, "Recv")
	L.SetField(-2, "__index")
	flagValue()
}

func proxy__eq(L *lua.State) int {
	// TODO: The two values are not necessarily proxies, are they?
	v1, t1 := valueOfProxy(L, 1)
	v2, t2 := valueOfProxy(L, 2)
	if t1 != t2 {
		RaiseError(L, fmt.Sprintf("mismatched types %s and %s", t1, t2))
	}
	L.PushBoolean(v1.Interface() == v2.Interface())
	return 1
}

func proxy__gc(L *lua.State) int {
	vp := (*valueProxy)(L.ToUserdata(1))
	proxymu.Lock()
	delete(proxyMap, vp)
	proxymu.Unlock()
	return 0
}

func proxy__tostring(L *lua.State) int {
	obj, _ := valueOfProxy(L, 1)
	L.PushString(fmt.Sprintf("%v", obj))
	return 1
}

func channel_send(L *lua.State) int {
	L.PushValue(2)
	L.PushValue(1)
	L.PushBoolean(true)
	return L.Yield(3)
	//~ ch,t := valueOfProxy(L,1)
	//~ val := reflect.ValueOf(LuaToGo(L, t.Elem(),2))
	//~ ch.Send(val)
	//~ return 0
}

func channel_recv(L *lua.State) int {
	L.PushValue(1)
	L.PushBoolean(false)
	return L.Yield(2)
	//~ ch,t := valueOfProxy(L,1)
	//~ L.Yield(0)
	//~ val,ok := ch.Recv()
	//~ GoToLua(L,t.Elem(),val)
	//~ L.PushBoolean(ok)
	//~ L.Resume(0)
	//~ return 2
}

func slice__index(L *lua.State) int {
	slice, _ := valueOfProxy(L, 1)
	for slice.Kind() == reflect.Ptr {
		// For arrays.
		slice = slice.Elem()
	}
	if L.IsNumber(2) {
		idx := L.ToInteger(2)
		if idx < 1 || idx > slice.Len() {
			RaiseError(L, "slice/array get: index out of range")
		}
		ret := slice.Index(idx - 1)
		GoToLua(L, nil, ret, false)
	} else if L.IsString(2) {
		name := L.ToString(2)
		callGoMethod(L, name, slice)
	} else {
		RaiseError(L, "slice/array requires integer index")
	}
	return 1
}

func slice__newindex(L *lua.State) int {
	slice, t := valueOfProxy(L, 1)
	for slice.Kind() == reflect.Ptr {
		// For arrays.
		slice = slice.Elem()
		t = t.Elem()
	}
	idx := L.ToInteger(2)
	val := reflect.ValueOf(LuaToGo(L, t.Elem(), 3))
	if idx < 1 || idx > slice.Len() {
		RaiseError(L, "slice/array set: index out of range")
	}
	slice.Index(idx - 1).Set(val)
	return 0
}

func slicemap__len(L *lua.State) int {
	val, _ := valueOfProxy(L, 1)
	for val.Kind() == reflect.Ptr {
		// For arrays.
		val = val.Elem()
	}
	L.PushInteger(int64(val.Len()))
	return 1
}

func map__index(L *lua.State) int {
	val, t := valueOfProxy(L, 1)
	key := reflect.ValueOf(LuaToGo(L, t.Key(), 2))
	ret := val.MapIndex(key)
	if ret.IsValid() {
		GoToLua(L, nil, ret, false)
		return 1
	} else if key.Kind() == reflect.String {
		st := val
		name := key.String()

		// From 'callGoMethod':
		ret := st.MethodByName(name)
		if !ret.IsValid() {
			T := st.Type()
			// Could not resolve this method. Perhaps it's defined on the pointer?
			if T.Kind() != reflect.Ptr {
				if st.CanAddr() { // easy if we can get a pointer directly
					st = st.Addr()
				} else { // otherwise have to create and initialize one...
					VP := reflect.New(T)
					VP.Elem().Set(st)
					st = VP
				}
			}
			ret = st.MethodByName(name)
			// Unlike 'callGoMethod', do not panic.
			if !ret.IsValid() {
				L.PushNil()
				return 1
			}
		}
		L.PushGoFunction(GoLuaFunc(L, ret))
		return 1
	}
	return 0
}

func map__newindex(L *lua.State) int {
	m, t := valueOfProxy(L, 1)
	key := reflect.ValueOf(LuaToGo(L, t.Key(), 2))
	val := reflect.ValueOf(LuaToGo(L, t.Elem(), 3))
	m.SetMapIndex(key, val)
	return 0
}

func map__pairs(L *lua.State) int {
	m, _ := valueOfProxy(L, 1)
	keys := m.MapKeys()
	idx := -1
	n := m.Len()
	iter := func(L *lua.State) int {
		idx++
		if idx == n {
			L.PushNil()
			return 1
		}
		GoToLua(L, nil, keys[idx], false)
		val := m.MapIndex(keys[idx])
		GoToLua(L, nil, val, false)
		return 2
	}
	L.PushGoFunction(iter)
	return 1
}

func map__ipairs(L *lua.State) int {
	m, _ := valueOfProxy(L, 1)
	keys := m.MapKeys()
	intKeys := map[uint64]reflect.Value{}

	// Filter integer keys.
	for _, k := range keys {
		if k.Kind() == reflect.Interface {
			k = k.Elem()
		}
		switch k.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			i := k.Int()
			if i > 0 {
				intKeys[uint64(i)] = k
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			intKeys[k.Uint()] = k
		}
	}

	idx := uint64(0)
	iter := func(L *lua.State) int {
		idx++
		if _, ok := intKeys[idx]; !ok {
			L.PushNil()
			return 1
		}
		GoToLua(L, nil, reflect.ValueOf(idx), false)
		val := m.MapIndex(intKeys[idx])
		GoToLua(L, nil, val, false)
		return 2
	}
	L.PushGoFunction(iter)
	return 1
}

func slice__ipairs(L *lua.State) int {
	s, _ := valueOfProxy(L, 1)
	for s.Kind() == reflect.Ptr {
		s = s.Elem()
	}
	n := s.Len()
	idx := -1
	iter := func(L *lua.State) int {
		idx++
		if idx == n {
			L.PushNil()
			return 1
		}
		GoToLua(L, nil, reflect.ValueOf(idx+1), false) // report as 1-based index
		val := s.Index(idx)
		GoToLua(L, nil, val, false)
		return 2
	}
	L.PushGoFunction(iter)
	return 1
}

func struct__index(L *lua.State) int {
	st, t := valueOfProxy(L, 1)
	name := L.ToString(2)
	est := st
	if t.Kind() == reflect.Ptr {
		est = st.Elem()
	}
	ret := est.FieldByName(name)
	if !ret.IsValid() || !ret.CanSet() {
		// No such exported field, try for method.
		callGoMethod(L, name, st)
	} else {
		if isPointerToPrimitive(ret) {
			GoToLua(L, nil, ret.Elem(), false)
		} else {
			GoToLua(L, nil, ret, false)
		}
	}
	return 1
}

func interface__index(L *lua.State) int {
	st, _ := valueOfProxy(L, 1)
	name := L.ToString(2)
	callGoMethod(L, name, st)
	return 1
}

func struct__newindex(L *lua.State) int {
	st, t := valueOfProxy(L, 1)
	name := L.ToString(2)
	if t.Kind() == reflect.Ptr {
		st = st.Elem()
	}
	field := st.FieldByName(name)
	assertValid(L, field, st, name, "field")
	val := reflect.ValueOf(LuaToGo(L, field.Type(), 3))
	if isPointerToPrimitive(field) {
		field.Elem().Set(val)
	} else {
		field.Set(val)
	}
	return 0
}

// pushNumberValue pushes the number resulting from an arithmetic operation.
//
// At least one operand must be a proxy for this function to be called. See the
// main documentation for the conversion rules.
func pushNumberValue(L *lua.State, i interface{}, t1, t2 reflect.Type) {
	v := reflect.ValueOf(i)
	floatType := reflect.TypeOf(0.0)
	if t1 == t2 || t2 == floatType {
		makeValueProxy(L, v.Convert(t1), cNumberMeta)
	} else if t1 == floatType {
		makeValueProxy(L, v.Convert(t2), cNumberMeta)
	} else {
		L.PushNumber(valueToFloat(v))
	}
}

// Shorthand for kind-switches.
func numericKind(v reflect.Value) reflect.Kind {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflect.Int64
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return reflect.Uint64
	case reflect.Float32, reflect.Float64:
		return reflect.Float64
	default:
		return reflect.Invalid
	}
}

func valueToFloat(v reflect.Value) float64 {
	switch numericKind(v) {
	case reflect.Int64:
		return float64(v.Int())
	case reflect.Uint64:
		return float64(v.Uint())
	}
	return v.Float()
}

func valueToString(L *lua.State, v reflect.Value) string {
	if numericKind(v) != reflect.Invalid {
		switch numericKind(v) {
		case reflect.Uint64:
			return fmt.Sprintf("%v", v.Uint())
		case reflect.Int64:
			return fmt.Sprintf("%v", v.Int())
		case reflect.Float64:
			return fmt.Sprintf("%v", v.Float())
		}
	}
	if v.Kind() == reflect.String {
		return v.String()
	}

	RaiseError(L, "cannot convert to string")
	return ""
}

// commonKind returns the kind to which v1 and v2 can be converted with the
// least information loss.
func commonKind(v1, v2 reflect.Value) reflect.Kind {
	k1 := numericKind(v1)
	k2 := numericKind(v2)
	if k1 == k2 && (k1 == reflect.Uint64 || k1 == reflect.Int64) {
		return k1
	}
	return reflect.Float64
}

func number__lt(L *lua.State) int {
	v1, _ := valueOnStack(L, 1)
	v2, _ := valueOnStack(L, 2)
	switch commonKind(v1, v2) {
	case reflect.Uint64:
		L.PushBoolean(v1.Uint() < v2.Uint())
	case reflect.Int64:
		L.PushBoolean(v1.Int() < v2.Int())
	case reflect.Float64:
		L.PushBoolean(valueToFloat(v1) < valueToFloat(v2))
	}
	return 1
}

func number__add(L *lua.State) int {
	v1, t1 := valueOnStack(L, 1)
	v2, t2 := valueOnStack(L, 2)
	var result interface{}
	switch commonKind(v1, v2) {
	case reflect.Uint64:
		result = v1.Uint() + v2.Uint()
	case reflect.Int64:
		result = v1.Int() + v2.Int()
	case reflect.Float64:
		result = valueToFloat(v1) + valueToFloat(v2)
	}
	pushNumberValue(L, result, t1, t2)
	return 1
}

func number__sub(L *lua.State) int {
	v1, t1 := valueOnStack(L, 1)
	v2, t2 := valueOnStack(L, 2)
	var result interface{}
	switch commonKind(v1, v2) {
	case reflect.Uint64:
		result = v1.Uint() - v2.Uint()
	case reflect.Int64:
		result = v1.Int() - v2.Int()
	case reflect.Float64:
		result = valueToFloat(v1) - valueToFloat(v2)
	}
	pushNumberValue(L, result, t1, t2)
	return 1
}

func number__mul(L *lua.State) int {
	v1, t1 := valueOnStack(L, 1)
	v2, t2 := valueOnStack(L, 2)
	var result interface{}
	switch commonKind(v1, v2) {
	case reflect.Uint64:
		result = v1.Uint() * v2.Uint()
	case reflect.Int64:
		result = v1.Int() * v2.Int()
	case reflect.Float64:
		result = valueToFloat(v1) * valueToFloat(v2)
	}
	pushNumberValue(L, result, t1, t2)
	return 1
}

func number__div(L *lua.State) int {
	v1, t1 := valueOnStack(L, 1)
	v2, t2 := valueOnStack(L, 2)
	var result interface{}
	switch commonKind(v1, v2) {
	case reflect.Uint64:
		result = v1.Uint() / v2.Uint()
	case reflect.Int64:
		result = v1.Int() / v2.Int()
	case reflect.Float64:
		result = valueToFloat(v1) / valueToFloat(v2)
	}
	pushNumberValue(L, result, t1, t2)
	return 1
}

func number__mod(L *lua.State) int {
	v1, t1 := valueOnStack(L, 1)
	v2, t2 := valueOnStack(L, 2)
	var result interface{}
	switch commonKind(v1, v2) {
	case reflect.Uint64:
		result = v1.Uint() % v2.Uint()
	case reflect.Int64:
		result = v1.Int() % v2.Int()
	case reflect.Float64:
		result = math.Mod(valueToFloat(v1), valueToFloat(v2))
	}
	pushNumberValue(L, result, t1, t2)
	return 1
}

func number__pow(L *lua.State) int {
	v1, t1 := valueOnStack(L, 1)
	v2, t2 := valueOnStack(L, 2)
	var result interface{}
	switch commonKind(v1, v2) {
	case reflect.Uint64:
		result = math.Pow(float64(v1.Uint()), float64(v2.Uint()))
	case reflect.Int64:
		result = math.Pow(float64(v1.Int()), float64(v2.Int()))
	case reflect.Float64:
		result = math.Pow(valueToFloat(v1), valueToFloat(v2))
	}
	pushNumberValue(L, result, t1, t2)
	return 1
}

func number__unm(L *lua.State) int {
	v1, t1 := valueOnStack(L, 1)
	var result interface{}
	switch numericKind(v1) {
	case reflect.Uint64:
		result = -v1.Uint()
	case reflect.Int64:
		result = -v1.Int()
	case reflect.Float64:
		result = -v1.Float()
	}
	v := reflect.ValueOf(result)
	if isNewScalar(t1) != nil {
		makeValueProxy(L, v1, cNumberMeta)
	} else {
		L.PushNumber(v.Float())
	}
	return 1
}

func string__len(L *lua.State) int {
	v1, _ := valueOnStack(L, 1)
	L.PushInteger(int64(v1.Len()))
	return 1
}

func string__lt(L *lua.State) int {
	v1, _ := valueOnStack(L, 1)
	v2, _ := valueOnStack(L, 2)
	L.PushBoolean(v1.String() < v2.String())
	return 1
}

// Lua accepts concatenation with string and number.
func string__concat(L *lua.State) int {
	v1, t1 := valueOnStack(L, 1)
	v2, t2 := valueOnStack(L, 2)
	s1 := valueToString(L, v1)
	s2 := valueToString(L, v2)
	result := s1 + s2

	stringType := reflect.TypeOf("")
	floatType := reflect.TypeOf(0.0)
	if t1 == t2 || t2 == floatType || t2 == stringType {
		v := reflect.ValueOf(result)
		makeValueProxy(L, v.Convert(t1), cStringMeta)
	} else if t1 == floatType || t1 == stringType {
		v := reflect.ValueOf(result)
		makeValueProxy(L, v.Convert(t2), cStringMeta)
	} else {
		L.PushString(result)
	}

	return 1
}
