// Copyright (C) 2026 Jae Kwon
//
// This file is part of gnosh's GnoVM machine subsystem. It is licensed under
// the GNO Network General Public License, version 6, as published by
// NewTendermint, LLC or its successor, or any later version of that License
// that the Licensor accepts as the proxy under its section 14, by a statement
// published in the canonical copy of internal/gnovm/LICENSE.md; together with
// the Additional Terms (Strong Attribution) in that file.

package gnovm

import (
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	gno "github.com/gnolang/gno/gnovm/pkg/gnolang"
	storetypes "github.com/gnolang/gno/tm2/pkg/store/types"
)

// The natives behind klashlibs/fmt: Go's fmt over the operands of a Gno
// call. Models write Go's fmt (%-10s, %5.2f, %+v of a struct, %w), and gno's
// ufmt, which gnosh shipped first, printed "(unhandled)" for most of it.

const (
	// maxFmtWidth caps a width or precision: Go's fmt allocates the
	// padding, and %999999999d would take the worker's memory.
	maxFmtWidth = 1 << 16
	// maxFmtOutput caps what one call formats, so a huge value prints its
	// start and stops.
	maxFmtOutput = 1 << 20
	// maxFmtDepth is how deep nested values are followed.
	maxFmtDepth = 10
	// maxFmtRaw caps the bytes of the strings and byte slices one call
	// takes in as operands: Go's fmt builds all of its text before the
	// output is cut to maxFmtOutput, and operands that share one string
	// cost the VM its bytes once (the gno review of 2026-11-19: four
	// thousand operands holding one 4 MB string built 16 GB).
	maxFmtRaw = 2 * maxFmtOutput
)

// fmtGas is what a native fmt call costs, before fmtElemGas: the GnoVM
// refuses to run a native without a calibrated entry. Measured on the
// devbox against gno's own op costs, which it runs at about 0.55 ns per gas:
// formatting a few basic values costs as a few dozen Gno ops do, and so
// does formatting a composite, element by element (TestFmtChargesPerElement).
var fmtGas = gno.NativeGasInfo{
	Base: 1000, SlopeIdx: -1, SlopeKind: gno.SizeFlat,
	PostSlope: 4 << 10, PostSlopeIdx: 1, PostSlopeKind: gno.SizeReturnLen,
}

// fmtElemGas is charged for each element, field, key or value of a
// composite operand that is formatted: Go's fmt takes about 100 ns for
// each, far more than its bytes of output would pay.
const fmtElemGas = 200

func init() {
	for _, name := range []gno.Name{"sprintf", "sprint", "sprintln"} {
		info := fmtGas
		gno.RegisterNativeGas("fmt", name, &info)
	}
}

func fmtNative(name gno.Name) func(*gno.Machine) {
	switch name {
	case "sprintf":
		return func(m *gno.Machine) {
			format := fmtArg(m, 0).GetString()
			tvs, args, budget := fmtOperands(m, fmtArg(m, 1))
			format, err := prepareFormat(format, tvs, args)
			if err != nil {
				panic(err.Error())
			}
			fmtPush(m, fmt.Sprintf(format, args...), budget)
		}
	case "sprint":
		return func(m *gno.Machine) {
			_, args, budget := fmtOperands(m, fmtArg(m, 0))
			fmtPush(m, fmt.Sprint(args...), budget)
		}
	case "sprintln":
		return func(m *gno.Machine) {
			_, args, budget := fmtOperands(m, fmtArg(m, 0))
			fmtPush(m, fmt.Sprintln(args...), budget)
		}
	}
	return nil
}

// fmtArg is the native's i'th parameter. A VM's values are all in memory
// (its package has no realm), so nothing needs loading; gno's DeepFill
// would panic on a map anyway.
func fmtArg(m *gno.Machine, i int) *gno.TypedValue {
	return m.LastBlock().GetPointerTo(nil, gno.NewValuePathBlock(1, uint16(i), "")).TV
}

// fmtPush returns s from the native, charging for the composite elements
// formatted.
func fmtPush(m *gno.Machine, s string, budget *fmtBudget) {
	if m.GasMeter != nil && budget.elems > 0 {
		m.GasMeter.ConsumeGas(storetypes.Gas(budget.elems*fmtElemGas), "fmt")
	}
	if len(s) > maxFmtOutput {
		s = s[:maxFmtOutput] + "…"
	}
	m.PushValue(gno.Go2GnoValue(m.Alloc, m.Store, reflect.ValueOf(&s).Elem()))
}

// fmtOperands turns a Gno []any into Go operands: basic values as the Go
// values they are, anything else as a gnoValue, which formats itself. It
// returns the Gno values too.
func fmtOperands(m *gno.Machine, args *gno.TypedValue) ([]gno.TypedValue, []any, *fmtBudget) {
	budget := &fmtBudget{left: maxFmtOutput}
	tvs := listOf(m.Store, *args)
	out := make([]any, len(tvs))
	for i, tv := range tvs {
		out[i] = goOperand(m.Store, tv, budget)
	}
	return tvs, out, budget
}

// fmtBudget is what is left of a call's output, the composite elements it
// has formatted, and the bytes of strings and byte slices taken in (raw,
// up to maxFmtRaw).
type fmtBudget struct{ left, elems, raw int }

// take is as much of a string or of bytes, n long, as the call still takes
// in, and says whether it was cut.
func (b *fmtBudget) take(n int) (keep int, cut bool) {
	keep = min(n, max(maxFmtRaw-b.raw, 0))
	b.raw += keep
	return keep, keep < n
}

// cutText is s cut to at most n bytes, at a character's start.
func cutText(s string, n int) string {
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// listOf returns the elements of a slice or array value.
func listOf(store gno.Store, tv gno.TypedValue) []gno.TypedValue {
	switch v := tv.V.(type) {
	case *gno.SliceValue:
		base := v.GetBase(store)
		if base == nil {
			return nil
		}
		if base.Data != nil {
			return byteElems(base.Data[v.Offset : v.Offset+v.Length])
		}
		return base.List[v.Offset : v.Offset+v.Length]
	case *gno.ArrayValue:
		if v.Data != nil {
			return byteElems(v.Data)
		}
		return v.List
	}
	return nil
}

// bytesOf copies the bytes of a byte slice or array value, as many as the
// call still takes in.
func bytesOf(store gno.Store, tv gno.TypedValue, budget *fmtBudget) []byte {
	var data []byte
	var list []gno.TypedValue
	switch v := tv.V.(type) {
	case *gno.SliceValue:
		base := v.GetBase(store)
		if base == nil {
			return []byte{}
		}
		if base.Data != nil {
			data = base.Data[v.Offset : v.Offset+v.Length]
		} else {
			list = base.List[v.Offset : v.Offset+v.Length]
		}
	case *gno.ArrayValue:
		data, list = v.Data, v.List
	}
	n := len(data)
	if data == nil {
		n = len(list)
	}
	keep, _ := budget.take(n)
	out := make([]byte, keep)
	if data != nil {
		copy(out, data[:keep])
		return out
	}
	for i := range out {
		out[i] = list[i].GetUint8()
	}
	return out
}

func byteElems(data []byte) []gno.TypedValue {
	out := make([]gno.TypedValue, len(data))
	for i, b := range data {
		out[i] = gno.TypedValue{T: gno.Uint8Type}
		out[i].SetUint8(b)
	}
	return out
}

func goOperand(store gno.Store, tv gno.TypedValue, budget *fmtBudget) any {
	if hiv, ok := tv.V.(*gno.HeapItemValue); ok {
		tv = hiv.Value
	}
	if tv.T == nil {
		return nil
	}
	if text, value, ok := byMethod(tv); ok {
		return &methodArg{text: text, value: goOperand(store, value, budget)}
	}
	switch tv.T.Kind() {
	case gno.BoolKind:
		return tv.GetBool()
	case gno.StringKind:
		s := tv.GetString()
		if keep, cut := budget.take(len(s)); cut {
			return cutText(s, keep) + "…"
		}
		return s
	case gno.IntKind:
		return int(tv.GetInt())
	case gno.Int8Kind:
		return tv.GetInt8()
	case gno.Int16Kind:
		return tv.GetInt16()
	case gno.Int32Kind:
		return tv.GetInt32()
	case gno.Int64Kind:
		return tv.GetInt64()
	case gno.UintKind:
		return uint(tv.GetUint())
	case gno.Uint8Kind:
		return tv.GetUint8()
	case gno.Uint16Kind:
		return tv.GetUint16()
	case gno.Uint32Kind:
		return tv.GetUint32()
	case gno.Uint64Kind:
		return tv.GetUint64()
	case gno.Float32Kind:
		return math.Float32frombits(tv.GetFloat32())
	case gno.Float64Kind:
		return math.Float64frombits(tv.GetFloat64())
	case gno.SliceKind, gno.ArrayKind:
		// A byte slice formats as Go's does: %s and %q as text, %x in hex.
		// Its bytes are read as they are held, and only as many as the
		// call takes in: a value for each byte took forty times its size.
		if et := elemType(tv.T); et != nil && et.Kind() == gno.Uint8Kind {
			return bytesOf(store, tv, budget)
		}
	}
	return &gnoValue{store: store, tv: tv, budget: budget}
}

// byMethod unwraps klashlibs/fmt's byMethod: the text a String or Error
// method gave, and the operand.
func byMethod(tv gno.TypedValue) (string, gno.TypedValue, bool) {
	dt, ok := tv.T.(*gno.DeclaredType)
	if !ok || dt.PkgPath != "fmt" || dt.Name != "byMethod" {
		return "", gno.TypedValue{}, false
	}
	sv, ok := tv.V.(*gno.StructValue)
	if !ok || len(sv.Fields) != 2 {
		return "", gno.TypedValue{}, false
	}
	return sv.Fields[0].GetString(), sv.Fields[1], true
}

// methodArg formats as Go's fmt formats a value with a String or Error
// method: the method's text for %v, %s, %q, %x and %X, and the value
// itself for any other verb.
type methodArg struct {
	text  string
	value any
}

func (a *methodArg) Format(st fmt.State, verb rune) {
	switch verb {
	case 'v', 's', 'q', 'x', 'X':
		fmt.Fprintf(st, directive(st, verb), a.text)
	default:
		fmt.Fprintf(st, directive(st, verb), a.value)
	}
}

func elemType(t gno.Type) gno.Type {
	switch t := baseType(t).(type) {
	case *gno.SliceType:
		return t.Elt
	case *gno.ArrayType:
		return t.Elt
	}
	return nil
}

func baseType(t gno.Type) gno.Type {
	if dt, ok := t.(*gno.DeclaredType); ok {
		return dt.Base
	}
	return t
}

// gnoValue is a Gno value that is not a basic one: it formats itself as Go's
// fmt would format the same Go value, applying the directive to each
// element. A pointer prints as &{...} at any depth (Go prints a nested one
// as an address, which would differ when a VM is replayed).
type gnoValue struct {
	store  gno.Store
	tv     gno.TypedValue
	budget *fmtBudget
	depth  int
}

func (g *gnoValue) Format(st fmt.State, verb rune) {
	var b strings.Builder
	g.write(&b, st, verb)
	if g.budget.left -= b.Len(); g.budget.left < 0 {
		_, _ = io.WriteString(st, "…")
		return
	}
	_, _ = io.WriteString(st, b.String())
}

func (g *gnoValue) child(tv gno.TypedValue) any {
	v := goOperand(g.store, tv, g.budget)
	if c, ok := v.(*gnoValue); ok {
		c.depth = g.depth + 1
	}
	return v
}

func (g *gnoValue) write(b *strings.Builder, st fmt.State, verb rune) {
	if g.depth > maxFmtDepth {
		b.WriteString("…")
		return
	}
	spec := directive(st, verb)
	sharp := verb == 'v' && st.Flag('#')
	elem := func(tv gno.TypedValue) {
		if b.Len() > g.budget.left {
			return
		}
		g.budget.elems++
		fmt.Fprintf(b, spec, g.child(tv))
	}
	tv := g.tv
	switch tv.T.Kind() {
	case gno.SliceKind, gno.ArrayKind:
		if sharp {
			b.WriteString(tv.T.String())
			b.WriteString("{")
		} else {
			b.WriteString("[")
		}
		for i, e := range listOf(g.store, tv) {
			if b.Len() > g.budget.left {
				break
			}
			if i > 0 {
				b.WriteString(sep(sharp))
			}
			elem(e)
		}
		if sharp {
			b.WriteString("}")
		} else {
			b.WriteString("]")
		}
	case gno.MapKind:
		if sharp {
			b.WriteString(tv.T.String())
			b.WriteString("{")
		} else {
			b.WriteString("map[")
		}
		var items []*gno.MapListItem
		if mv, ok := tv.V.(*gno.MapValue); ok && mv != nil && mv.List != nil {
			for it := mv.List.Head; it != nil; it = it.Next {
				items = append(items, it)
			}
		}
		// Go's fmt prints a map sorted by key.
		sort.SliceStable(items, func(i, j int) bool { return keyLess(items[i].Key, items[j].Key) })
		for i, it := range items {
			if i > 0 {
				b.WriteString(sep(sharp))
			}
			elem(it.Key)
			b.WriteString(":")
			elem(it.Value)
		}
		if sharp {
			b.WriteString("}")
		} else {
			b.WriteString("]")
		}
	case gno.StructKind:
		stt, _ := baseType(tv.T).(*gno.StructType)
		sv, _ := tv.V.(*gno.StructValue)
		if sharp {
			b.WriteString(tv.T.String())
		}
		b.WriteString("{")
		if sv != nil {
			for i, f := range sv.Fields {
				if i > 0 {
					b.WriteString(sep(sharp))
				}
				if (st.Flag('+') || sharp) && stt != nil && i < len(stt.Fields) {
					b.WriteString(string(stt.Fields[i].Name))
					b.WriteString(":")
				}
				elem(f)
			}
		}
		b.WriteString("}")
	case gno.PointerKind:
		pv, ok := tv.V.(gno.PointerValue)
		if !ok || pv.TV == nil {
			b.WriteString("<nil>")
			return
		}
		b.WriteString("&")
		elem(pv.Deref())
	case gno.FuncKind:
		if tv.V == nil {
			b.WriteString("<nil>")
		} else {
			b.WriteString("func")
		}
	default:
		b.WriteString(gno.BoundedSprintTV(tv, nil, maxValue))
	}
}

func sep(sharp bool) string {
	if sharp {
		return ", "
	}
	return " "
}

// directive rebuilds the directive being applied, to apply it to elements.
func directive(st fmt.State, verb rune) string {
	var b strings.Builder
	b.WriteByte('%')
	for _, c := range "+-# 0" {
		if st.Flag(int(c)) {
			b.WriteRune(c)
		}
	}
	if w, ok := st.Width(); ok {
		b.WriteString(strconv.Itoa(w))
	}
	if p, ok := st.Precision(); ok {
		b.WriteString("." + strconv.Itoa(p))
	}
	b.WriteRune(verb)
	return b.String()
}

// keyLess orders map keys as Go's fmt does for the basic kinds: numbers by
// value, strings by bytes, false before true; others by their text.
func keyLess(a, b gno.TypedValue) bool {
	if a.T != nil && b.T != nil && a.T.Kind() == b.T.Kind() {
		switch a.T.Kind() {
		case gno.StringKind:
			return a.GetString() < b.GetString()
		case gno.BoolKind:
			return !a.GetBool() && b.GetBool()
		case gno.IntKind, gno.Int8Kind, gno.Int16Kind, gno.Int32Kind, gno.Int64Kind,
			gno.UintKind, gno.Uint8Kind, gno.Uint16Kind, gno.Uint32Kind, gno.Uint64Kind,
			gno.Float32Kind, gno.Float64Kind:
			return number(a) < number(b)
		}
	}
	return gno.BoundedSprintTV(a, nil, maxValue) < gno.BoundedSprintTV(b, nil, maxValue)
}

func number(tv gno.TypedValue) float64 {
	switch v := goOperand(nil, tv, &fmtBudget{}).(type) {
	case int:
		return float64(v)
	case int8:
		return float64(v)
	case int16:
		return float64(v)
	case int32:
		return float64(v)
	case int64:
		return float64(v)
	case uint:
		return float64(v)
	case uint8:
		return float64(v)
	case uint16:
		return float64(v)
	case uint32:
		return float64(v)
	case uint64:
		return float64(v)
	case float32:
		return float64(v)
	case float64:
		return v
	}
	return 0
}

// prepareFormat readies a format for Go's fmt over Gno operands. It refuses
// a width or precision, written or taken by *, over maxFmtWidth; and turns
// each %T into %s of the operand's Gno type, which Go's fmt cannot know.
func prepareFormat(format string, tvs []gno.TypedValue, args []any) (string, error) {
	f := []byte(format)
	arg := 0
	// The widths and precisions of all the directives together are capped
	// too: each pads its operand, and %[1]65536d written a hundred
	// thousand times built 6.5 GB (the gno review of 2026-11-19).
	total := 0
	add := func(n int) error {
		if total += max(n, -n); total > maxFmtOutput {
			return fmt.Errorf("fmt: the widths and precisions of the format add up to over %d", maxFmtOutput)
		}
		return nil
	}
directives:
	for i := 0; i < len(f); i++ {
		if f[i] != '%' {
			continue
		}
		for i++; i < len(f); {
			c := f[i]
			switch {
			case c == '[':
				n := 0
				for i+1 < len(f) && f[i+1] >= '0' && f[i+1] <= '9' {
					i++
					n = n*10 + int(f[i]-'0')
				}
				arg = n - 1
			case c == '*':
				if arg >= 0 && arg < len(args) {
					if n, ok := args[arg].(int); ok && (n > maxFmtWidth || n < -maxFmtWidth) {
						return "", fmt.Errorf("fmt: a width or precision of %d is over %d", n, maxFmtWidth)
					} else if ok {
						if err := add(n); err != nil {
							return "", err
						}
					}
				}
				arg++
			case c >= '0' && c <= '9':
				n := 0
				for ; i < len(f) && f[i] >= '0' && f[i] <= '9'; i++ {
					if n = n*10 + int(f[i]-'0'); n > maxFmtWidth {
						return "", fmt.Errorf("fmt: a width or precision over %d", maxFmtWidth)
					}
				}
				if err := add(n); err != nil {
					return "", err
				}
				continue
			case c == '%':
				continue directives
			case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
				switch {
				case c == 'p':
					return "", errors.New("fmt: %p prints an address, which is different each time a VM is rebuilt; use %v")
				case c == 'T' && arg >= 0 && arg < len(args):
					f[i] = 's'
					args[arg] = "<nil>"
					tv := tvs[arg]
					if _, value, ok := byMethod(tv); ok {
						tv = value
					}
					if tv.T != nil {
						args[arg] = tv.T.String()
					}
				}
				arg++
				continue directives
			}
			i++
		}
	}
	return string(f), nil
}
