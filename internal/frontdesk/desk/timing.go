package desk

// How often the desk checks what, in seconds, as frontdesk.py.
const (
	Fast          = 2.0   // between checks while someone watches
	KeysEvery     = 10.0  // card spend lands in batches
	TallyEvery    = 5.0   // the proxy writes spend in batches anyway
	HistoryEvery  = 600.0 // the days before today barely change
	FallbackEvery = 60.0  // the proxy's own tally is heavy for it
	SlowEvery     = 60.0  // version and model list
	IdleAfter     = 30.0  // stop checking this long after the last viewer
	PingEvery     = 15.0  // keep live streams open
)

// Waits, in seconds.
const (
	freshWait    = 4.0  // Fresh: for a check to finish
	refreshWait  = 5.0  // RefreshNow: for a full check
	tallyWait    = 10.0 // a check, for the tally job
	liveTimeout  = 2.0  // /health/liveliness
	proxyTimeout = 5.0  // every other proxy GET
	maxPages     = 50   // /key/list pages of 100
	maxAnswer    = 16 << 20
)
