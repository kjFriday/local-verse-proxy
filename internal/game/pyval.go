package game

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// The game's numbers come from two places: the proxy's database, as
// JSON (json.Number), and office.db (int64 or float64). office.py
// added and compared them as Python ints and floats; these helpers do
// the same, so a sum of ints stays an int and prints as one.

// pyValue is v as Python's json.loads gives it: a JSON number with
// no ".", "e" or "E" is an int, else a float.
func pyValue(v any) any {
	n, ok := v.(json.Number)
	if !ok {
		return v
	}
	s := string(n)
	if !strings.ContainsAny(s, ".eE") {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i
		}
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// toF is a number as a float; anything else is 0.
func toF(v any) float64 {
	switch x := pyValue(v).(type) {
	case int64:
		return float64(x)
	case float64:
		return x
	case bool:
		if x {
			return 1
		}
	}
	return 0
}

// toI is a number as an int, a float cut toward zero.
func toI(v any) int64 {
	switch x := pyValue(v).(type) {
	case int64:
		return x
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0
		}
		return int64(x)
	case bool:
		if x {
			return 1
		}
	}
	return 0
}

// add is Python's a + b on ints and floats: int + int is an int,
// anything with a float is a float. nil counts as 0.
func add(a, b any) any {
	a, b = pyValue(a), pyValue(b)
	ai, aInt := a.(int64)
	bi, bInt := b.(int64)
	if a == nil {
		ai, aInt = 0, true
	}
	if b == nil {
		bi, bInt = 0, true
	}
	if aInt && bInt {
		return ai + bi
	}
	return toF(a) + toF(b)
}

// sum is Python's sum(values), started at 0. Ints add exactly; once
// a float comes, Python (3.12 and later) adds the rest of the floats
// with Neumaier's compensated sum, so a long sum of floats differs
// from adding them one by one.
func sum(values []any) any {
	var s any = int64(0)
	i := 0
	for ; i < len(values); i++ {
		s = add(s, values[i])
		if _, isF := s.(float64); isF {
			i++
			break
		}
	}
	f, ok := s.(float64)
	if !ok {
		return s
	}
	c := 0.0
	for ; i < len(values); i++ {
		v := pyValue(values[i])
		x, isF := v.(float64)
		if !isF {
			x = toF(v) // an int: added plainly, as CPython does
			f += x
			continue
		}
		t := f + x
		if math.Abs(f) >= math.Abs(x) {
			c += (f - t) + x
		} else {
			c += (x - t) + f
		}
		f = t
	}
	if c != 0 && !math.IsInf(c, 0) && !math.IsNaN(c) {
		f += c
	}
	return f
}

// fsum is sum() of floats.
func fsum(values []float64) float64 {
	a := make([]any, len(values))
	for i, v := range values {
		a[i] = v
	}
	return toF(sum(a))
}

// truthy is Python's bool(v) for the values rows hold.
func truthy(v any) bool {
	switch x := pyValue(v).(type) {
	case nil:
		return false
	case bool:
		return x
	case int64:
		return x != 0
	case float64:
		return x != 0
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// str is Python's str(v) for the values rows hold.
func str(v any) string {
	switch x := pyValue(v).(type) {
	case nil:
		return "None"
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	}
	return pyfmt.Str(pyValue(v))
}
