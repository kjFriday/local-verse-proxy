package pyrand

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

// The golden: tests/golden/pyrand.py's run, op by op, per seed.
func TestGolden(t *testing.T) {
	b, err := os.ReadFile("testdata/golden/pyrand.json")
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Python []string `json:"python"`
		Cases  [][2]json.RawMessage
	}
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Cases) < 200 {
		t.Fatalf("%d seeds, want 200", len(g.Cases))
	}
	for _, c := range g.Cases {
		var seed string
		var ops [][]any
		json.Unmarshal(c[0], &seed)
		json.Unmarshal(c[1], &ops)
		r := New(seed)
		for i, op := range ops {
			want := op[len(op)-1]
			var got any
			switch op[0] {
			case "random":
				got = repr(r.Random())
			case "getrandbits":
				k := int(op[1].(float64))
				got = strconv.FormatUint(r.GetRandBits(k), 10)
			case "randint":
				got = float64(r.RandInt(int(op[1].(float64)),
					int(op[2].(float64))))
			case "uniform":
				a, _ := strconv.ParseFloat(op[1].(string), 64)
				bb, _ := strconv.ParseFloat(op[2].(string), 64)
				got = repr(r.Uniform(a, bb))
			case "choice":
				got = float64(r.Choice(int(op[1].(float64))))
			case "choices":
				var w []float64
				for _, x := range op[1].([]any) {
					w = append(w, x.(float64))
				}
				got = float64(r.Choices(w))
			}
			if got != want {
				t.Fatalf("seed %q, op %d %v: got %v, Python %v",
					seed,
					i, op[:len(op)-1], got, want)
			}
		}
	}
}

// repr is enough of Python's repr(float) for random()'s values: the
// shortest text that reads back.
func repr(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if f == float64(int64(f)) && f < 1e16 {
		s = strconv.FormatFloat(f, 'f', 1, 64)
	}
	return s
}

func TestRandBelowEdges(t *testing.T) {
	r := New("edges")
	if r.RandBelow(0) != 0 || r.RandBelow(-3) != 0 || r.Choice(1) != 0 {
		t.Fatal("RandBelow of 1 or less must be 0")
	}
	if got := New("x").GetRandBits(0); got != 0 {
		t.Fatalf("getrandbits(0) = %d", got)
	}
}
