package game

import "strings"

// pg is office.py's pg(): one SELECT on the proxy's Postgres, READ
// ONLY, through psql in the proxy folder. A failure ends the command
// with pgread's words.
func (g *game) pg(sql string) ([]map[string]any, error) {
	if g.ctx.Err() != nil {
		return nil, errStopped
	}
	rows, err := g.pgFn(g.ctx, g.root, sql)
	if err != nil {
		if g.ctx.Err() != nil {
			return nil, errStopped
		}
		return nil, die(err.Error())
	}
	return rows, nil
}

// seatState is office.py's seat_state(): each seat's spend and next
// refill (epoch seconds), by key alias.
func (g *game) seatState(seats []string) (map[string]map[string]any,
	error) {
	quoted := make([]string, len(seats))
	for i, s := range seats {
		quoted[i] = lit(s)
	}
	rows, err := g.pg("SELECT key_alias AS seat, spend, " +
		"extract(epoch FROM budget_reset_at) AS reset_at " +
		"FROM \"LiteLLM_VerificationToken\" " +
		"WHERE key_alias IN (" + strings.Join(quoted, ", ") + ")")
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	for _, r := range rows {
		out[str(r["seat"])] = r
	}
	return out, nil
}

// spendWhere is office.py's spend_where(): the tagged, answered
// requests of one game day. The time bound only keeps the scan short.
func spendWhere(office, tag string) string {
	tags := jsonList([]string{"lvg:" + office, "day:" + tag})
	return "request_tags @> " + lit(tags) + "::jsonb AND status = " +
		"'success' AND \"startTime\" > (now() AT TIME ZONE 'utc') " +
		"- interval '9 days'"
}
