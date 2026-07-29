package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type FKind int

const (
	FQuantity FKind = iota
	FBool
	FString
)

type FValue struct {
	Kind          FKind
	Number        float64
	Uncertainty   float64
	Dimension     Dim
	Bool          bool
	Text          string
	PreferredUnit string
}

func fnum(x float64) FValue { return FValue{Kind: FQuantity, Number: x} }
func fbool(x bool) FValue   { return FValue{Kind: FBool, Bool: x} }
func fstr(x string) FValue  { return FValue{Kind: FString, Text: x} }
func (v FValue) truthy() (bool, error) {
	switch v.Kind {
	case FBool:
		return v.Bool, nil
	case FQuantity:
		if v.Dimension != (Dim{}) {
			return false, errors.New("物理量不能直接作为布尔条件")
		}
		return v.Number != 0, nil
	case FString:
		return v.Text != "", nil
	}
	return false, nil
}
func (v FValue) String() string {
	switch v.Kind {
	case FBool:
		if v.Bool {
			return "true"
		}
		return "false"
	case FString:
		return v.Text
	case FQuantity:
		value := v.Number
		unit := ""
		if v.PreferredUnit != "" {
			if u, e := parseUnit(v.PreferredUnit); e == nil && u.Dim == v.Dimension {
				value = v.Number / u.Factor
				unit = " " + v.PreferredUnit
			}
		}
		s := strconv.FormatFloat(value, 'g', 12, 64)
		if v.Uncertainty > 0 {
			u := v.Uncertainty
			if v.PreferredUnit != "" {
				if q, e := parseUnit(v.PreferredUnit); e == nil {
					u /= q.Factor
				}
			}
			s += " ± " + strconv.FormatFloat(u, 'g', 8, 64)
		}
		if unit != "" {
			return s + unit
		}
		if v.Dimension != (Dim{}) {
			return s + " SI{" + v.Dimension.String() + "}"
		}
		return s
	}
	return "null"
}

type UnitDef struct {
	Factor float64
	Dim    Dim
}

var unitDefs map[string]UnitDef

func initUnits() {
	unitDefs = map[string]UnitDef{}
	add := func(n string, f float64, d string) { x, _ := parseDim(d); unitDefs[n] = UnitDef{f, x} }
	add("one", 1, "1")
	add("m", 1, "Length")
	add("metre", 1, "Length")
	add("meter", 1, "Length")
	add("km", 1000, "Length")
	add("cm", .01, "Length")
	add("mm", .001, "Length")
	add("s", 1, "Time")
	add("second", 1, "Time")
	add("min", 60, "Time")
	add("h", 3600, "Time")
	add("kg", 1, "Mass")
	add("g", .001, "Mass")
	add("A", 1, "ElectricCurrent")
	add("K", 1, "Temperature")
	add("N", 1, "Force")
	add("newton", 1, "Force")
	add("J", 1, "Energy")
	add("joule", 1, "Energy")
	add("W", 1, "Power")
	add("Pa", 1, "Pressure")
	add("Hz", 1, "1/Time")
	add("rad", 1, "1")
	add("deg", math.Pi/180, "1")
}

type unitParser struct {
	s string
	p int
}

func (p *unitParser) sp() {
	for p.p < len(p.s) && (p.s[p.p] == ' ' || p.s[p.p] == '\t') {
		p.p++
	}
}
func (p *unitParser) eat(c byte) bool {
	p.sp()
	if p.p < len(p.s) && p.s[p.p] == c {
		p.p++
		return true
	}
	return false
}
func (p *unitParser) name() (string, error) {
	p.sp()
	st := p.p
	for p.p < len(p.s) {
		c := p.s[p.p]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '.') {
			break
		}
		p.p++
	}
	if st == p.p {
		return "", errors.New("缺少单位名称")
	}
	return p.s[st:p.p], nil
}
func (p *unitParser) factor() (UnitDef, error) {
	var u UnitDef
	var e error
	if p.eat('(') {
		u, e = p.prod()
		if e == nil && !p.eat(')') {
			e = errors.New("单位表达式缺少 )")
		}
	} else {
		var n string
		n, e = p.name()
		if e == nil {
			var ok bool
			u, ok = unitDefs[n]
			if !ok {
				e = fmt.Errorf("未知单位 %s", n)
			}
		}
	}
	if e != nil {
		return u, e
	}
	if p.eat('^') {
		sg := 1
		if p.eat('-') {
			sg = -1
		} else {
			p.eat('+')
		}
		p.sp()
		st := p.p
		for p.p < len(p.s) && p.s[p.p] >= '0' && p.s[p.p] <= '9' {
			p.p++
		}
		if st == p.p {
			return u, errors.New("单位幂必须是整数")
		}
		n, _ := strconv.Atoi(p.s[st:p.p])
		n *= sg
		u.Factor = math.Pow(u.Factor, float64(n))
		u.Dim = dpow(u.Dim, n)
	}
	return u, nil
}
func (p *unitParser) prod() (UnitDef, error) {
	u, e := p.factor()
	if e != nil {
		return u, e
	}
	for {
		if p.eat('*') {
			v, e := p.factor()
			if e != nil {
				return u, e
			}
			u.Factor *= v.Factor
			u.Dim = dmul(u.Dim, v.Dim)
		} else if p.eat('/') {
			v, e := p.factor()
			if e != nil {
				return u, e
			}
			u.Factor /= v.Factor
			u.Dim = ddiv(u.Dim, v.Dim)
		} else {
			break
		}
	}
	return u, nil
}
func parseUnit(s string) (UnitDef, error) {
	p := &unitParser{s: s}
	u, e := p.prod()
	p.sp()
	if e == nil && p.p != len(s) {
		e = fmt.Errorf("单位表达式尾部无效: %s", s[p.p:])
	}
	return u, e
}

type FFunction struct {
	Params  []string
	Body    string
	Closure *FEnv
}
type FLawParam struct{ Name, Type string }
type FLaw struct {
	Name        string
	Params      []FLawParam
	Assumptions []string
	Equations   []string
	Closure     *FEnv
}
type ExportKind string

const (
	ExportQuantity ExportKind = "quantity"
	ExportUnit     ExportKind = "unit"
	ExportLaw      ExportKind = "law"
	ExportFunction ExportKind = "function"
	ExportConstant ExportKind = "constant"
)

type ImportState struct {
	Loaded  map[string]bool
	Loading map[string]bool
	Graph   map[string][]string
	Modules map[string]*FEnv
}
type SourcePackageMetadata struct {
	Name       string
	Version    string
	Requires   string
	Namespace  string
	Extensions map[string]string
}
type FEnv struct {
	Parent             *FEnv
	Values             map[string]FValue
	Consts             map[string]bool
	Functions          map[string]FFunction
	Laws               map[string]FLaw
	Exports            map[string]ExportKind
	Aliases            map[string]string
	DeclaredQuantities map[string]Dim
	DeclaredUnits      map[string]UnitDef
	BaseDir            string
	Manifest           *PackageManifest
	SourceMeta         SourcePackageMetadata
	Packages           *PackageManager
	Imports            *ImportState
}

var quantityDefs map[string]Dim
var frontendMu sync.Mutex

func initQuantityDefs() { quantityDefs = map[string]Dim{} }
func newFEnv() *FEnv    { return newRootFEnv("") }
func newRootFEnv(base string) *FEnv {
	initUnits()
	initQuantityDefs()
	if base == "" {
		base, _ = os.Getwd()
	}
	e := &FEnv{Values: map[string]FValue{}, Consts: map[string]bool{}, Functions: map[string]FFunction{}, Laws: map[string]FLaw{}, Exports: map[string]ExportKind{}, Aliases: map[string]string{}, DeclaredQuantities: map[string]Dim{}, DeclaredUnits: map[string]UnitDef{}, BaseDir: base, Packages: NewPackageManager(base), Imports: &ImportState{Loaded: map[string]bool{}, Loading: map[string]bool{}, Graph: map[string][]string{}, Modules: map[string]*FEnv{}}}
	e.Values["pi"] = fnum(math.Pi)
	e.Consts["pi"] = true
	e.Values["e"] = fnum(math.E)
	e.Consts["e"] = true
	vd, _ := parseDim("Velocity")
	ad, _ := parseDim("Acceleration")
	e.Values["c"] = FValue{Kind: FQuantity, Number: 299792458, Dimension: vd, PreferredUnit: "m/s"}
	e.Consts["c"] = true
	e.Values["g0"] = FValue{Kind: FQuantity, Number: 9.80665, Dimension: ad, PreferredUnit: "m/s^2"}
	e.Consts["g0"] = true
	e.Laws["NewtonSecondLaw"] = FLaw{Name: "NewtonSecondLaw", Params: []FLawParam{{"force", "Force"}, {"mass", "Mass"}, {"acceleration", "Acceleration"}}, Assumptions: []string{"mass>0 kg"}, Equations: []string{"force==mass*acceleration"}, Closure: e}
	return e
}
func newChildFEnv(parent *FEnv, base string) *FEnv {
	if base == "" {
		base = parent.BaseDir
	}
	return &FEnv{Parent: parent, Values: map[string]FValue{}, Consts: map[string]bool{}, Functions: map[string]FFunction{}, Laws: map[string]FLaw{}, Exports: map[string]ExportKind{}, Aliases: map[string]string{}, DeclaredQuantities: map[string]Dim{}, DeclaredUnits: map[string]UnitDef{}, BaseDir: base, Packages: parent.Packages, Imports: parent.Imports}
}
func (e *FEnv) lookupValue(name string) (FValue, bool) {
	for x := e; x != nil; x = x.Parent {
		if v, ok := x.Values[name]; ok {
			return v, true
		}
	}
	return FValue{}, false
}
func (e *FEnv) lookupFunction(name string) (FFunction, bool) {
	for x := e; x != nil; x = x.Parent {
		if v, ok := x.Functions[name]; ok {
			return v, true
		}
	}
	return FFunction{}, false
}
func (e *FEnv) lookupLaw(name string) (FLaw, bool) {
	for x := e; x != nil; x = x.Parent {
		if v, ok := x.Laws[name]; ok {
			return v, true
		}
	}
	return FLaw{}, false
}
func (e *FEnv) declareValue(name string, v FValue, isConst bool) error {
	if _, ok := e.Values[name]; ok {
		return fmt.Errorf("符号 %s 已在当前作用域声明", name)
	}
	e.Values[name] = v
	e.Consts[name] = isConst
	return nil
}
func (e *FEnv) assignValue(name string, v FValue) error {
	for x := e; x != nil; x = x.Parent {
		if _, ok := x.Values[name]; ok {
			if x.Consts[name] {
				return fmt.Errorf("常量 %s 不可重新赋值", name)
			}
			x.Values[name] = v
			return nil
		}
	}
	return fmt.Errorf("未定义变量 %s", name)
}

func splitConversion(s string) (string, string) {
	depth := 0
	quote := byte(0)
	candidate := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		switch c {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		}
		if depth == 0 && i+4 <= len(s) && s[i:i+4] == " in " {
			candidate = i
		}
	}
	if candidate >= 0 {
		return strings.TrimSpace(s[:candidate]), strings.TrimSpace(s[candidate+4:])
	}
	return strings.TrimSpace(s), ""
}
func preprocessExpr(s string) (string, map[string]FValue, error) {
	vals := map[string]FValue{}
	var out strings.Builder
	idx := 0
	makeVar := func(v FValue) string { name := fmt.Sprintf("__lit%d", idx); idx++; vals[name] = v; return name }
	for i := 0; i < len(s); {
		c := s[i]
		if c == '"' || c == '\'' {
			q := c
			j := i + 1
			var b strings.Builder
			for j < len(s) && s[j] != q {
				if s[j] == '\\' && j+1 < len(s) {
					j++
					switch s[j] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					default:
						b.WriteByte(s[j])
					}
					j++
				} else {
					b.WriteByte(s[j])
					j++
				}
			}
			if j >= len(s) {
				return "", nil, errors.New("字符串未闭合")
			}
			out.WriteString(makeVar(fstr(b.String())))
			i = j + 1
			continue
		}
		if (c >= '0' && c <= '9') || (c == '.' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9') {
			st := i
			i++
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				i++
			}
			if i < len(s) && s[i] == '.' {
				i++
				for i < len(s) && s[i] >= '0' && s[i] <= '9' {
					i++
				}
			}
			if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
				q := i
				i++
				if i < len(s) && (s[i] == '+' || s[i] == '-') {
					i++
				}
				d := i
				for i < len(s) && s[i] >= '0' && s[i] <= '9' {
					i++
				}
				if d == i {
					i = q
				}
			}
			numText := s[st:i]
			num, _ := strconv.ParseFloat(numText, 64)
			k := i
			for k < len(s) && (s[k] == ' ' || s[k] == '\t') {
				k++
			}
			unit := ""
			end := i
			if k < len(s) && s[k] == '[' {
				depth := 1
				j := k + 1
				for j < len(s) && depth > 0 {
					if s[j] == '[' {
						depth++
					} else if s[j] == ']' {
						depth--
					}
					j++
				}
				if depth != 0 {
					return "", nil, errors.New("单位方括号未闭合")
				}
				unit = s[k+1 : j-1]
				end = j
			} else if k < len(s) && ((s[k] >= 'A' && s[k] <= 'Z') || (s[k] >= 'a' && s[k] <= 'z')) {
				j := k + 1
				for j < len(s) && ((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z') || (s[j] >= '0' && s[j] <= '9') || s[j] == '_' || s[j] == '.') {
					j++
				}
				cand := s[k:j]
				if _, ok := unitDefs[cand]; ok {
					unit = cand
					end = j
				}
			}
			if unit != "" {
				u, e := parseUnit(unit)
				if e != nil {
					return "", nil, e
				}
				out.WriteString(makeVar(FValue{Kind: FQuantity, Number: num * u.Factor, Dimension: u.Dim, PreferredUnit: unit}))
				i = end
			} else {
				out.WriteString(numText)
			}
			continue
		}
		out.WriteByte(c)
		i++
	}
	return out.String(), vals, nil
}

func sameDim(a, b FValue, op string) error {
	if a.Kind != FQuantity || b.Kind != FQuantity {
		return fmt.Errorf("%s 需要数值", op)
	}
	if a.Dimension != b.Dimension {
		return fmt.Errorf("%s 量纲不匹配: %s 与 %s", op, a.Dimension, b.Dimension)
	}
	return nil
}
func evalFrontendNode(n *Node, env *FEnv, local map[string]FValue) (FValue, error) {
	get := func(name string) (FValue, bool) {
		if v, ok := local[name]; ok {
			return v, true
		}
		return env.lookupValue(name)
	}
	switch n.K {
	case KConst:
		f, _ := n.V.Float64()
		return fnum(f), nil
	case KVar:
		if n.Name == "true" {
			return fbool(true), nil
		}
		if n.Name == "false" {
			return fbool(false), nil
		}
		if v, ok := get(n.Name); ok {
			return v, nil
		}
		return FValue{}, fmt.Errorf("未定义变量 %s", n.Name)
	case KNeg:
		v, e := evalFrontendNode(n.A[0], env, local)
		if e != nil {
			return v, e
		}
		if v.Kind != FQuantity {
			return v, errors.New("负号只能用于数值")
		}
		v.Number = -v.Number
		return v, nil
	case KNot:
		v, e := evalFrontendNode(n.A[0], env, local)
		if e != nil {
			return v, e
		}
		b, e := v.truthy()
		return fbool(!b), e
	case KAnd, KOr:
		a, e := evalFrontendNode(n.A[0], env, local)
		if e != nil {
			return a, e
		}
		ab, e := a.truthy()
		if e != nil {
			return a, e
		}
		if n.K == KAnd && !ab {
			return fbool(false), nil
		}
		if n.K == KOr && ab {
			return fbool(true), nil
		}
		b, e := evalFrontendNode(n.A[1], env, local)
		if e != nil {
			return b, e
		}
		bb, e := b.truthy()
		return fbool(bb), e
	case KAdd, KSub, KMul, KDiv, KPow, KEq, KNe, KLt, KLe, KGt, KGe:
		a, e := evalFrontendNode(n.A[0], env, local)
		if e != nil {
			return a, e
		}
		b, e := evalFrontendNode(n.A[1], env, local)
		if e != nil {
			return b, e
		}
		switch n.K {
		case KAdd, KSub:
			if a.Kind == FString && b.Kind == FString && n.K == KAdd {
				return fstr(a.Text + b.Text), nil
			}
			if e := sameDim(a, b, "加减"); e != nil {
				return FValue{}, e
			}
			r := a
			if n.K == KAdd {
				r.Number = a.Number + b.Number
			} else {
				r.Number = a.Number - b.Number
			}
			r.Uncertainty = math.Hypot(a.Uncertainty, b.Uncertainty)
			return r, nil
		case KMul:
			r := FValue{Kind: FQuantity, Number: a.Number * b.Number, Dimension: dmul(a.Dimension, b.Dimension)}
			r.Uncertainty = math.Hypot(b.Number*a.Uncertainty, a.Number*b.Uncertainty)
			return r, nil
		case KDiv:
			if b.Number == 0 {
				return FValue{}, errors.New("除以零")
			}
			r := FValue{Kind: FQuantity, Number: a.Number / b.Number, Dimension: ddiv(a.Dimension, b.Dimension)}
			r.Uncertainty = math.Hypot(a.Uncertainty/b.Number, a.Number*b.Uncertainty/(b.Number*b.Number))
			return r, nil
		case KPow:
			if b.Dimension != (Dim{}) {
				return FValue{}, errors.New("指数必须无量纲")
			}
			rn := math.Round(b.Number)
			if a.Dimension != (Dim{}) && math.Abs(rn-b.Number) > 1e-12 {
				return FValue{}, errors.New("带量纲底数需要整数指数")
			}
			r := FValue{Kind: FQuantity, Number: math.Pow(a.Number, b.Number), Dimension: dpow(a.Dimension, int(rn))}
			if a.Uncertainty > 0 {
				r.Uncertainty = math.Abs(b.Number*math.Pow(a.Number, b.Number-1)) * a.Uncertainty
			}
			return r, nil
		case KEq, KNe:
			if a.Kind != b.Kind {
				return fbool(n.K == KNe), nil
			}
			if a.Kind == FString {
				eq := a.Text == b.Text
				if n.K == KNe {
					eq = !eq
				}
				return fbool(eq), nil
			}
			if a.Kind == FBool {
				eq := a.Bool == b.Bool
				if n.K == KNe {
					eq = !eq
				}
				return fbool(eq), nil
			}
			if e := sameDim(a, b, "比较"); e != nil {
				return FValue{}, e
			}
			scale := math.Max(1, math.Max(math.Abs(a.Number), math.Abs(b.Number)))
			eq := math.Abs(a.Number-b.Number) <= 1e-12*scale
			if n.K == KNe {
				eq = !eq
			}
			return fbool(eq), nil
		default:
			if e := sameDim(a, b, "比较"); e != nil {
				return FValue{}, e
			}
			var q bool
			switch n.K {
			case KLt:
				q = a.Number < b.Number
			case KLe:
				q = a.Number <= b.Number
			case KGt:
				q = a.Number > b.Number
			case KGe:
				q = a.Number >= b.Number
			}
			return fbool(q), nil
		}
	case KCall:
		args := make([]FValue, len(n.A))
		for i, x := range n.A {
			v, e := evalFrontendNode(x, env, local)
			if e != nil {
				return FValue{}, e
			}
			args[i] = v
		}
		return callFrontend(n.Name, args, env)
	}
	return FValue{}, errors.New("不支持的表达式")
}
func callFrontend(name string, a []FValue, env *FEnv) (FValue, error) {
	count := func(n int) error {
		if len(a) != n {
			return fmt.Errorf("%s 需要 %d 个参数", name, n)
		}
		return nil
	}
	if f, ok := env.lookupFunction(name); ok {
		if len(a) != len(f.Params) {
			return FValue{}, fmt.Errorf("函数 %s 参数数量错误", name)
		}
		closure := f.Closure
		if closure == nil {
			closure = env
		}
		child := newChildFEnv(closure, closure.BaseDir)
		for i, p := range f.Params {
			child.Values[p] = a[i]
		}
		err := executeFrontendBlock(f.Body, child, io.Discard)
		if ret, ok := err.(frontReturn); ok {
			return ret.Value, nil
		}
		if err != nil {
			return FValue{}, err
		}
		return FValue{}, fmt.Errorf("函数 %s 没有执行 return", name)
	}
	switch name {
	case "measured":
		if e := count(2); e != nil {
			return FValue{}, e
		}
		if e := sameDim(a[0], a[1], "测量不确定度"); e != nil {
			return FValue{}, e
		}
		r := a[0]
		r.Uncertainty = math.Abs(a[1].Number)
		return r, nil
	case "nominal":
		if e := count(1); e != nil {
			return FValue{}, e
		}
		r := a[0]
		r.Uncertainty = 0
		return r, nil
	case "uncertainty":
		if e := count(1); e != nil {
			return FValue{}, e
		}
		return FValue{Kind: FQuantity, Number: a[0].Uncertainty, Dimension: a[0].Dimension, PreferredUnit: a[0].PreferredUnit}, nil
	case "relative_uncertainty":
		if e := count(1); e != nil {
			return FValue{}, e
		}
		if a[0].Number == 0 {
			return FValue{}, errors.New("零值没有有限相对不确定度")
		}
		return fnum(math.Abs(a[0].Uncertainty / a[0].Number)), nil
	case "zscore":
		if e := count(2); e != nil {
			return FValue{}, e
		}
		if e := sameDim(a[0], a[1], "zscore"); e != nil {
			return FValue{}, e
		}
		u := math.Hypot(a[0].Uncertainty, a[1].Uncertainty)
		if u == 0 {
			return FValue{}, errors.New("合成不确定度为零")
		}
		return fnum(math.Abs(a[0].Number-a[1].Number) / u), nil
	case "compatible_measurements":
		if len(a) != 3 {
			return FValue{}, errors.New("compatible_measurements 需要 3 个参数")
		}
		z, e := callFrontend("zscore", a[:2], env)
		if e != nil {
			return FValue{}, e
		}
		return fbool(z.Number <= a[2].Number), nil
	case "abs":
		if e := count(1); e != nil {
			return FValue{}, e
		}
		r := a[0]
		r.Number = math.Abs(r.Number)
		return r, nil
	case "sqrt":
		if e := count(1); e != nil {
			return FValue{}, e
		}
		if a[0].Number < 0 {
			return FValue{}, errors.New("sqrt 定义域错误")
		}
		r := a[0]
		for i, x := range r.Dimension {
			if x%2 != 0 {
				return FValue{}, errors.New("sqrt 的量纲指数必须为偶数")
			}
			r.Dimension[i] = x / 2
		}
		r.Number = math.Sqrt(r.Number)
		if r.Uncertainty > 0 && r.Number != 0 {
			r.Uncertainty = a[0].Uncertainty / (2 * r.Number)
		}
		return r, nil
	case "sin", "cos", "tan", "exp", "log", "ln":
		if e := count(1); e != nil {
			return FValue{}, e
		}
		if a[0].Dimension != (Dim{}) {
			return FValue{}, fmt.Errorf("%s 需要无量纲输入", name)
		}
		x := a[0].Number
		switch name {
		case "sin":
			x = math.Sin(x)
		case "cos":
			x = math.Cos(x)
		case "tan":
			x = math.Tan(x)
		case "exp":
			x = math.Exp(x)
		default:
			x = math.Log(x)
		}
		return fnum(x), nil
	case "diff":
		if e := count(2); e != nil {
			return FValue{}, e
		}
		if a[0].Kind != FString || a[1].Kind != FString {
			return FValue{}, errors.New("diff 参数必须是字符串")
		}
		d, e := symbolicDerivative(a[0].Text, a[1].Text)
		if e != nil {
			return FValue{}, e
		}
		return fstr(d), nil
	case "simplify":
		if e := count(1); e != nil {
			return FValue{}, e
		}
		if a[0].Kind != FString {
			return FValue{}, errors.New("simplify 参数必须是字符串")
		}
		n, e := parse(a[0].Text)
		if e != nil {
			return FValue{}, e
		}
		return fstr(nodeStr(simplifyNode(n))), nil
	case "backend_run":
		if len(a) < 1 || a[0].Kind != FString {
			return FValue{}, errors.New("backend_run 第一个参数必须是表达式字符串")
		}
		assign := map[string]float64{}
		for _, v := range a[1:] {
			if v.Kind != FString {
				return FValue{}, errors.New("赋值应写成字符串 x=3")
			}
			p := strings.Index(v.Text, "=")
			if p < 0 {
				return FValue{}, errors.New("赋值格式应为 x=3")
			}
			x, e := strconv.ParseFloat(strings.TrimSpace(v.Text[p+1:]), 64)
			if e != nil {
				return FValue{}, e
			}
			assign[strings.TrimSpace(v.Text[:p])] = x
		}
		names := make([]string, 0, len(assign))
		for k := range assign {
			names = append(names, k)
		}
		sort.Strings(names)
		vals := make([]float64, len(names))
		for i, k := range names {
			vals[i] = assign[k]
		}
		m, e := compile(a[0].Text, names, make([]Dim, len(names)))
		if e != nil {
			return FValue{}, e
		}
		x, e := m.run(vals)
		return fnum(x), e
	case "backend_solve":
		if e := count(1); e != nil {
			return FValue{}, e
		}
		if a[0].Kind != FString {
			return FValue{}, errors.New("backend_solve 参数必须是字符串")
		}
		s, n, e := solve(a[0].Text)
		if e != nil {
			return FValue{}, e
		}
		return fstr(fmt.Sprintf("%s: explored %d Boolean assignments", s, n)), nil
	}
	return FValue{}, fmt.Errorf("未知函数 %s", name)
}
func evalFrontendExprWithLocal(expr string, env *FEnv, local map[string]FValue) (FValue, error) {
	raw, unit := splitConversion(expr)
	clean, lits, e := preprocessExpr(raw)
	if e != nil {
		return FValue{}, e
	}
	for k, v := range lits {
		local[k] = v
	}
	n, e := parse(clean)
	if e != nil {
		return FValue{}, e
	}
	v, e := evalFrontendNode(n, env, local)
	if e != nil {
		return v, e
	}
	if unit != "" {
		if v.Kind != FQuantity {
			return v, errors.New("只有物理量可以执行单位转换")
		}
		u, e := parseUnit(strings.Trim(unit, "[]"))
		if e != nil {
			return v, e
		}
		if u.Dim != v.Dimension {
			return v, fmt.Errorf("单位转换量纲不匹配: %s -> %s", v.Dimension, u.Dim)
		}
		v.PreferredUnit = strings.Trim(unit, "[]")
	}
	return v, nil
}
func evalFrontendExpr(expr string, env *FEnv) (FValue, error) {
	return evalFrontendExprWithLocal(expr, env, map[string]FValue{})
}

func simplifyNode(n *Node) *Node {
	if n == nil {
		return n
	}
	for i := range n.A {
		n.A[i] = simplifyNode(n.A[i])
	}
	zero := func(x *Node) bool { return x.K == KConst && x.V.Sign() == 0 }
	one := func(x *Node) bool { return x.K == KConst && x.V.Cmp(bigOne()) == 0 }
	if len(n.A) == 2 && n.A[0].K == KConst && n.A[1].K == KConst {
		a := new(big.Rat).Set(n.A[0].V)
		b := n.A[1].V
		switch n.K {
		case KAdd:
			a.Add(a, b)
		case KSub:
			a.Sub(a, b)
		case KMul:
			a.Mul(a, b)
		case KDiv:
			if b.Sign() != 0 {
				a.Quo(a, b)
			} else {
				return n
			}
		default:
			return n
		}
		return &Node{K: KConst, V: a}
	}
	switch n.K {
	case KAdd:
		if zero(n.A[0]) {
			return n.A[1]
		}
		if zero(n.A[1]) {
			return n.A[0]
		}
	case KSub:
		if zero(n.A[1]) {
			return n.A[0]
		}
		if nodeStr(n.A[0]) == nodeStr(n.A[1]) {
			return &Node{K: KConst, V: rat(0)}
		}
	case KMul:
		if zero(n.A[0]) || zero(n.A[1]) {
			return &Node{K: KConst, V: rat(0)}
		}
		if one(n.A[0]) {
			return n.A[1]
		}
		if one(n.A[1]) {
			return n.A[0]
		}
	case KDiv:
		if zero(n.A[0]) {
			return &Node{K: KConst, V: rat(0)}
		}
		if one(n.A[1]) {
			return n.A[0]
		}
	case KPow:
		if zero(n.A[1]) {
			return &Node{K: KConst, V: rat(1)}
		}
		if one(n.A[1]) {
			return n.A[0]
		}
	}
	return n
}
func bigOne() *big.Rat { return rat(1) }
func derivativeNode(n *Node, v string) (*Node, error) {
	z := func() *Node { return &Node{K: KConst, V: rat(0)} }
	o := func() *Node { return &Node{K: KConst, V: rat(1)} }
	switch n.K {
	case KConst:
		return z(), nil
	case KVar:
		if n.Name == v {
			return o(), nil
		}
		return z(), nil
	case KNeg:
		d, e := derivativeNode(n.A[0], v)
		return &Node{K: KNeg, A: []*Node{d}}, e
	case KAdd, KSub:
		a, e := derivativeNode(n.A[0], v)
		if e != nil {
			return nil, e
		}
		b, e := derivativeNode(n.A[1], v)
		return &Node{K: n.K, A: []*Node{a, b}}, e
	case KMul:
		a, e := derivativeNode(n.A[0], v)
		if e != nil {
			return nil, e
		}
		b, e := derivativeNode(n.A[1], v)
		if e != nil {
			return nil, e
		}
		return &Node{K: KAdd, A: []*Node{{K: KMul, A: []*Node{a, n.A[1]}}, {K: KMul, A: []*Node{n.A[0], b}}}}, nil
	case KDiv:
		a, e := derivativeNode(n.A[0], v)
		if e != nil {
			return nil, e
		}
		b, e := derivativeNode(n.A[1], v)
		if e != nil {
			return nil, e
		}
		num := &Node{K: KSub, A: []*Node{{K: KMul, A: []*Node{a, n.A[1]}}, {K: KMul, A: []*Node{n.A[0], b}}}}
		den := &Node{K: KPow, A: []*Node{n.A[1], {K: KConst, V: rat(2)}}}
		return &Node{K: KDiv, A: []*Node{num, den}}, nil
	case KPow:
		if n.A[1].K != KConst {
			return nil, errors.New("当前求导器只支持常数幂")
		}
		d, e := derivativeNode(n.A[0], v)
		if e != nil {
			return nil, e
		}
		c := n.A[1]
		minus := new(big.Rat).Sub(c.V, rat(1))
		return &Node{K: KMul, A: []*Node{c, {K: KMul, A: []*Node{{K: KPow, A: []*Node{n.A[0], {K: KConst, V: minus}}}, d}}}}, nil
	case KCall:
		if len(n.A) != 1 {
			return nil, errors.New("函数求导参数数量错误")
		}
		d, e := derivativeNode(n.A[0], v)
		if e != nil {
			return nil, e
		}
		var outer *Node
		switch n.Name {
		case "sin":
			outer = &Node{K: KCall, Name: "cos", A: n.A}
		case "cos":
			outer = &Node{K: KNeg, A: []*Node{{K: KCall, Name: "sin", A: n.A}}}
		case "exp":
			outer = &Node{K: KCall, Name: "exp", A: n.A}
		case "log", "ln":
			outer = &Node{K: KDiv, A: []*Node{o(), n.A[0]}}
		case "sqrt":
			outer = &Node{K: KDiv, A: []*Node{o(), {K: KMul, A: []*Node{{K: KConst, V: rat(2)}, {K: KCall, Name: "sqrt", A: n.A}}}}}
		default:
			return nil, fmt.Errorf("不支持函数 %s 的符号求导", n.Name)
		}
		return &Node{K: KMul, A: []*Node{outer, d}}, nil
	}
	return nil, errors.New("不支持该表达式的求导")
}
func symbolicDerivative(expr, v string) (string, error) {
	n, e := parse(expr)
	if e != nil {
		return "", e
	}
	d, e := derivativeNode(n, v)
	if e != nil {
		return "", e
	}
	return nodeStr(simplifyNode(d)), nil
}

func stripComments(s string) string {
	re := regexp.MustCompile(`(?m)//.*$|#.*$`)
	return re.ReplaceAllString(s, "")
}
func topStatements(src string) ([]string, error) {
	src = stripComments(src)
	var out []string
	for i := 0; i < len(src); {
		for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\r' || src[i] == '\n') {
			i++
		}
		if i >= len(src) {
			break
		}
		st := i
		depth := 0
		quote := byte(0)
		seenBrace := false
		for i < len(src) {
			c := src[i]
			if quote != 0 {
				if c == '\\' {
					i += 2
					continue
				}
				if c == quote {
					quote = 0
				}
				i++
				continue
			}
			if c == '"' || c == '\'' {
				quote = c
				i++
				continue
			}
			if c == '{' {
				depth++
				seenBrace = true
			} else if c == '}' {
				depth--
				if depth < 0 {
					return nil, errors.New("多余的 }")
				}
				if depth == 0 && seenBrace {
					prefix := strings.TrimSpace(src[st:])
					if strings.HasPrefix(prefix, "if ") || strings.HasPrefix(prefix, "if(") {
						j := i + 1
						for j < len(src) && (src[j] == ' ' || src[j] == '\t' || src[j] == '\r' || src[j] == '\n') {
							j++
						}
						if strings.HasPrefix(src[j:], "else") {
							i = j + len("else")
							continue
						}
					}
					if strings.HasPrefix(prefix, "law ") || strings.HasPrefix(prefix, "fn ") || strings.HasPrefix(prefix, "package ") || strings.HasPrefix(prefix, "export law ") || strings.HasPrefix(prefix, "export fn ") || strings.HasPrefix(prefix, "if ") || strings.HasPrefix(prefix, "if(") || strings.HasPrefix(prefix, "while ") || strings.HasPrefix(prefix, "while(") {
						i++
						break
					}
				}
			} else if c == ';' && depth == 0 {
				i++
				break
			}
			i++
		}
		part := strings.TrimSpace(src[st:i])
		if part != "" {
			out = append(out, part)
		}
	}
	return out, nil
}
func section(body, name string) string {
	p := strings.Index(body, name)
	if p < 0 {
		return ""
	}
	q := strings.Index(body[p:], "{")
	if q < 0 {
		return ""
	}
	q += p
	d := 1
	for i := q + 1; i < len(body); i++ {
		if body[i] == '{' {
			d++
		} else if body[i] == '}' {
			d--
			if d == 0 {
				return body[q+1 : i]
			}
		}
	}
	return ""
}
func parseLaw(stmt string) (FLaw, error) {
	re := regexp.MustCompile(`(?s)^law\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{(.*)\}\s*$`)
	m := re.FindStringSubmatch(stmt)
	if m == nil {
		return FLaw{}, errors.New("law 语法错误")
	}
	l := FLaw{Name: m[1]}
	for _, p := range strings.Split(section(m[2], "parameters"), ";") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		x := strings.SplitN(p, ":", 2)
		if len(x) != 2 {
			return l, fmt.Errorf("参数声明错误: %s", p)
		}
		l.Params = append(l.Params, FLawParam{strings.TrimSpace(x[0]), strings.TrimSpace(x[1])})
	}
	for _, x := range strings.Split(section(m[2], "assumptions"), ";") {
		if strings.TrimSpace(x) != "" {
			l.Assumptions = append(l.Assumptions, strings.TrimSpace(x))
		}
	}
	eq := section(m[2], "equations")
	if eq == "" {
		eq = section(m[2], "equation")
	}
	for _, x := range strings.Split(eq, ";") {
		if strings.TrimSpace(x) != "" {
			l.Equations = append(l.Equations, strings.TrimSpace(x))
		}
	}
	if len(l.Equations) == 0 {
		return l, errors.New("规律至少需要一个 equation")
	}
	return l, nil
}
func checkLawDimensions(l FLaw) error {
	dm := map[string]Dim{}
	for _, p := range l.Params {
		d, e := parseDim(p.Type)
		if e != nil {
			return e
		}
		dm[p.Name] = d
	}
	for _, eq := range append(append([]string{}, l.Assumptions...), l.Equations...) {
		clean, literals, e := preprocessExpr(eq)
		if e != nil {
			return e
		}
		localDims := map[string]Dim{}
		for name, dim := range dm {
			localDims[name] = dim
		}
		for name, value := range literals {
			if value.Kind == FQuantity {
				localDims[name] = value.Dimension
			}
		}
		n, e := parse(clean)
		if e != nil {
			return e
		}
		if _, e = inferDimRelation(n, localDims); e != nil {
			return fmt.Errorf("规律 %s: %w", l.Name, e)
		}
	}
	return nil
}
func inferDimRelation(n *Node, dm map[string]Dim) (Dim, error) {
	if n.K == KEq || n.K == KNe || n.K == KLt || n.K == KLe || n.K == KGt || n.K == KGe {
		a, e := inferDim(n.A[0], dm)
		if e != nil {
			return Dim{}, e
		}
		b, e := inferDim(n.A[1], dm)
		if e != nil {
			return Dim{}, e
		}
		if a != b {
			return Dim{}, fmt.Errorf("关系两侧量纲不匹配: %s 与 %s", a, b)
		}
		return Dim{}, nil
	}
	if n.K == KAnd || n.K == KOr || n.K == KNot {
		return Dim{}, nil
	}
	return inferDim(n, dm)
}

func parseControlStatement(t, keyword string) (condition, body, tail string, err error) {
	rest := strings.TrimSpace(strings.TrimPrefix(t, keyword))
	quote := byte(0)
	brace := -1
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '{' {
			brace = i
			break
		}
	}
	if brace < 0 {
		return "", "", "", errors.New(keyword + " 缺少代码块")
	}
	condition = strings.TrimSpace(rest[:brace])
	if strings.HasPrefix(condition, "(") && strings.HasSuffix(condition, ")") {
		condition = strings.TrimSpace(condition[1 : len(condition)-1])
	}
	depth := 1
	quote = 0
	end := -1
	for i := brace + 1; i < len(rest); i++ {
		c := rest[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				end = i
				break
			}
		}
	}
	if end < 0 {
		return "", "", "", errors.New(keyword + " 代码块未闭合")
	}
	body = rest[brace+1 : end]
	tail = strings.TrimSpace(rest[end+1:])
	return
}

type frontReturn struct{ Value FValue }

func (r frontReturn) Error() string { return "return" }

func executeFrontendBlock(body string, env *FEnv, out io.Writer) error {
	statements, err := topStatements(body)
	if err != nil {
		return err
	}
	for _, statement := range statements {
		if err = executeFrontendStatement(statement, env, out); err != nil {
			return err
		}
	}
	return nil
}

func executeFrontendStatement(stmt string, env *FEnv, out io.Writer) error {
	t := strings.TrimSpace(strings.TrimSuffix(stmt, ";"))
	if t == "" {
		return nil
	}
	exported := false
	if strings.HasPrefix(t, "export ") {
		if strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(t, "export")), "{") {
			return exportListStatement(t, env)
		}
		exported = true
		t = strings.TrimSpace(strings.TrimPrefix(t, "export"))
	}
	if strings.HasPrefix(t, "import ") {
		return executeImport(t+";", env, out)
	}
	if strings.HasPrefix(t, "package ") {
		meta, e := parsePackageBlock(t)
		if e != nil {
			return e
		}
		env.SourceMeta = meta
		if env.Manifest != nil {
			if meta.Name != "" && meta.Name != env.Manifest.Name {
				return fmt.Errorf("package 名称与清单不一致: %s / %s", meta.Name, env.Manifest.Name)
			}
		}
		return nil
	}
	if strings.HasPrefix(t, "if ") || strings.HasPrefix(t, "if(") {
		condition, body, tail, err := parseControlStatement(t, "if")
		if err != nil {
			return err
		}
		value, err := evalFrontendExpr(condition, env)
		if err != nil {
			return err
		}
		ok, err := value.truthy()
		if err != nil {
			return err
		}
		if ok {
			return executeFrontendBlock(body, newChildFEnv(env, env.BaseDir), out)
		}
		if strings.HasPrefix(tail, "else") {
			_, elseBody, extra, err := parseControlStatement(strings.TrimSpace(strings.TrimPrefix(tail, "else")), "")
			if err != nil {
				return err
			}
			if strings.TrimSpace(extra) != "" {
				return errors.New("else 后存在无法解析的内容")
			}
			return executeFrontendBlock(elseBody, newChildFEnv(env, env.BaseDir), out)
		}
		return nil
	}
	if strings.HasPrefix(t, "while ") || strings.HasPrefix(t, "while(") {
		condition, body, tail, err := parseControlStatement(t, "while")
		if err != nil {
			return err
		}
		if strings.TrimSpace(tail) != "" {
			return errors.New("while 后存在无法解析的内容")
		}
		for iteration := 0; iteration < 1000000; iteration++ {
			value, err := evalFrontendExpr(condition, env)
			if err != nil {
				return err
			}
			ok, err := value.truthy()
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
			if err = executeFrontendBlock(body, env, out); err != nil {
				return err
			}
		}
		return errors.New("while 超过 1000000 次安全限制")
	}
	if strings.HasPrefix(t, "quantity ") {
		x := strings.SplitN(strings.TrimSpace(strings.TrimPrefix(t, "quantity ")), ":", 2)
		if len(x) != 2 {
			return errors.New("quantity 语法错误，应为 quantity Name : Dimension")
		}
		name := strings.TrimSpace(x[0])
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(name) {
			return fmt.Errorf("无效物理量名称 %s", name)
		}
		d, e := parseDim(strings.TrimSpace(x[1]))
		if e != nil {
			return e
		}
		if old, ok := quantityDefs[name]; ok && old != d {
			return fmt.Errorf("物理量 %s 已注册为不同量纲 %s", name, old)
		}
		quantityDefs[name] = d
		env.DeclaredQuantities[name] = d
		if exported {
			env.Exports[name] = ExportQuantity
		}
		return nil
	}
	if strings.HasPrefix(t, "unit ") {
		re := regexp.MustCompile(`(?s)^unit\s+([A-Za-z_][A-Za-z0-9_.]*)\s*:\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.+)$`)
		m := re.FindStringSubmatch(t)
		if m == nil {
			return errors.New("unit 语法错误，应为 unit symbol : Quantity = scale")
		}
		name, typeName, expr := m[1], m[2], strings.TrimSpace(m[3])
		want, e := parseDim(typeName)
		if e != nil {
			return e
		}
		value, e := evalFrontendExpr(expr, env)
		if e != nil {
			return e
		}
		if value.Kind != FQuantity {
			return errors.New("单位定义必须是物理量")
		}
		if value.Dimension != want {
			return fmt.Errorf("单位 %s 的定义量纲为 %s，期望 %s", name, value.Dimension, want)
		}
		if value.Number == 0 {
			return errors.New("单位换算因子不能为零")
		}
		u := UnitDef{Factor: value.Number, Dim: value.Dimension}
		if old, ok := unitDefs[name]; ok && (old != u) {
			return fmt.Errorf("单位 %s 已注册为不同定义", name)
		}
		unitDefs[name] = u
		env.DeclaredUnits[name] = u
		if exported {
			env.Exports[name] = ExportUnit
		}
		return nil
	}
	if strings.HasPrefix(t, "law ") {
		l, e := parseLaw(t)
		if e != nil {
			return e
		}
		l.Closure = env
		if e = checkLawDimensions(l); e != nil {
			return e
		}
		if _, ok := env.Laws[l.Name]; ok {
			return fmt.Errorf("规律 %s 已声明", l.Name)
		}
		env.Laws[l.Name] = l
		if exported {
			env.Exports[l.Name] = ExportLaw
		}
		return nil
	}
	if strings.HasPrefix(t, "fn ") {
		re := regexp.MustCompile(`(?s)^fn\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]*)\)\s*\{(.*)\}$`)
		m := re.FindStringSubmatch(t)
		if m == nil {
			return errors.New("fn 语法错误")
		}
		var ps []string
		seen := map[string]bool{}
		for _, x := range strings.Split(m[2], ",") {
			x = strings.TrimSpace(x)
			if x != "" {
				if seen[x] {
					return fmt.Errorf("重复函数参数 %s", x)
				}
				seen[x] = true
				ps = append(ps, x)
			}
		}
		env.Functions[m[1]] = FFunction{Params: ps, Body: m[3], Closure: env}
		if exported {
			env.Exports[m[1]] = ExportFunction
		}
		return nil
	}
	if strings.HasPrefix(t, "return") {
		expr := strings.TrimSpace(strings.TrimPrefix(t, "return"))
		if expr == "" {
			return errors.New("return 必须返回值")
		}
		v, e := evalFrontendExpr(expr, env)
		if e != nil {
			return e
		}
		return frontReturn{v}
	}
	if strings.HasPrefix(t, "verify ") {
		return verifyFrontend(t, env, out)
	}
	if strings.HasPrefix(t, "let ") || strings.HasPrefix(t, "const ") {
		p := strings.Index(t, "=")
		if p < 0 {
			return errors.New("变量声明缺少 =")
		}
		left := strings.Fields(strings.TrimSpace(t[:p]))
		if len(left) < 2 {
			return errors.New("变量名缺失")
		}
		v, e := evalFrontendExpr(strings.TrimSpace(t[p+1:]), env)
		if e != nil {
			return e
		}
		isConst := left[0] == "const" || exported
		if e = env.declareValue(left[1], v, isConst); e != nil {
			return e
		}
		if exported {
			env.Exports[left[1]] = ExportConstant
		}
		return nil
	}
	if strings.HasPrefix(t, "print ") {
		v, e := evalFrontendExpr(strings.TrimSpace(strings.TrimPrefix(t, "print ")), env)
		if e != nil {
			return e
		}
		fmt.Fprintln(out, v.String())
		return nil
	}
	if strings.HasPrefix(t, "assert ") {
		v, e := evalFrontendExpr(strings.TrimSpace(strings.TrimPrefix(t, "assert ")), env)
		if e != nil {
			return e
		}
		b, e := v.truthy()
		if e != nil {
			return e
		}
		if !b {
			return errors.New("断言失败")
		}
		return nil
	}
	if p := strings.Index(t, "="); p > 0 && !strings.Contains(t[:p], "==") && !strings.Contains(t[:p], ">=") && !strings.Contains(t[:p], "<=") && !strings.Contains(t[:p], "!=") {
		name := strings.TrimSpace(t[:p])
		v, e := evalFrontendExpr(strings.TrimSpace(t[p+1:]), env)
		if e != nil {
			return e
		}
		return env.assignValue(name, v)
	}
	_, e := evalFrontendExpr(t, env)
	return e
}
func verifyFrontend(t string, env *FEnv, out io.Writer) error {
	re := regexp.MustCompile(`(?s)^verify\s+([A-Za-z_][A-Za-z0-9_.]*)(?:\s+against\s+([A-Za-z_][A-Za-z0-9_.]*))?(?:\s+with\s*\{(.*)\})?$`)
	m := re.FindStringSubmatch(t)
	if m == nil {
		return errors.New("verify 语法错误")
	}
	l, ok := env.lookupLaw(m[1])
	if !ok {
		return fmt.Errorf("未知规律 %s", m[1])
	}
	if e := checkLawDimensions(l); e != nil {
		return e
	}
	fmt.Fprintf(out, "Verification: %s\n  [PASS] Dimensional consistency\n", l.Name)
	if strings.TrimSpace(m[3]) != "" {
		closure := l.Closure
		if closure == nil {
			closure = env
		}
		sampleEnv := newChildFEnv(closure, closure.BaseDir)
		for _, x := range strings.Split(m[3], ";") {
			x = strings.TrimSpace(x)
			if x == "" {
				continue
			}
			p := strings.Index(x, "=")
			if p < 0 {
				return errors.New("verify with 赋值缺少 =")
			}
			v, e := evalFrontendExpr(strings.TrimSpace(x[p+1:]), env)
			if e != nil {
				return e
			}
			name := strings.TrimSpace(x[:p])
			sampleEnv.Values[name] = v
		}
		for _, p := range l.Params {
			v, exists := sampleEnv.Values[p.Name]
			if !exists {
				return fmt.Errorf("缺少样本参数 %s", p.Name)
			}
			d, e := parseDim(p.Type)
			if e != nil {
				return e
			}
			if v.Dimension != d {
				return fmt.Errorf("参数 %s 量纲错误: %s，期望 %s", p.Name, v.Dimension, d)
			}
		}
		assumptions := true
		for _, a := range l.Assumptions {
			v, e := evalFrontendExpr(a, sampleEnv)
			if e != nil {
				return e
			}
			b, e := v.truthy()
			if e != nil {
				return e
			}
			assumptions = assumptions && b
		}
		if assumptions {
			fmt.Fprintln(out, "  [PASS] Assumptions")
		} else {
			fmt.Fprintln(out, "  [FAIL] Assumptions")
		}
		eqok := assumptions
		for _, q := range l.Equations {
			v, e := evalFrontendExpr(q, sampleEnv)
			if e != nil {
				return e
			}
			b, e := v.truthy()
			if e != nil {
				return e
			}
			eqok = eqok && b
		}
		if eqok {
			fmt.Fprintln(out, "  [PASS] Sample equations")
		} else {
			fmt.Fprintln(out, "  [FAIL] Sample equations")
		}
	}
	if m[2] != "" {
		r, ok := env.lookupLaw(m[2])
		if !ok {
			return fmt.Errorf("未知参考规律 %s", m[2])
		}
		compatible := len(r.Params) == len(l.Params)
		if compatible {
			for i := range r.Params {
				compatible = compatible && r.Params[i].Name == l.Params[i].Name && r.Params[i].Type == l.Params[i].Type
			}
		}
		status := "FAIL"
		if compatible {
			status = "PASS"
		}
		fmt.Fprintf(out, "Against: %s\n  [%s] Parameter interface compatibility\n", r.Name, status)
	}
	return nil
}
func runFrontendSourceWithEnv(src, name string, env *FEnv, out io.Writer) error {
	sts, e := topStatements(src)
	if e != nil {
		return e
	}
	for i, statement := range sts {
		if e = executeFrontendStatement(statement, env, out); e != nil {
			if _, ok := e.(frontReturn); ok {
				return fmt.Errorf("%s: statement %d: return 只能出现在函数中", name, i+1)
			}
			return fmt.Errorf("%s: statement %d: %w", name, i+1, e)
		}
	}
	return nil
}
func runFrontendSource(src, name string, out io.Writer) (*FEnv, error) {
	frontendMu.Lock()
	defer frontendMu.Unlock()
	base, _ := os.Getwd()
	env := newRootFEnv(base)
	e := runFrontendSourceWithEnv(src, name, env, out)
	return env, e
}
func runFrontendFile(path string, out io.Writer) error {
	frontendMu.Lock()
	defer frontendMu.Unlock()
	abs, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	b, e := os.ReadFile(abs)
	if e != nil {
		return e
	}
	env := newRootFEnv(filepath.Dir(abs))
	return runFrontendSourceWithEnv(string(b), abs, env, out)
}
func checkFrontendSource(src, name string) error {
	var b bytes.Buffer
	_, e := runFrontendSource(src, name, &b)
	return e
}

func frontendREPL() error {
	frontendMu.Lock()
	defer frontendMu.Unlock()
	env := newRootFEnv("")
	in := bufio.NewScanner(os.Stdin)
	var buf strings.Builder
	braces := 0
	fmt.Printf("PhyLang Community %s REPL\n输入 quit、exit、:q 或 :quit 可直接返回原终端。\n", version)
	for {
		if buf.Len() == 0 {
			fmt.Print("phy> ")
		} else {
			fmt.Print("...> ")
		}
		if !in.Scan() {
			fmt.Println()
			return in.Err()
		}
		line := in.Text()
		trim := strings.TrimSpace(line)
		if buf.Len() == 0 && (trim == "quit" || trim == "exit" || trim == ":q" || trim == ":quit" || trim == "quit;" || trim == "exit;") {
			return nil
		}
		for _, c := range line {
			if c == '{' {
				braces++
			} else if c == '}' {
				braces--
			}
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
		if braces <= 0 && (strings.Contains(line, ";") || strings.HasSuffix(trim, "}")) {
			sts, e := topStatements(buf.String())
			if e != nil {
				fmt.Fprintln(os.Stderr, "error:", e)
			} else {
				for _, st := range sts {
					if e = executeFrontendStatement(st, env, os.Stdout); e != nil {
						fmt.Fprintln(os.Stderr, "error:", e)
						break
					}
				}
			}
			buf.Reset()
			braces = 0
		}
	}
}
func completionItems(src, prefix string) []string {
	base := []string{"package", "export", "requires", "namespace", "import", "as", "only", "let", "const", "print", "assert", "fn", "return", "if", "else", "while", "law", "parameters", "assumptions", "equation", "equations", "verify", "against", "with", "quantity", "unit", "in", "true", "false", "measured", "nominal", "uncertainty", "relative_uncertainty", "zscore", "compatible_measurements", "sin", "cos", "tan", "exp", "log", "sqrt", "abs", "diff", "simplify", "backend_run", "backend_solve", "Mass", "Length", "Time", "Velocity", "Acceleration", "Force", "Energy", "Power", "Momentum", "Pressure", "m", "km", "cm", "mm", "s", "min", "h", "kg", "g", "N", "J", "W", "Pa", "Hz", "rad", "deg", "NewtonSecondLaw"}
	patterns := []string{`\blet\s+([A-Za-z_][A-Za-z0-9_]*)`, `\bconst\s+([A-Za-z_][A-Za-z0-9_]*)`, `\bfn\s+([A-Za-z_][A-Za-z0-9_]*)`, `\blaw\s+([A-Za-z_][A-Za-z0-9_]*)`, `\bquantity\s+([A-Za-z_][A-Za-z0-9_]*)`, `\bunit\s+([A-Za-z_][A-Za-z0-9_]*)`, `([A-Za-z_][A-Za-z0-9_.]*)\s*:\s*[A-Za-z_]`}
	seen := map[string]bool{}
	for _, x := range base {
		seen[x] = true
	}
	for _, pat := range patterns {
		re := regexp.MustCompile(pat)
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			if len(m) > 1 {
				seen[m[1]] = true
			}
		}
	}
	// 将当前文件 import 的包清单导出也加入补全候选；解析失败不会影响编辑。
	pm := NewPackageManager("")
	importRe := regexp.MustCompile(`(?m)\bimport\s+([A-Za-z0-9_.-]+)(?:@([^;\s]+))?`)
	for _, m := range importRe.FindAllStringSubmatch(src, -1) {
		constraint := "*"
		if len(m) > 2 && m[2] != "" {
			constraint = m[2]
		}
		if manifest, e := pm.Resolve(m[1], constraint); e == nil {
			for _, group := range [][]string{manifest.Exports.Quantities, manifest.Exports.Units, manifest.Exports.Laws, manifest.Exports.Functions, manifest.Exports.Constants} {
				for _, name := range group {
					seen[name] = true
				}
			}
		}
	}
	var out []string
	p := strings.ToLower(prefix)
	for x := range seen {
		if p == "" || strings.HasPrefix(strings.ToLower(x), p) {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		ai := strings.EqualFold(out[i], prefix)
		aj := strings.EqualFold(out[j], prefix)
		if ai != aj {
			return ai
		}
		return out[i] < out[j]
	})
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}
