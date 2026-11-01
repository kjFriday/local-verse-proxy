package desk

import (
	"context"
	"strings"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/pyjson"
)

// TallySQL is spend per seat and per model in one round trip, for
// today (UTC) or for the days before it; {op} is = or <. Cancelled
// cards keep their seat name via the archive. fails: requests the
// proxy refused. Byte for byte frontdesk.py's (golden-checked).
const TallySQL = `
with n as (
  select distinct on (token) token, key_alias from (
    select token, key_alias, 0 o from "LiteLLM_VerificationToken"
    union all
    select token, key_alias, 1 from "LiteLLM_DeletedVerificationToken"
  ) x order by token, o),
s as (
  select coalesce(n.key_alias, '') seat,
    coalesce(d.model_group, d.model, '') model, d.spend,
    d.prompt_tokens + d.completion_tokens tokens,
    d.api_requests reqs, d.failed_requests fails
  from "LiteLLM_DailyUserSpend" d left join n on n.token = d.api_key
  where d.date {op} to_char(now() at time zone 'UTC', 'YYYY-MM-DD')),
g as (
  select case when grouping(seat) = 0 then 'seat' else 'model' end k,
    coalesce(seat, model) name, sum(spend) spend, sum(tokens) tokens,
    sum(reqs) reqs, sum(fails) fails
  from s group by grouping sets ((seat), (model)))
select json_build_object(
  'day', to_char(now() at time zone 'UTC', 'YYYY-MM-DD'),
  'rows', coalesce(json_agg(g), '[]'))
from g;
`

// TodaySQL and HistorySQL are the two questions.
var (
	TodaySQL   = strings.Replace(TallySQL, "{op}", "=", 1)
	HistorySQL = strings.Replace(TallySQL, "{op}", "<", 1)
)

// Counts are the tally's numbers, in order.
var Counts = [4]string{"spend", "tokens", "reqs", "fails"}

// Row is one tally line: a seat or a model, all time and today (t_).
// A nil count is Python's None (http_tally, before its last step).
type Row struct {
	K, Name string
	All     [4]*pyjson.Num
	Today   [4]pyjson.Num
}

// Obj is the row as JSON; live, if not nil, is added at the end.
func (r Row) Obj(live *bool) pyjson.Obj {
	o := pyjson.O("k", r.K, "name", r.Name)
	for i, f := range Counts {
		o = append(o, pyjson.KV{K: f, V: r.All[i]})
	}
	for i, f := range Counts {
		o = append(o, pyjson.KV{K: "t_" + f, V: r.Today[i]})
	}
	if live != nil {
		o = append(o, pyjson.KV{K: "live", V: *live})
	}
	return o
}

// Tally is one tally: the database's or the proxy's.
type Tally struct {
	Day    any
	Source any
	Why    any
	Rows   []Row
}

// dbTally is the database's answer: {"day": ..., "rows": [...]}.
type dbTally struct {
	Day  any
	Rows []pyjson.Obj
}

func readTally(raw []byte) (dbTally, error) {
	v, err := Decode(raw)
	if err != nil {
		return dbTally{}, err
	}
	o, ok := v.(pyjson.Obj)
	if !ok {
		return dbTally{}, &ShapeError{"not an object"}
	}
	t := dbTally{Day: o.Val("day")}
	rows, _ := o.Val("rows").([]any)
	for _, r := range rows {
		ro, ok := r.(pyjson.Obj)
		if !ok {
			return dbTally{}, &ShapeError{"a row is not an object"}
		}
		t.Rows = append(t.Rows, ro)
	}
	return t, nil
}

func ptr(n pyjson.Num) *pyjson.Num { return &n }

// MergeTally is merge_tally: all time is the days before plus today;
// the t_ fields are today only.
func MergeTally(hist, today []pyjson.Obj, day any) Tally {
	var rows []Row
	at := map[[2]string]int{}
	for pass, src := range [][]pyjson.Obj{hist, today} {
		for _, r := range src {
			key := [2]string{text(r.Val("k")), text(r.Val("name"))}
			i, ok := at[key]
			if !ok {
				i = len(rows)
				at[key] = i
				row := Row{K: key[0], Name: key[1]}
				for j := range Counts {
					row.All[j] = ptr(pyjson.IntNum(0))
					row.Today[j] = pyjson.IntNum(0)
				}
				rows = append(rows, row)
			}
			for j, f := range Counts {
				v := orZero(r.Val(f))
				n := rows[i].All[j].Add(v)
				rows[i].All[j] = &n
				if pass == 1 {
					rows[i].Today[j] = v
				}
			}
		}
	}
	return Tally{Day: day, Rows: rows}
}

type adder func(kind, name string, spend pyjson.Num, today any)

// addGroup adds one model group of /user/daily/activity: the model,
// then each key's share by its seat name.
func addGroup(add adder, g pyjson.KV) {
	m, _ := g.V.(pyjson.Obj)
	add("model", g.K, pyjson.IntNum(0), m.Val("metrics"))
	kbs, _ := m.Val("api_key_breakdown").(pyjson.Obj)
	for _, kb := range kbs {
		ko, _ := kb.V.(pyjson.Obj)
		meta, _ := ko.Val("metadata").(pyjson.Obj)
		add("seat", text(meta.Val("key_alias")), pyjson.IntNum(0),
			ko.Val("metrics"))
	}
}

// HTTPTally is http_tally: without the database, today from the
// proxy's own API, and for all time only what it still knows, at
// least today. keys are the cards now.
func (d *Desk) HTTPTally(ctx context.Context, why string,
	keys []pyjson.Obj) Tally {
	day := time.Unix(int64(d.clk.Wall()), 0).UTC().Format("2006-01-02")
	var rows []Row
	at := map[[2]string]int{}
	add := func(kind, name string, spend pyjson.Num, today any) {
		key := [2]string{kind, name}
		i, ok := at[key]
		if !ok {
			i = len(rows)
			at[key] = i
			row := Row{K: kind, Name: name}
			for j := range Counts {
				row.Today[j] = pyjson.IntNum(0)
			}
			rows = append(rows, row)
		}
		r := &rows[i]
		if Truthy(today) {
			t, _ := today.(pyjson.Obj)
			for j, f := range []string{"spend", "total_tokens",
				"api_requests", "failed_requests"} {
				r.Today[j] = r.Today[j].Add(orZero(t.Val(f)))
			}
			return
		}
		old := pyjson.IntNum(0)
		if r.All[0] != nil && r.All[0].F != 0 {
			old = *r.All[0]
		}
		r.All[0] = ptr(old.Add(spend))
	}
	j, err := d.proxy.JSON(ctx, "/user/daily/activity?start_date="+day+
		"&end_date="+day+"&page_size=1000")
	if err == nil {
		top, _ := j.(pyjson.Obj)
		results, _ := top.Val("results").([]any)
		for _, res := range results {
			ro, _ := res.(pyjson.Obj)
			bd, _ := ro.Val("breakdown").(pyjson.Obj)
			groups, _ := bd.Val("model_groups").(pyjson.Obj)
			for _, g := range groups {
				addGroup(add, g)
			}
		}
	}
	for _, k := range keys {
		add("seat", text(k.Val("key_alias")), orZero(k.Val("spend")),
			nil)
	}
	// A card that refills keeps only its spend since the last
	// refill, a cancelled one none, and the proxy keeps no request
	// or token totals at all: all time is at least today.
	for i := range rows {
		for f := range Counts {
			v := pyjson.IntNum(0)
			if p := rows[i].All[f]; p != nil && p.F != 0 {
				v = *p
			}
			if rows[i].Today[f].F > v.F {
				v = rows[i].Today[f]
			}
			rows[i].All[f] = ptr(v)
		}
	}
	return Tally{Day: day, Rows: rows, Source: "proxy", Why: why}
}
