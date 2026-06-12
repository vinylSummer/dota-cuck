package api

import (
	"encoding/json"
	"testing"
)

func TestPushEventJSONShapes(t *testing.T) {
	cases := []struct {
		name  string
		event PushEvent
		want  string
	}{
		{
			"session_state",
			SessionStateEvent("sid", "WATCHING"),
			`{"type":"session_state","session_id":"sid","state":"WATCHING"}`,
		},
		{
			"steam_guard",
			SteamGuardEvent("sid", "EMAIL"),
			`{"type":"steam_guard","session_id":"sid","guard_type":"EMAIL"}`,
		},
		{
			"stream_ready",
			StreamReadyEvent("sid", "https://dota.example.com/webrtc/live/match"),
			`{"type":"stream_ready","session_id":"sid","webrtc_url":"https://dota.example.com/webrtc/live/match"}`,
		},
		{
			"error",
			ErrorEvent("sid", "DOTA_CRASH", "boom"),
			`{"type":"error","session_id":"sid","code":"DOTA_CRASH","message":"boom"}`,
		},
	}
	for _, c := range cases {
		got, err := json.Marshal(c.event)
		if err != nil {
			t.Errorf("%s: marshal: %v", c.name, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}
