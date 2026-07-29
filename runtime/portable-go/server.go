package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type APIRequest struct {
	Operation         string             `json:"operation"`
	Expression        string             `json:"expression"`
	Variables         map[string]string  `json:"variables"`
	Assignments       map[string]float64 `json:"assignments"`
	Emit              string             `json:"emit"`
	Theorem           string             `json:"theorem"`
	LHS               string             `json:"lhs"`
	RHS               string             `json:"rhs"`
	Proof             string             `json:"proof"`
	Formula           string             `json:"formula"`
	ExpectedDimension string             `json:"expectedDimension"`
}

type APIResponse struct {
	OK     bool     `json:"ok"`
	Status string   `json:"status"`
	Output string   `json:"output,omitempty"`
	Error  string   `json:"error,omitempty"`
	Result *float64 `json:"result,omitempty"`
}

func variableLists(m map[string]string) ([]string, []Dim, error) {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	dims := make([]Dim, 0, len(names))
	for _, n := range names {
		d, e := parseDim(m[n])
		if e != nil {
			return nil, nil, e
		}
		dims = append(dims, d)
	}
	return names, dims, nil
}
func apiHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(APIResponse{Error: "POST required"})
		return
	}
	var q APIRequest
	if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&q); e != nil {
		_ = json.NewEncoder(w).Encode(APIResponse{Error: e.Error()})
		return
	}
	reply := APIResponse{}
	fail := func(e error) { reply.OK = false; reply.Status = "error"; reply.Error = e.Error() }
	switch q.Operation {
	case "capabilities":
		reply.OK = true
		reply.Status = "ok"
		reply.Output = fmt.Sprintf("{\"version\":\"%s\",\"pir\":true,\"ssa\":true,\"bytecode_vm\":true,\"proof_kernel\":true,\"qf_lra\":true}", version)
	case "compile":
		n, d, e := variableLists(q.Variables)
		if e != nil {
			fail(e)
			break
		}
		m, e := compile(q.Expression, n, d)
		if e != nil {
			fail(e)
			break
		}
		reply.OK = true
		reply.Status = "ok"
		if q.Emit == "c" {
			reply.Output = m.c99()
		} else {
			reply.Output = m.ssa()
		}
	case "run":
		names, dims, e := variableLists(q.Variables)
		if e != nil {
			fail(e)
			break
		}
		if len(names) == 0 {
			for n := range q.Assignments {
				names = append(names, n)
			}
			sort.Strings(names)
			dims = make([]Dim, len(names))
		}
		vals := make([]float64, len(names))
		missing := ""
		for i, n := range names {
			value, exists := q.Assignments[n]
			if !exists {
				missing = n
				break
			}
			vals[i] = value
		}
		if missing != "" {
			fail(fmt.Errorf("missing assignment for %s", missing))
			break
		}
		m, e := compile(q.Expression, names, dims)
		if e != nil {
			fail(e)
			break
		}
		v, e := m.run(vals)
		if e != nil {
			fail(e)
			break
		}
		reply.OK = true
		reply.Status = "ok"
		reply.Result = &v
		reply.Output = strconv.FormatFloat(v, 'g', 17, 64)
	case "prove":
		vars := make([]string, 0, len(q.Variables))
		for n := range q.Variables {
			vars = append(vars, n)
		}
		sort.Strings(vars)
		c, e := prove(q.Theorem, q.LHS, q.RHS, vars)
		if e != nil {
			fail(e)
			break
		}
		reply.OK = true
		reply.Status = "proof_valid"
		reply.Output = c.String()
	case "verify-proof":
		c, e := parseCert(q.Proof)
		if e != nil {
			fail(e)
			break
		}
		if e = verify(c); e != nil {
			fail(e)
			break
		}
		reply.OK = true
		reply.Status = "proof_valid"
		reply.Output = "certificate accepted"
	case "solve":
		s, n, e := solve(q.Formula)
		if e != nil {
			fail(e)
			break
		}
		reply.OK = true
		reply.Status = s
		reply.Output = fmt.Sprintf("%s: explored %d Boolean assignments", s, n)
	case "dimension":
		n, d, e := variableLists(q.Variables)
		if e != nil {
			fail(e)
			break
		}
		dm := map[string]Dim{}
		for i := range n {
			dm[n[i]] = d[i]
		}
		a, e := parse(q.Expression)
		if e != nil {
			fail(e)
			break
		}
		got, e := inferDim(a, dm)
		if e != nil {
			fail(e)
			break
		}
		if strings.TrimSpace(q.ExpectedDimension) != "" {
			want, e := parseDim(q.ExpectedDimension)
			if e != nil {
				fail(e)
				break
			}
			if got != want {
				fail(fmt.Errorf("expected %s, got %s", want, got))
				break
			}
		}
		reply.OK = true
		reply.Status = "ok"
		reply.Output = got.String()
	default:
		fail(fmt.Errorf("unknown operation %q", q.Operation))
	}
	_ = json.NewEncoder(w).Encode(reply)
}

func serveBackend(address string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "{\"ok\":true,\"version\":\"%s\"}", version)
	})
	mux.HandleFunc("/api", apiHandler)
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	fmt.Printf("PhyBackend autonomous service listening on http://%s\n", address)
	return server.ListenAndServe()
}
