package live

import "time"

// hi is a chat call's body for model.
func hi(model string) []byte {
	return []byte(`{"model": "` + model + `", "messages": ` +
		`[{"role": "user", "content": "hi"}]}`)
}

// Steps are the requests both the capture and the fake get, in order:
// every route of spec 7.9. {seat} is a per-run seat name.
func Steps(now time.Time) []Step {
	day := now.UTC().Format("2006-01-02")
	gen := []byte(`{"key_alias": "{seat}", "models": ["local"], ` +
		`"max_budget": 1, "duration": "1h"}`)
	del := []byte(`{"key_aliases": ["{seat}"]}`)
	return []Step{
		{Name: "liveliness", Method: "GET",
			Path: "/health/liveliness"},
		{Name: "readiness", Method: "GET",
			Path: "/health/readiness/details", Auth: "master"},
		{Name: "readiness_nokey", Method: "GET",
			Path: "/health/readiness/details"},
		{Name: "models", Method: "GET", Path: "/v1/models",
			Auth: "master"},
		{Name: "key_list", Method: "GET", Path: "/key/list?" +
			"return_full_object=true&size=100&page=1",
			Auth: "master"},
		{Name: "key_list_wrong", Method: "GET", Path: "/key/list",
			Auth: "wrong"},
		{Name: "generate", Method: "POST", Path: "/key/generate",
			Auth: "master", Body: gen},
		{Name: "generate_taken", Method: "POST",
			Path: "/key/generate", Auth: "master", Body: gen},
		{Name: "generate_wrong", Method: "POST",
			Path: "/key/generate", Auth: "wrong",
			Body: []byte(`{"key_alias": "x"}`)},
		{Name: "key_list_alias", Method: "GET", Path: "/key/list?" +
			"return_full_object=true&key_alias={seat}",
			Auth: "master"},
		{Name: "chat", Method: "POST", Path: "/v1/chat/completions",
			Auth: "seat", Body: hi("local")},
		{Name: "chat_model_refused", Method: "POST",
			Path: "/v1/chat/completions", Auth: "seat",
			Body: hi("nope")},
		{Name: "daily_activity", Method: "GET",
			Path: "/user/daily/activity?start_date=" + day +
				"&end_date=" + day + "&page_size=1000",
			Auth: "master"},
		{Name: "delete", Method: "POST", Path: "/key/delete",
			Auth: "master", Body: del},
		{Name: "delete_none", Method: "POST", Path: "/key/delete",
			Auth: "master", Body: del},
		{Name: "chat_deleted", Method: "POST",
			Path: "/v1/chat/completions", Auth: "seat",
			Body: hi("local")},
		{Name: "unknown", Method: "GET", Path: "/no/such/path",
			Auth: "master"},
	}
}
