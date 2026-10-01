package picker

import (
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
)

func TestRankAndPlaceConversion(t *testing.T) {
	match, ok := Rank("bar", "foobar")
	if !ok || match.Score <= 0 {
		t.Fatalf("Rank = %#v,%v", match, ok)
	}
	places := ContainerPlaces([]capabilities.Container{{ID: "one", Name: "one", Machine: "dev"}})
	if len(places) != 1 || places[0].ContainerID != "one" || places[0].Machine != "dev" {
		t.Fatalf("places = %#v", places)
	}
}

func TestAgentHelpers(t *testing.T) {
	since := time.Now().Add(-3 * time.Minute)
	agents := []capabilities.Agent{{Harness: "pi", State: "stopped", Seen: false, StoppedSince: &since}, {Harness: "claude", State: "working"}}
	if AttentionRank(agents[0]) >= AttentionRank(agents[1]) {
		t.Fatal("unseen stopped should rank first")
	}
	if AgentIdentity(capabilities.Agent{PaneID: "%7"}) != "%7" {
		t.Fatal("pane identity lost")
	}
	if StoppedAge(time.Now(), since) != "3m" {
		t.Fatal("stopped age formatting changed")
	}
	if StoppedAge(time.Unix(0, 0), time.Unix(-24*60*60, 0)) != "1d" {
		t.Fatal("24-hour age should use days")
	}
	if StoppedAge(time.Unix(0, 0), time.Unix(-23*60*60, 0)) != "23h" {
		t.Fatal("sub-day age should use hours")
	}
}
