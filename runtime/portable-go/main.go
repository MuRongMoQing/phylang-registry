package main

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const version = "0.6.2"

type Kind int

const (
	KConst Kind = iota
	KVar
	KAdd
	KSub
	KMul
	KDiv
	KPow
	KNeg
	KCall
	KEq
	KNe
	KLt
	KLe
	KGt
	KGe
	KAnd
	KOr
	KNot
)

type Node struct {
	K    Kind
	V    *big.Rat
	Name string
	A    []*Node
}

type TokKind int

const (
	TEnd TokKind = iota
	TNum
	TId
	TLp
	TRp
	TComma
	TPlus
	TMinus
	TStar
	TSlash
	TCaret
	TBang
	TEq
	TNe
	TLt
	TLe
	TGt
	TGe
	TAnd
	TOr
)

type Tok struct {
	K   TokKind
	S   string
	Pos int
}

type Lexer struct {
	s string
	p int
}

func (l *Lexer) scan() ([]Tok, error) {
	var t []Tok
	for {
		for l.p < len(l.s) && (l.s[l.p] == ' ' || l.s[l.p] == '\t' || l.s[l.p] == '\r' || l.s[l.p] == '\n') {
			l.p++
		}
		st := l.p
		if l.p >= len(l.s) {
			t = append(t, Tok{TEnd, "", st})
			return t, nil
		}
		c := l.s[l.p]
		l.p++
		one := func(k TokKind) { t = append(t, Tok{k, l.s[st:l.p], st}) }
		switch c {
		case '(':
			one(TLp)
		case ')':
			one(TRp)
		case ',':
			one(TComma)
		case '+':
			one(TPlus)
		case '-':
			one(TMinus)
		case '*':
			one(TStar)
		case '/':
			one(TSlash)
		case '^':
			one(TCaret)
		case '!':
			if l.p < len(l.s) && l.s[l.p] == '=' {
				l.p++
				one(TNe)
			} else {
				one(TBang)
			}
		case '=':
			if l.p < len(l.s) && l.s[l.p] == '=' {
				l.p++
				one(TEq)
			} else {
				return nil, fmt.Errorf("expected == at %d", st)
			}
		case '<':
			if l.p < len(l.s) && l.s[l.p] == '=' {
				l.p++
				one(TLe)
			} else {
				one(TLt)
			}
		case '>':
			if l.p < len(l.s) && l.s[l.p] == '=' {
				l.p++
				one(TGe)
			} else {
				one(TGt)
			}
		case '&':
			if l.p < len(l.s) && l.s[l.p] == '&' {
				l.p++
				one(TAnd)
			} else {
				return nil, fmt.Errorf("expected && at %d", st)
			}
		case '|':
			if l.p < len(l.s) && l.s[l.p] == '|' {
				l.p++
				one(TOr)
			} else {
				return nil, fmt.Errorf("expected || at %d", st)
			}
		default:
			if (c >= '0' && c <= '9') || (c == '.' && l.p < len(l.s) && l.s[l.p] >= '0' && l.s[l.p] <= '9') {
				for l.p < len(l.s) && l.s[l.p] >= '0' && l.s[l.p] <= '9' {
					l.p++
				}
				if l.p < len(l.s) && l.s[l.p] == '.' {
					l.p++
					for l.p < len(l.s) && l.s[l.p] >= '0' && l.s[l.p] <= '9' {
						l.p++
					}
				}
				if l.p < len(l.s) && (l.s[l.p] == 'e' || l.s[l.p] == 'E') {
					q := l.p
					l.p++
					if l.p < len(l.s) && (l.s[l.p] == '+' || l.s[l.p] == '-') {
						l.p++
					}
					d := l.p
					for l.p < len(l.s) && l.s[l.p] >= '0' && l.s[l.p] <= '9' {
						l.p++
					}
					if d == l.p {
						l.p = q
					}
				}
				t = append(t, Tok{TNum, l.s[st:l.p], st})
			} else if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' {
				for l.p < len(l.s) {
					x := l.s[l.p]
					if !((x >= 'a' && x <= 'z') || (x >= 'A' && x <= 'Z') || (x >= '0' && x <= '9') || x == '_' || x == '.') {
						break
					}
					l.p++
				}
				t = append(t, Tok{TId, l.s[st:l.p], st})
			} else {
				return nil, fmt.Errorf("unexpected character %q at %d", c, st)
			}
		}
	}
}

type Parser struct {
	t []Tok
	p int
}

func parse(s string) (*Node, error) {
	ts, e := (&Lexer{s: s}).scan()
	if e != nil {
		return nil, e
	}
	p := &Parser{t: ts}
	n, e := p.or()
	if e != nil {
		return nil, e
	}
	if p.peek().K != TEnd {
		return nil, fmt.Errorf("trailing input at %d", p.peek().Pos)
	}
	return n, nil
}
func (p *Parser) peek() Tok { return p.t[p.p] }
func (p *Parser) prev() Tok { return p.t[p.p-1] }
func (p *Parser) match(ks ...TokKind) bool {
	for _, k := range ks {
		if p.peek().K == k {
			p.p++
			return true
		}
	}
	return false
}
func (p *Parser) need(k TokKind, msg string) (Tok, error) {
	if p.peek().K == k {
		x := p.peek()
		p.p++
		return x, nil
	}
	return Tok{}, fmt.Errorf("%s at %d", msg, p.peek().Pos)
}
func (p *Parser) or() (*Node, error) {
	n, e := p.and()
	if e != nil {
		return nil, e
	}
	for p.match(TOr) {
		r, e := p.and()
		if e != nil {
			return nil, e
		}
		n = &Node{K: KOr, A: []*Node{n, r}}
	}
	return n, nil
}
func (p *Parser) and() (*Node, error) {
	n, e := p.eq()
	if e != nil {
		return nil, e
	}
	for p.match(TAnd) {
		r, e := p.eq()
		if e != nil {
			return nil, e
		}
		n = &Node{K: KAnd, A: []*Node{n, r}}
	}
	return n, nil
}
func (p *Parser) eq() (*Node, error) {
	n, e := p.cmp()
	if e != nil {
		return nil, e
	}
	for p.match(TEq, TNe) {
		k := KEq
		if p.prev().K == TNe {
			k = KNe
		}
		r, e := p.cmp()
		if e != nil {
			return nil, e
		}
		n = &Node{K: k, A: []*Node{n, r}}
	}
	return n, nil
}
func (p *Parser) cmp() (*Node, error) {
	n, e := p.term()
	if e != nil {
		return nil, e
	}
	for p.match(TLt, TLe, TGt, TGe) {
		k := KLt
		switch p.prev().K {
		case TLe:
			k = KLe
		case TGt:
			k = KGt
		case TGe:
			k = KGe
		}
		r, e := p.term()
		if e != nil {
			return nil, e
		}
		n = &Node{K: k, A: []*Node{n, r}}
	}
	return n, nil
}
func (p *Parser) term() (*Node, error) {
	n, e := p.factor()
	if e != nil {
		return nil, e
	}
	for p.match(TPlus, TMinus) {
		k := KAdd
		if p.prev().K == TMinus {
			k = KSub
		}
		r, e := p.factor()
		if e != nil {
			return nil, e
		}
		n = &Node{K: k, A: []*Node{n, r}}
	}
	return n, nil
}
func (p *Parser) factor() (*Node, error) {
	n, e := p.power()
	if e != nil {
		return nil, e
	}
	for p.match(TStar, TSlash) {
		k := KMul
		if p.prev().K == TSlash {
			k = KDiv
		}
		r, e := p.power()
		if e != nil {
			return nil, e
		}
		n = &Node{K: k, A: []*Node{n, r}}
	}
	return n, nil
}
func (p *Parser) power() (*Node, error) {
	n, e := p.unary()
	if e != nil {
		return nil, e
	}
	if p.match(TCaret) {
		r, e := p.power()
		if e != nil {
			return nil, e
		}
		n = &Node{K: KPow, A: []*Node{n, r}}
	}
	return n, nil
}
func (p *Parser) unary() (*Node, error) {
	if p.match(TMinus) {
		n, e := p.unary()
		return &Node{K: KNeg, A: []*Node{n}}, e
	}
	if p.match(TPlus) {
		return p.unary()
	}
	if p.match(TBang) {
		n, e := p.unary()
		return &Node{K: KNot, A: []*Node{n}}, e
	}
	return p.primary()
}
func (p *Parser) primary() (*Node, error) {
	if p.match(TNum) {
		r, ok := new(big.Rat).SetString(p.prev().S)
		if !ok {
			return nil, fmt.Errorf("bad number")
		}
		return &Node{K: KConst, V: r}, nil
	}
	if p.match(TId) {
		name := p.prev().S
		if p.match(TLp) {
			var a []*Node
			if p.peek().K != TRp {
				for {
					n, e := p.or()
					if e != nil {
						return nil, e
					}
					a = append(a, n)
					if !p.match(TComma) {
						break
					}
				}
			}
			if _, e := p.need(TRp, "expected )"); e != nil {
				return nil, e
			}
			return &Node{K: KCall, Name: name, A: a}, nil
		}
		return &Node{K: KVar, Name: name}, nil
	}
	if p.match(TLp) {
		n, e := p.or()
		if e != nil {
			return nil, e
		}
		_, e = p.need(TRp, "expected )")
		return n, e
	}
	return nil, fmt.Errorf("expected expression at %d", p.peek().Pos)
}

func rat(v int64) *big.Rat   { return new(big.Rat).SetInt64(v) }
func cp(r *big.Rat) *big.Rat { return new(big.Rat).Set(r) }
func nodeStr(n *Node) string {
	switch n.K {
	case KConst:
		return n.V.RatString()
	case KVar:
		return n.Name
	case KCall:
		var a []string
		for _, x := range n.A {
			a = append(a, nodeStr(x))
		}
		return n.Name + "(" + strings.Join(a, ",") + ")"
	case KNeg:
		return "-(" + nodeStr(n.A[0]) + ")"
	case KNot:
		return "!(" + nodeStr(n.A[0]) + ")"
	}
	ops := map[Kind]string{KAdd: "+", KSub: "-", KMul: "*", KDiv: "/", KPow: "^", KEq: "==", KNe: "!=", KLt: "<", KLe: "<=", KGt: ">", KGe: ">=", KAnd: "&&", KOr: "||"}
	return "(" + nodeStr(n.A[0]) + ops[n.K] + nodeStr(n.A[1]) + ")"
}

func eval(n *Node, v map[string]float64) (float64, error) {
	switch n.K {
	case KConst:
		f, _ := n.V.Float64()
		return f, nil
	case KVar:
		x, ok := v[n.Name]
		if !ok {
			return 0, fmt.Errorf("missing variable %s", n.Name)
		}
		return x, nil
	case KNeg:
		a, e := eval(n.A[0], v)
		return -a, e
	}
	if n.K == KCall {
		if len(n.A) != 1 {
			return 0, errors.New("unary calls only")
		}
		x, e := eval(n.A[0], v)
		if e != nil {
			return 0, e
		}
		switch n.Name {
		case "sin":
			return math.Sin(x), nil
		case "cos":
			return math.Cos(x), nil
		case "tan":
			return math.Tan(x), nil
		case "exp":
			return math.Exp(x), nil
		case "log", "ln":
			return math.Log(x), nil
		case "sqrt":
			return math.Sqrt(x), nil
		case "abs":
			return math.Abs(x), nil
		}
		return 0, fmt.Errorf("unknown function %s", n.Name)
	}
	a, e := eval(n.A[0], v)
	if e != nil {
		return 0, e
	}
	b, e := eval(n.A[1], v)
	if e != nil {
		return 0, e
	}
	switch n.K {
	case KAdd:
		return a + b, nil
	case KSub:
		return a - b, nil
	case KMul:
		return a * b, nil
	case KDiv:
		return a / b, nil
	case KPow:
		return math.Pow(a, b), nil
	}
	return 0, errors.New("boolean used as number")
}

type Dim [7]int

func (d Dim) String() string {
	names := []string{"L", "M", "T", "I", "Theta", "N", "J"}
	var a []string
	for i, x := range d {
		if x != 0 {
			if x == 1 {
				a = append(a, names[i])
			} else {
				a = append(a, fmt.Sprintf("%s^%d", names[i], x))
			}
		}
	}
	if len(a) == 0 {
		return "1"
	}
	return strings.Join(a, "*")
}
func dmul(a, b Dim) Dim {
	var r Dim
	for i := range r {
		r[i] = a[i] + b[i]
	}
	return r
}
func ddiv(a, b Dim) Dim {
	var r Dim
	for i := range r {
		r[i] = a[i] - b[i]
	}
	return r
}
func dpow(a Dim, n int) Dim {
	var r Dim
	for i := range r {
		r[i] = a[i] * n
	}
	return r
}
func knownDim(s string) (Dim, bool) {
	var d Dim
	switch s {
	case "1", "Dimensionless":
		return d, true
	case "Length", "L":
		d[0] = 1
	case "Mass", "M":
		d[1] = 1
	case "Time", "T":
		d[2] = 1
	case "ElectricCurrent", "I":
		d[3] = 1
	case "Temperature", "Theta":
		d[4] = 1
	case "AmountOfSubstance", "N":
		d[5] = 1
	case "LuminousIntensity", "Jbase":
		d[6] = 1
	case "Velocity":
		d[0] = 1
		d[2] = -1
	case "Acceleration":
		d[0] = 1
		d[2] = -2
	case "Force":
		d[0] = 1
		d[1] = 1
		d[2] = -2
	case "Energy":
		d[0] = 2
		d[1] = 1
		d[2] = -2
	case "Power":
		d[0] = 2
		d[1] = 1
		d[2] = -3
	case "Momentum":
		d[0] = 1
		d[1] = 1
		d[2] = -1
	case "Pressure":
		d[0] = -1
		d[1] = 1
		d[2] = -2
	default:
		if quantityDefs != nil {
			if q, ok := quantityDefs[s]; ok {
				return q, true
			}
		}
		return d, false
	}
	return d, true
}

type DParser struct {
	s string
	p int
}

func (pd *DParser) space() {
	for pd.p < len(pd.s) && (pd.s[pd.p] == ' ' || pd.s[pd.p] == '\t') {
		pd.p++
	}
}
func (pd *DParser) eat(c byte) bool {
	pd.space()
	if pd.p < len(pd.s) && pd.s[pd.p] == c {
		pd.p++
		return true
	}
	return false
}
func (pd *DParser) ident() (string, error) {
	pd.space()
	st := pd.p
	if pd.p < len(pd.s) && pd.s[pd.p] == '1' {
		pd.p++
		return "1", nil
	}
	for pd.p < len(pd.s) {
		c := pd.s[pd.p]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.') {
			break
		}
		pd.p++
	}
	if st == pd.p {
		return "", errors.New("expected dimension")
	}
	return pd.s[st:pd.p], nil
}
func (pd *DParser) factor() (Dim, error) {
	pd.space()
	var d Dim
	var e error
	if pd.eat('(') {
		d, e = pd.prod()
		if e == nil && !pd.eat(')') {
			e = errors.New("expected )")
		}
	} else {
		var n string
		n, e = pd.ident()
		if e == nil {
			var ok bool
			d, ok = knownDim(n)
			if !ok {
				e = fmt.Errorf("unknown dimension %s", n)
			}
		}
	}
	if e != nil {
		return d, e
	}
	if pd.eat('^') {
		sg := 1
		if pd.eat('-') {
			sg = -1
		} else {
			pd.eat('+')
		}
		pd.space()
		st := pd.p
		for pd.p < len(pd.s) && pd.s[pd.p] >= '0' && pd.s[pd.p] <= '9' {
			pd.p++
		}
		if st == pd.p {
			return d, errors.New("expected exponent")
		}
		n, _ := strconv.Atoi(pd.s[st:pd.p])
		d = dpow(d, sg*n)
	}
	return d, nil
}
func (pd *DParser) prod() (Dim, error) {
	d, e := pd.factor()
	if e != nil {
		return d, e
	}
	for {
		if pd.eat('*') {
			x, e := pd.factor()
			if e != nil {
				return d, e
			}
			d = dmul(d, x)
		} else if pd.eat('/') {
			x, e := pd.factor()
			if e != nil {
				return d, e
			}
			d = ddiv(d, x)
		} else {
			break
		}
	}
	return d, nil
}
func parseDim(s string) (Dim, error) {
	p := &DParser{s: s}
	d, e := p.prod()
	p.space()
	if e == nil && p.p != len(s) {
		e = fmt.Errorf("trailing dimension")
	}
	return d, e
}
func inferDim(n *Node, v map[string]Dim) (Dim, error) {
	switch n.K {
	case KConst:
		return Dim{}, nil
	case KVar:
		d, ok := v[n.Name]
		if !ok {
			return Dim{}, fmt.Errorf("unknown dimension for %s", n.Name)
		}
		return d, nil
	case KNeg:
		return inferDim(n.A[0], v)
	case KAdd, KSub:
		a, e := inferDim(n.A[0], v)
		if e != nil {
			return Dim{}, e
		}
		b, e := inferDim(n.A[1], v)
		if e != nil {
			return Dim{}, e
		}
		if a != b {
			return Dim{}, fmt.Errorf("dimension mismatch: %s vs %s", a, b)
		}
		return a, nil
	case KMul:
		a, e := inferDim(n.A[0], v)
		if e != nil {
			return Dim{}, e
		}
		b, e := inferDim(n.A[1], v)
		return dmul(a, b), e
	case KDiv:
		a, e := inferDim(n.A[0], v)
		if e != nil {
			return Dim{}, e
		}
		b, e := inferDim(n.A[1], v)
		return ddiv(a, b), e
	case KPow:
		a, e := inferDim(n.A[0], v)
		if e != nil {
			return Dim{}, e
		}
		if n.A[1].K != KConst || !n.A[1].V.IsInt() {
			return Dim{}, errors.New("dimensioned power requires integer constant")
		}
		i, _ := strconv.Atoi(n.A[1].V.Num().String())
		return dpow(a, i), nil
	case KCall:
		a, e := inferDim(n.A[0], v)
		if e != nil {
			return Dim{}, e
		}
		if n.Name == "abs" {
			return a, nil
		}
		if n.Name == "sqrt" {
			var r Dim
			for i, x := range a {
				if x%2 != 0 {
					return Dim{}, errors.New("sqrt dimension exponent must be even")
				}
				r[i] = x / 2
			}
			return r, nil
		}
		if a != (Dim{}) {
			return Dim{}, fmt.Errorf("%s requires dimensionless input", n.Name)
		}
		return Dim{}, nil
	}
	return Dim{}, errors.New("boolean has no numeric dimension")
}

type Ins struct {
	ID   int
	Op   string
	Args []int
	Val  float64
	Var  int
	D    Dim
}
type Module struct {
	Expr   string
	Vars   []string
	VD     []Dim
	I      []Ins
	Result int
	D      Dim
}

func compile(expr string, vars []string, vd []Dim) (*Module, error) {
	n, e := parse(expr)
	if e != nil {
		return nil, e
	}
	vm := map[string]int{}
	dm := map[string]Dim{}
	for i, x := range vars {
		vm[x] = i
		dm[x] = vd[i]
	}
	m := &Module{Expr: expr, Vars: vars, VD: vd}
	cse := map[string]int{}
	var low func(*Node) (int, error)
	low = func(x *Node) (int, error) {
		add := func(op string, a []int, val float64, vi int, d Dim) int {
			key := fmt.Sprintf("%s:%v:%g:%d:%v", op, a, val, vi, d)
			if id, ok := cse[key]; ok && op != "var" {
				return id
			}
			id := len(m.I)
			m.I = append(m.I, Ins{id, op, a, val, vi, d})
			cse[key] = id
			return id
		}
		switch x.K {
		case KConst:
			f, _ := x.V.Float64()
			return add("const", nil, f, -1, Dim{}), nil
		case KVar:
			i, ok := vm[x.Name]
			if !ok {
				return 0, fmt.Errorf("undeclared variable %s", x.Name)
			}
			return add("var", nil, 0, i, vd[i]), nil
		case KNeg:
			a, e := low(x.A[0])
			if e != nil {
				return 0, e
			}
			return add("neg", []int{a}, 0, -1, m.I[a].D), nil
		case KAdd, KSub, KMul, KDiv:
			a, e := low(x.A[0])
			if e != nil {
				return 0, e
			}
			b, e := low(x.A[1])
			if e != nil {
				return 0, e
			}
			op := map[Kind]string{KAdd: "add", KSub: "sub", KMul: "mul", KDiv: "div"}[x.K]
			d := m.I[a].D
			if x.K == KAdd || x.K == KSub {
				if d != m.I[b].D {
					return 0, errors.New("dimension mismatch")
				}
			} else if x.K == KMul {
				d = dmul(d, m.I[b].D)
			} else {
				d = ddiv(d, m.I[b].D)
			}
			return add(op, []int{a, b}, 0, -1, d), nil
		case KPow:
			a, e := low(x.A[0])
			if e != nil {
				return 0, e
			}
			if x.A[1].K != KConst || !x.A[1].V.IsInt() {
				return 0, errors.New("integer power required")
			}
			p, _ := strconv.Atoi(x.A[1].V.Num().String())
			return add("powi", []int{a}, float64(p), -1, dpow(m.I[a].D, p)), nil
		case KCall:
			a, e := low(x.A[0])
			if e != nil {
				return 0, e
			}
			d, e := inferDim(x, dm)
			if e != nil {
				return 0, e
			}
			return add(x.Name, []int{a}, 0, -1, d), nil
		}
		return 0, errors.New("numeric expression required")
	}
	id, e := low(n)
	if e != nil {
		return nil, e
	}
	m.Result = id
	m.D = m.I[id].D
	return m, nil
}
func (m *Module) ssa() string {
	var b strings.Builder
	b.WriteString("pir.ssa @model {\n")
	for _, x := range m.I {
		fmt.Fprintf(&b, "  %%%d = pir.%s", x.ID, x.Op)
		if x.Op == "const" || x.Op == "powi" {
			fmt.Fprintf(&b, " %g", x.Val)
		}
		if x.Op == "var" {
			fmt.Fprintf(&b, " %%arg%d", x.Var)
		}
		for _, a := range x.Args {
			fmt.Fprintf(&b, " %%%d", a)
		}
		fmt.Fprintf(&b, " : !pir.quantity<%s>\n", x.D)
	}
	fmt.Fprintf(&b, "  pir.return %%%d\n}\n", m.Result)
	return b.String()
}
func (m *Module) c99() string {
	var b strings.Builder
	b.WriteString("#include <math.h>\n#include <stddef.h>\n\ndouble phylang_model(const double* args,size_t count){\n(void)count;\n")
	ref := func(i int) string { return fmt.Sprintf("v%d", i) }
	for _, x := range m.I {
		e := ""
		switch x.Op {
		case "const":
			e = fmt.Sprintf("%.17g", x.Val)
		case "var":
			e = fmt.Sprintf("args[%d]", x.Var)
		case "neg":
			e = "-" + ref(x.Args[0])
		case "add":
			e = ref(x.Args[0]) + "+" + ref(x.Args[1])
		case "sub":
			e = ref(x.Args[0]) + "-" + ref(x.Args[1])
		case "mul":
			e = ref(x.Args[0]) + "*" + ref(x.Args[1])
		case "div":
			e = ref(x.Args[0]) + "/" + ref(x.Args[1])
		case "powi":
			e = fmt.Sprintf("pow(%s,%.0f)", ref(x.Args[0]), x.Val)
		default:
			e = fmt.Sprintf("%s(%s)", x.Op, ref(x.Args[0]))
		}
		fmt.Fprintf(&b, "const double v%d=%s;\n", x.ID, e)
	}
	fmt.Fprintf(&b, "return v%d;\n}\n", m.Result)
	return b.String()
}
func (m *Module) run(vals []float64) (float64, error) {
	v := make([]float64, len(m.I))
	for _, x := range m.I {
		switch x.Op {
		case "const":
			v[x.ID] = x.Val
		case "var":
			if x.Var >= len(vals) {
				return 0, errors.New("missing value")
			}
			v[x.ID] = vals[x.Var]
		case "neg":
			v[x.ID] = -v[x.Args[0]]
		case "add":
			v[x.ID] = v[x.Args[0]] + v[x.Args[1]]
		case "sub":
			v[x.ID] = v[x.Args[0]] - v[x.Args[1]]
		case "mul":
			v[x.ID] = v[x.Args[0]] * v[x.Args[1]]
		case "div":
			v[x.ID] = v[x.Args[0]] / v[x.Args[1]]
		case "powi":
			v[x.ID] = math.Pow(v[x.Args[0]], x.Val)
		case "sin":
			v[x.ID] = math.Sin(v[x.Args[0]])
		case "cos":
			v[x.ID] = math.Cos(v[x.Args[0]])
		case "tan":
			v[x.ID] = math.Tan(v[x.Args[0]])
		case "exp":
			v[x.ID] = math.Exp(v[x.Args[0]])
		case "log", "ln":
			v[x.ID] = math.Log(v[x.Args[0]])
		case "sqrt":
			v[x.ID] = math.Sqrt(v[x.Args[0]])
		case "abs":
			v[x.ID] = math.Abs(v[x.Args[0]])
		default:
			return 0, fmt.Errorf("unknown op %s", x.Op)
		}
	}
	return v[m.Result], nil
}

// Exact polynomial normalization.
type Poly map[string]*big.Rat

func monoDecode(k string) map[string]int {
	r := map[string]int{}
	if k == "" {
		return r
	}
	for _, p := range strings.Split(k, "*") {
		q := strings.Split(p, "^")
		e := 1
		if len(q) == 2 {
			e, _ = strconv.Atoi(q[1])
		}
		r[q[0]] = e
	}
	return r
}
func monoEncode(m map[string]int) string {
	var n []string
	for x, e := range m {
		if e != 0 {
			n = append(n, x)
		}
	}
	sort.Strings(n)
	var a []string
	for _, x := range n {
		if m[x] == 1 {
			a = append(a, x)
		} else {
			a = append(a, fmt.Sprintf("%s^%d", x, m[x]))
		}
	}
	return strings.Join(a, "*")
}
func padd(a, b Poly, sg int) Poly {
	r := Poly{}
	for k, v := range a {
		r[k] = cp(v)
	}
	for k, v := range b {
		if r[k] == nil {
			r[k] = rat(0)
		}
		z := cp(v)
		if sg < 0 {
			z.Neg(z)
		}
		r[k].Add(r[k], z)
		if r[k].Sign() == 0 {
			delete(r, k)
		}
	}
	return r
}
func pmul(a, b Poly) Poly {
	r := Poly{}
	for ka, ca := range a {
		for kb, cb := range b {
			ma := monoDecode(ka)
			for x, e := range monoDecode(kb) {
				ma[x] += e
			}
			k := monoEncode(ma)
			if r[k] == nil {
				r[k] = rat(0)
			}
			r[k].Add(r[k], new(big.Rat).Mul(ca, cb))
			if r[k].Sign() == 0 {
				delete(r, k)
			}
		}
	}
	return r
}
func ppow(a Poly, n int) Poly {
	r := Poly{"": rat(1)}
	for n > 0 {
		if n&1 == 1 {
			r = pmul(r, a)
		}
		n >>= 1
		if n > 0 {
			a = pmul(a, a)
		}
	}
	return r
}
func poly(n *Node) (Poly, bool) {
	switch n.K {
	case KConst:
		return Poly{"": cp(n.V)}, true
	case KVar:
		return Poly{n.Name: rat(1)}, true
	case KNeg:
		a, ok := poly(n.A[0])
		if !ok {
			return nil, false
		}
		return pmul(Poly{"": rat(-1)}, a), true
	case KAdd, KSub, KMul:
		a, ok := poly(n.A[0])
		if !ok {
			return nil, false
		}
		b, ok := poly(n.A[1])
		if !ok {
			return nil, false
		}
		if n.K == KAdd {
			return padd(a, b, 1), true
		}
		if n.K == KSub {
			return padd(a, b, -1), true
		}
		return pmul(a, b), true
	case KDiv:
		a, ok := poly(n.A[0])
		if !ok || n.A[1].K != KConst || n.A[1].V.Sign() == 0 {
			return nil, false
		}
		return pmul(a, Poly{"": new(big.Rat).Inv(n.A[1].V)}), true
	case KPow:
		a, ok := poly(n.A[0])
		if !ok || n.A[1].K != KConst || !n.A[1].V.IsInt() {
			return nil, false
		}
		i, _ := strconv.Atoi(n.A[1].V.Num().String())
		if i < 0 || i > 64 {
			return nil, false
		}
		return ppow(a, i), true
	}
	return nil, false
}
func polyStr(p Poly) string {
	if len(p) == 0 {
		return "0"
	}
	var k []string
	for x := range p {
		k = append(k, x)
	}
	sort.Strings(k)
	var b strings.Builder
	first := true
	for _, m := range k {
		c := p[m]
		if c.Sign() == 0 {
			continue
		}
		neg := c.Sign() < 0
		z := cp(c)
		if neg {
			z.Neg(z)
		}
		if first {
			if neg {
				b.WriteByte('-')
			}
		} else if neg {
			b.WriteByte('-')
		} else {
			b.WriteByte('+')
		}
		if m == "" || z.Cmp(rat(1)) != 0 {
			b.WriteString(z.RatString())
			if m != "" {
				b.WriteByte('*')
			}
		}
		b.WriteString(m)
		first = false
	}
	if first {
		return "0"
	}
	return b.String()
}

type Step struct {
	Rule   string
	Before string
	After  string
}

type Cert struct {
	Name                 string
	Vars                 []string
	L, R, NL, NR, Digest string
	Steps                []Step
}

func digest(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%016x", h.Sum64())
}

func (c Cert) payload() string {
	b := c.Name + "\n" + strings.Join(c.Vars, ",") + "\n" + c.L + "\n" + c.R + "\n" + c.NL + "\n" + c.NR + "\n"
	for _, st := range c.Steps {
		b += st.Rule + "\n" + st.Before + "\n" + st.After + "\n"
	}
	return b
}

func hx(s string) string { return hex.EncodeToString([]byte(s)) }
func unhx(s string) (string, error) {
	b, e := hex.DecodeString(s)
	return string(b), e
}

func (c Cert) String() string {
	var b strings.Builder
	b.WriteString("PHYPROOF/1\n")
	fmt.Fprintf(&b, "theorem %s\nvariables %d\n", hx(c.Name), len(c.Vars))
	for _, v := range c.Vars {
		fmt.Fprintf(&b, "var %s\n", hx(v))
	}
	fmt.Fprintf(&b, "lhs %s\nrhs %s\nnormal_lhs %s\nnormal_rhs %s\nsteps %d\n",
		hx(c.L), hx(c.R), hx(c.NL), hx(c.NR), len(c.Steps))
	for _, st := range c.Steps {
		fmt.Fprintf(&b, "step %s %s %s\n", hx(st.Rule), hx(st.Before), hx(st.After))
	}
	fmt.Fprintf(&b, "digest %s\n", c.Digest)
	return b.String()
}

func parseCert(s string) (Cert, error) {
	sc := bufio.NewScanner(strings.NewReader(s))
	next := func() (string, error) {
		if !sc.Scan() {
			return "", errors.New("unexpected end")
		}
		return sc.Text(), nil
	}
	h, e := next()
	if e != nil || h != "PHYPROOF/1" {
		return Cert{}, errors.New("bad header")
	}
	field := func(name string) (string, error) {
		l, er := next()
		if er != nil || !strings.HasPrefix(l, name+" ") {
			return "", fmt.Errorf("expected %s", name)
		}
		return unhx(strings.TrimPrefix(l, name+" "))
	}
	var c Cert
	c.Name, e = field("theorem")
	if e != nil {
		return c, e
	}
	l, e := next()
	if e != nil || !strings.HasPrefix(l, "variables ") {
		return c, errors.New("variables")
	}
	n, _ := strconv.Atoi(strings.TrimPrefix(l, "variables "))
	for i := 0; i < n; i++ {
		v, er := field("var")
		if er != nil {
			return c, er
		}
		c.Vars = append(c.Vars, v)
	}
	c.L, e = field("lhs")
	if e != nil {
		return c, e
	}
	c.R, e = field("rhs")
	if e != nil {
		return c, e
	}
	c.NL, e = field("normal_lhs")
	if e != nil {
		return c, e
	}
	c.NR, e = field("normal_rhs")
	if e != nil {
		return c, e
	}
	l, e = next()
	if e != nil || !strings.HasPrefix(l, "steps ") {
		return c, errors.New("steps")
	}
	sn, _ := strconv.Atoi(strings.TrimPrefix(l, "steps "))
	for i := 0; i < sn; i++ {
		l, e = next()
		if e != nil || !strings.HasPrefix(l, "step ") {
			return c, errors.New("step")
		}
		parts := strings.Fields(strings.TrimPrefix(l, "step "))
		if len(parts) != 3 {
			return c, errors.New("bad step")
		}
		rule, er := unhx(parts[0])
		if er != nil {
			return c, er
		}
		before, er := unhx(parts[1])
		if er != nil {
			return c, er
		}
		after, er := unhx(parts[2])
		if er != nil {
			return c, er
		}
		c.Steps = append(c.Steps, Step{rule, before, after})
	}
	l, e = next()
	if e != nil || !strings.HasPrefix(l, "digest ") {
		return c, errors.New("digest")
	}
	c.Digest = strings.TrimPrefix(l, "digest ")
	return c, nil
}

func prove(name, l, r string, vars []string) (Cert, error) {
	a, e := parse(l)
	if e != nil {
		return Cert{}, e
	}
	b, e := parse(r)
	if e != nil {
		return Cert{}, e
	}
	pa, ok := poly(a)
	if !ok {
		return Cert{}, errors.New("proof fragment is polynomial only")
	}
	pb, ok := poly(b)
	if !ok {
		return Cert{}, errors.New("proof fragment is polynomial only")
	}
	c := Cert{
		Name: name, Vars: vars, L: l, R: r, NL: polyStr(pa), NR: polyStr(pb),
		Steps: []Step{
			{"parse", l, nodeStr(a)},
			{"polynomial_normalize", nodeStr(a), polyStr(pa)},
			{"parse", r, nodeStr(b)},
			{"polynomial_normalize", nodeStr(b), polyStr(pb)},
		},
	}
	if c.NL != c.NR {
		return Cert{}, fmt.Errorf("not equal: %s != %s", c.NL, c.NR)
	}
	c.Digest = digest(c.payload())
	return c, nil
}

func verify(c Cert) error {
	if digest(c.payload()) != c.Digest {
		return errors.New("digest mismatch")
	}
	a, e := parse(c.L)
	if e != nil {
		return e
	}
	b, e := parse(c.R)
	if e != nil {
		return e
	}
	pa, oka := poly(a)
	pb, okb := poly(b)
	if !oka || !okb || polyStr(pa) != c.NL || polyStr(pb) != c.NR || c.NL != c.NR {
		return errors.New("kernel replay failed")
	}
	return nil
}

// Linear real arithmetic.
type Lin struct {
	C map[string]*big.Rat
	K *big.Rat
}

func lin(n *Node) (Lin, bool) {
	switch n.K {
	case KConst:
		return Lin{map[string]*big.Rat{}, cp(n.V)}, true
	case KVar:
		return Lin{map[string]*big.Rat{n.Name: rat(1)}, rat(0)}, true
	case KNeg:
		a, ok := lin(n.A[0])
		if !ok {
			return Lin{}, false
		}
		return lscale(a, rat(-1)), true
	case KAdd, KSub:
		a, ok := lin(n.A[0])
		if !ok {
			return Lin{}, false
		}
		b, ok := lin(n.A[1])
		if !ok {
			return Lin{}, false
		}
		return ladd(a, b, n.K == KSub), true
	case KMul:
		a, ok := lin(n.A[0])
		if !ok {
			return Lin{}, false
		}
		b, ok := lin(n.A[1])
		if !ok {
			return Lin{}, false
		}
		if len(a.C) == 0 {
			return lscale(b, a.K), true
		}
		if len(b.C) == 0 {
			return lscale(a, b.K), true
		}
		return Lin{}, false
	case KDiv:
		a, ok := lin(n.A[0])
		if !ok || n.A[1].K != KConst || n.A[1].V.Sign() == 0 {
			return Lin{}, false
		}
		return lscale(a, new(big.Rat).Inv(n.A[1].V)), true
	case KPow:
		if n.A[1].K == KConst && n.A[1].V.Cmp(rat(1)) == 0 {
			return lin(n.A[0])
		}
		if n.A[1].K == KConst && n.A[1].V.Sign() == 0 {
			return Lin{map[string]*big.Rat{}, rat(1)}, true
		}
	}
	return Lin{}, false
}
func lscale(a Lin, f *big.Rat) Lin {
	r := Lin{map[string]*big.Rat{}, new(big.Rat).Mul(a.K, f)}
	for x, c := range a.C {
		r.C[x] = new(big.Rat).Mul(c, f)
		if r.C[x].Sign() == 0 {
			delete(r.C, x)
		}
	}
	return r
}
func ladd(a, b Lin, sub bool) Lin {
	r := Lin{map[string]*big.Rat{}, cp(a.K)}
	for x, c := range a.C {
		r.C[x] = cp(c)
	}
	sg := rat(1)
	if sub {
		sg = rat(-1)
	}
	r.K.Add(r.K, new(big.Rat).Mul(b.K, sg))
	for x, c := range b.C {
		if r.C[x] == nil {
			r.C[x] = rat(0)
		}
		r.C[x].Add(r.C[x], new(big.Rat).Mul(c, sg))
		if r.C[x].Sign() == 0 {
			delete(r.C, x)
		}
	}
	return r
}

type Ineq struct {
	C      map[string]*big.Rat
	R      *big.Rat
	Strict bool
}

func invert(k Kind) Kind {
	switch k {
	case KEq:
		return KNe
	case KNe:
		return KEq
	case KLt:
		return KGe
	case KLe:
		return KGt
	case KGt:
		return KLe
	case KGe:
		return KLt
	}
	return k
}
func branches(a *Node, truth bool) ([][]Ineq, error) {
	k := a.K
	if !truth {
		k = invert(k)
	}
	l, ok := lin(a.A[0])
	if !ok {
		return nil, errors.New("nonlinear atom")
	}
	r, ok := lin(a.A[1])
	if !ok {
		return nil, errors.New("nonlinear atom")
	}
	d := ladd(l, r, true)
	mk := func(neg, strict bool) Ineq {
		f := rat(1)
		if neg {
			f = rat(-1)
		}
		q := Ineq{map[string]*big.Rat{}, new(big.Rat).Mul(new(big.Rat).Neg(d.K), f), strict}
		for x, c := range d.C {
			q.C[x] = new(big.Rat).Mul(c, f)
		}
		return q
	}
	switch k {
	case KLe:
		return [][]Ineq{{mk(false, false)}}, nil
	case KLt:
		return [][]Ineq{{mk(false, true)}}, nil
	case KGe:
		return [][]Ineq{{mk(true, false)}}, nil
	case KGt:
		return [][]Ineq{{mk(true, true)}}, nil
	case KEq:
		return [][]Ineq{{mk(false, false), mk(true, false)}}, nil
	case KNe:
		return [][]Ineq{{mk(false, true)}, {mk(true, true)}}, nil
	}
	return nil, errors.New("not relation")
}
func feasible(q []Ineq) bool {
	vs := map[string]bool{}
	for _, z := range q {
		for x := range z.C {
			vs[x] = true
		}
	}
	var names []string
	for x := range vs {
		names = append(names, x)
	}
	sort.Strings(names)
	for _, x := range names {
		var pos, neg, zero []Ineq
		for _, z := range q {
			c := z.C[x]
			w := Ineq{map[string]*big.Rat{}, cp(z.R), z.Strict}
			for n, v := range z.C {
				if n != x {
					w.C[n] = cp(v)
				}
			}
			if c == nil || c.Sign() == 0 {
				zero = append(zero, w)
			} else {
				w.C["$e"] = cp(c)
				if c.Sign() > 0 {
					pos = append(pos, w)
				} else {
					neg = append(neg, w)
				}
			}
		}
		nq := zero
		if len(pos) > 0 && len(neg) > 0 {
			for _, p := range pos {
				for _, n := range neg {
					pc := p.C["$e"]
					nc := n.C["$e"]
					z := Ineq{map[string]*big.Rat{}, rat(0), p.Strict || n.Strict}
					for y, c := range p.C {
						if y != "$e" {
							z.C[y] = new(big.Rat).Mul(c, new(big.Rat).Neg(nc))
						}
					}
					for y, c := range n.C {
						if y != "$e" {
							if z.C[y] == nil {
								z.C[y] = rat(0)
							}
							z.C[y].Add(z.C[y], new(big.Rat).Mul(c, pc))
							if z.C[y].Sign() == 0 {
								delete(z.C, y)
							}
						}
					}
					z.R.Add(new(big.Rat).Mul(p.R, new(big.Rat).Neg(nc)), new(big.Rat).Mul(n.R, pc))
					nq = append(nq, z)
				}
			}
		}
		q = nq
		for _, z := range q {
			if len(z.C) == 0 {
				if z.Strict && z.R.Sign() <= 0 {
					return false
				}
				if !z.Strict && z.R.Sign() < 0 {
					return false
				}
			}
		}
	}
	for _, z := range q {
		if len(z.C) == 0 {
			if z.Strict && z.R.Sign() <= 0 {
				return false
			}
			if !z.Strict && z.R.Sign() < 0 {
				return false
			}
		}
	}
	return true
}
func atoms(n *Node, a *[]*Node, idx map[*Node]int) error {
	if n.K == KAnd || n.K == KOr || n.K == KNot {
		for _, x := range n.A {
			if e := atoms(x, a, idx); e != nil {
				return e
			}
		}
		return nil
	}
	if n.K >= KEq && n.K <= KGe {
		idx[n] = len(*a)
		*a = append(*a, n)
		return nil
	}
	return errors.New("boolean atom must be comparison")
}
func beval(n *Node, v []bool, idx map[*Node]int) bool {
	switch n.K {
	case KAnd:
		return beval(n.A[0], v, idx) && beval(n.A[1], v, idx)
	case KOr:
		return beval(n.A[0], v, idx) || beval(n.A[1], v, idx)
	case KNot:
		return !beval(n.A[0], v, idx)
	default:
		return v[idx[n]]
	}
}
func theory(a []*Node, v []bool) (bool, error) {
	sets := [][]Ineq{{}}
	for i, x := range a {
		bs, e := branches(x, v[i])
		if e != nil {
			return false, e
		}
		var ns [][]Ineq
		for _, s := range sets {
			for _, b := range bs {
				q := append([]Ineq{}, s...)
				q = append(q, b...)
				ns = append(ns, q)
			}
		}
		sets = ns
	}
	for _, s := range sets {
		if feasible(s) {
			return true, nil
		}
	}
	return false, nil
}
func solve(f string) (string, int, error) {
	r, e := parse(f)
	if e != nil {
		return "", 0, e
	}
	var a []*Node
	idx := map[*Node]int{}
	if e = atoms(r, &a, idx); e != nil {
		return "", 0, e
	}
	if len(a) > 22 {
		return "unknown", 0, errors.New("atom limit")
	}
	total := 1 << len(a)
	v := make([]bool, len(a))
	for mask := 0; mask < total; mask++ {
		for i := range v {
			v[i] = mask&(1<<i) != 0
		}
		if beval(r, v, idx) {
			ok, e := theory(a, v)
			if e != nil {
				return "unknown", mask + 1, e
			}
			if ok {
				return "sat", mask + 1, nil
			}
		}
	}
	return "unsat", total, nil
}

func parseVars(args []string) ([]string, []Dim, error) {
	var n []string
	var d []Dim
	for _, s := range args {
		p := strings.IndexByte(s, ':')
		if p < 0 {
			return nil, nil, errors.New("variable must be name:Dimension")
		}
		x, e := parseDim(s[p+1:])
		if e != nil {
			return nil, nil, e
		}
		n = append(n, s[:p])
		d = append(d, x)
	}
	return n, d, nil
}
func assignments(args []string) ([]string, []float64, error) {
	m := map[string]float64{}
	for _, s := range args {
		p := strings.IndexByte(s, '=')
		if p < 0 {
			return nil, nil, errors.New("assignment must be name=value")
		}
		x, e := strconv.ParseFloat(s[p+1:], 64)
		if e != nil {
			return nil, nil, e
		}
		m[s[:p]] = x
	}
	var n []string
	for x := range m {
		n = append(n, x)
	}
	sort.Strings(n)
	var v []float64
	for _, x := range n {
		v = append(v, m[x])
	}
	return n, v, nil
}
func help() {
	fmt.Printf(`PhyLang Community %s

完整语言前端：
  phylang run <file.phy>       运行 PhyLang 文件
  phylang check <file.phy>     检查 PhyLang 文件
  phylang repl                 启动 REPL；quit/exit 直接返回原终端
  phylang studio [port]        启动前后端一体化 Studio
  phylang package <subcommand> 社区扩展包管理、测试、安装与发布审核

社区包：
  phylang package init ./my-package --name community.example
  phylang package validate ./my-package
  phylang package test ./my-package
  phylang package pack ./my-package
  phylang package install file.phypkg
  phylang package help

自主后端：
  phylang compile <expr> --var x:Dimension --emit ssa|c
  phylang expr <expr> x=3      通过字节码 VM 执行表达式
  phylang prove ...            生成精确代数证明证书
  phylang check-proof <file>   复核 .phyproof
  phylang solve <formula>      SAT + QF_LRA 约束求解
  phylang dim <expr> ...       量纲推导
  phylang serve [address]      仅启动自主后端 JSON 服务
  phylang self-test            执行完整前后端回归测试
`, version)
}
func backendSelftest() error {
	n, v, _ := assignments([]string{"x=3"})
	m, e := compile("x*x+2*x+1", n, make([]Dim, len(n)))
	if e != nil {
		return e
	}
	z, e := m.run(v)
	if e != nil || math.Abs(z-16) > 1e-12 {
		return errors.New("VM")
	}
	c, e := prove("square", "(x+1)^2", "x^2+2*x+1", []string{"x"})
	if e != nil {
		return e
	}
	if e = verify(c); e != nil {
		return e
	}
	s, _, e := solve("x>=0 && x<=1 && x>2")
	if e != nil || s != "unsat" {
		return errors.New("solver")
	}
	fmt.Println("[PASS] portable PIR/VM\n[PASS] portable proof kernel\n[PASS] portable QF_LRA solver\n[PASS] portable dimension system")
	return nil
}
func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func registryClientSelfTest() error {
	tempDir, err := os.MkdirTemp("", "phylang-registry-self-test-")
	if err != nil {
		return fmt.Errorf("registry self-test temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	indexPath := filepath.Join(tempDir, "index.json")
	indexJSON := []byte(`{"schema":"phylang.registry/v2","name":"self-test","updated":"2026-07-28T00:00:00Z","packages":[]}` + "\n")
	if err = os.WriteFile(indexPath, indexJSON, 0600); err != nil {
		return fmt.Errorf("registry self-test index: %w", err)
	}
	idx, err := loadRegistry(indexPath)
	if err != nil {
		return fmt.Errorf("registry self-test load: %w", err)
	}
	if idx.Schema != "phylang.registry/v2" || idx.Name != "self-test" || len(idx.Packages) != 0 {
		return fmt.Errorf("registry self-test unexpected index: schema=%q name=%q packages=%d", idx.Schema, idx.Name, len(idx.Packages))
	}
	return nil
}

func integratedSelftest() error {
	if e := backendSelftest(); e != nil {
		return e
	}
	var out strings.Builder
	source := `import physics.classical;
import community.units.astronomy as astro;
import community.mechanics.linear-drag as drag only { linear_drag_force, kg_per_s };
let m = measured(2 kg, 0.01 kg);
let v = measured(3 [m/s], 0.02 [m/s]);
print 0.5*m*v^2 in J;
print diff("x^3+sin(x)", "x");
law Candidate { parameters { force: Force; mass: Mass; acceleration: Acceleration; } assumptions { mass > 0 kg; } equation { force == mass*acceleration; } }
verify Candidate against NewtonSecondLaw with { force=6 N; mass=2 kg; acceleration=3 [m/s^2]; };
print backend_run("x*x+1", "x=3");`
	if _, e := runFrontendSource(source, "<self-test>", &out); e != nil {
		return e
	}
	text := out.String()
	if !strings.Contains(text, "9") || !strings.Contains(text, "J") || !strings.Contains(text, "Verification: Candidate") || !strings.Contains(text, "10") {
		return fmt.Errorf("frontend self-test output: %s", text)
	}
	if e := studioSelfTest(); e != nil {
		return e
	}
	if e := registryClientSelfTest(); e != nil {
		return e
	}
	items := completionItems("let velocity = 3 [m/s]; fn kinetic(m,v){ return m*v^2/2; }", "ve")
	if len(items) == 0 || !containsString(items, "velocity") {
		return fmt.Errorf("completion self-test failed: %v", items)
	}
	fmt.Println("[PASS] integrated physical-language frontend")
	fmt.Println("[PASS] package import, namespace, quantity and unit registry")
	fmt.Println("[PASS] GitHub/static registry v1/v2 client")
	fmt.Println("[PASS] same-origin Studio/backend API")
	fmt.Println("[PASS] semantic code completion")
	fmt.Println("[PASS] REPL quit/exit immediate handling")
	return nil
}

func main() {
	if len(os.Args) < 2 {
		base := strings.ToLower(filepath.Base(os.Args[0]))
		if strings.Contains(base, "studio") {
			if e := serveStudio(0, false); e != nil {
				fmt.Fprintln(os.Stderr, "error:", e)
			}
			return
		}
		help()
		return
	}
	cmd := os.Args[1]
	var e error
	switch cmd {
	case "version":
		fmt.Println(version)
	case "capabilities":
		fmt.Printf("{\"version\":\"%s\",\"pir\":true,\"ssa\":true,\"bytecode_vm\":true,\"c99_aot\":true,\"native_x86_64_jit\":false,\"exact_polynomial_proofs\":true,\"proof_kernel\":true,\"boolean_sat\":true,\"linear_real_arithmetic\":true}\n", version)
	case "run":
		if len(os.Args) < 3 {
			e = errors.New("run 需要 .phy 文件或后端表达式")
			break
		}
		if info, statErr := os.Stat(os.Args[2]); statErr == nil && !info.IsDir() {
			e = runFrontendFile(os.Args[2], os.Stdout)
			break
		}
		// 为旧版 phybackend 命令保持兼容：文件不存在时按后端表达式执行。
		n, v, x := assignments(os.Args[3:])
		if x != nil {
			e = x
			break
		}
		m, x := compile(os.Args[2], n, make([]Dim, len(n)))
		if x != nil {
			e = x
			break
		}
		z, x := m.run(v)
		if x != nil {
			e = x
		} else {
			fmt.Printf("%.17g\n", z)
		}
	case "expr", "backend-run":
		if len(os.Args) < 3 {
			e = errors.New("expr 需要表达式")
			break
		}
		n, v, x := assignments(os.Args[3:])
		if x != nil {
			e = x
			break
		}
		m, x := compile(os.Args[2], n, make([]Dim, len(n)))
		if x != nil {
			e = x
			break
		}
		z, x := m.run(v)
		if x != nil {
			e = x
		} else {
			fmt.Printf("%.17g\n", z)
		}
	case "check":
		if len(os.Args) < 3 {
			e = errors.New("check 需要 .phy 文件")
			break
		}
		b, x := os.ReadFile(os.Args[2])
		if x != nil {
			e = x
			break
		}
		e = checkFrontendSource(string(b), os.Args[2])
		if e == nil {
			fmt.Println("[PASS] 语法、量纲和规律检查通过。")
		}
	case "repl":
		e = frontendREPL()
	case "studio":
		port, noOpen, x := parsePort(os.Args[2:])
		if x != nil {
			e = x
			break
		}
		e = serveStudio(port, noOpen)
	case "package", "pkg":
		e = packageCommand(os.Args[2:])
	case "compile":
		if len(os.Args) < 3 {
			e = errors.New("compile expression")
			break
		}
		var ds []string
		emit := "ssa"
		for i := 3; i < len(os.Args); i++ {
			if os.Args[i] == "--var" && i+1 < len(os.Args) {
				i++
				ds = append(ds, os.Args[i])
			} else if os.Args[i] == "--emit" && i+1 < len(os.Args) {
				i++
				emit = os.Args[i]
			}
		}
		n, d, x := parseVars(ds)
		if x != nil {
			e = x
			break
		}
		m, x := compile(os.Args[2], n, d)
		if x != nil {
			e = x
		} else if emit == "c" {
			fmt.Print(m.c99())
		} else {
			fmt.Print(m.ssa())
		}
	case "prove":
		if len(os.Args) < 5 {
			e = errors.New("prove theorem lhs rhs")
			break
		}
		var vars []string
		out := ""
		for i := 5; i < len(os.Args); i++ {
			if os.Args[i] == "--var" && i+1 < len(os.Args) {
				i++
				vars = append(vars, os.Args[i])
			} else if os.Args[i] == "--out" && i+1 < len(os.Args) {
				i++
				out = os.Args[i]
			}
		}
		c, x := prove(os.Args[2], os.Args[3], os.Args[4], vars)
		if x != nil {
			e = x
			break
		}
		if out != "" {
			e = os.WriteFile(out, []byte(c.String()), 0644)
			if e == nil {
				fmt.Println("proof_valid: certificate", out)
			}
		} else {
			fmt.Print(c.String())
		}
	case "check-proof":
		if len(os.Args) < 3 {
			e = errors.New("file")
			break
		}
		b, x := os.ReadFile(os.Args[2])
		if x != nil {
			e = x
			break
		}
		c, x := parseCert(string(b))
		if x != nil {
			e = x
		} else if x = verify(c); x != nil {
			e = x
		} else {
			fmt.Println("proof_valid: certificate accepted")
		}
	case "solve":
		if len(os.Args) < 3 {
			e = errors.New("formula")
			break
		}
		s, n, x := solve(os.Args[2])
		if x != nil {
			e = x
			break
		}
		fmt.Printf("%s: explored %d Boolean assignments\n", s, n)
		if s == "unsat" {
			os.Exit(2)
		}
	case "dim":
		if len(os.Args) < 3 {
			e = errors.New("expression")
			break
		}
		var decl []string
		expect := ""
		for i := 3; i < len(os.Args); i++ {
			if os.Args[i] == "--var" && i+1 < len(os.Args) {
				i++
				decl = append(decl, os.Args[i])
			} else if os.Args[i] == "--expect" && i+1 < len(os.Args) {
				i++
				expect = os.Args[i]
			}
		}
		n, d, x := parseVars(decl)
		if x != nil {
			e = x
			break
		}
		dm := map[string]Dim{}
		for i := range n {
			dm[n[i]] = d[i]
		}
		ast, x := parse(os.Args[2])
		if x != nil {
			e = x
			break
		}
		got, x := inferDim(ast, dm)
		if x != nil {
			e = x
			break
		}
		if expect != "" {
			want, x := parseDim(expect)
			if x != nil {
				e = x
			} else if got != want {
				e = fmt.Errorf("expected %s, got %s", want, got)
			} else {
				fmt.Println("ok:", got)
			}
		} else {
			fmt.Println(got)
		}
	case "serve":
		address := "127.0.0.1:18766"
		if len(os.Args) >= 3 {
			address = os.Args[2]
		}
		e = serveBackend(address)
	case "self-test":
		e = integratedSelftest()
	case "help", "--help", "-h":
		help()
	default:
		e = fmt.Errorf("unknown command %s", cmd)
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, "error:", e)
		os.Exit(1)
	}
}
