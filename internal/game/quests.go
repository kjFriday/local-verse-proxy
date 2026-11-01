package game

import (
	"fmt"
	"sort"

	"github.com/kjFriday/local-verse-proxy/internal/pyrand"
)

// postQuests is office.py's post_quests(): per_seat quests for each
// roster seat, their sizes drawn with the sizes' weights from a seed
// of the day. Returns how many.
func (o *office) postQuests(day int) (int, error) {
	r := o.rules
	sizes := r.Quests.Sizes
	names := make([]string, 0, len(sizes))
	for n := range sizes {
		names = append(names, n)
	}
	sort.Strings(names)
	weights := make([]float64, len(names))
	for i, n := range names {
		weights[i] = sizes[n].Weight
	}
	rng := pyrand.New(fmt.Sprintf("%s-quests-%d", r.Sim.Seed.Text, day))
	n := r.Quests.PerSeat * len(r.Roster.Seats)
	for i := 0; i < n; i++ {
		size := names[rng.Choices(weights)]
		s := sizes[size]
		if _, err := o.q("INSERT INTO quests VALUES "+
			"(?,?,?,?,?,?,NULL,'open')",
			fmt.Sprintf("d%dq%02d", day, i+1), int64(day), size,
			s.Value, int64(s.Calls), s.ParMana); err != nil {
			return 0, err
		}
	}
	return n, nil
}
