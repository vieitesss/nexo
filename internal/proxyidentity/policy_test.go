package proxyidentity

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeStore struct {
	candidates []Candidate
	adopted    []Identity
}

func (s *fakeStore) ListProxyCandidates(context.Context) ([]Candidate, error) {
	return s.candidates, nil
}

func (s *fakeStore) AdoptProxy(_ context.Context, _ string, identity Identity) error {
	s.adopted = append(s.adopted, identity)
	return nil
}

func TestPolicyResolvesTaggedIdentityExactly(t *testing.T) {
	store := &fakeStore{candidates: []Candidate{
		{ID: "other", TaggedIdentity: "devbox/prod-long"},
		{ID: "target", TaggedIdentity: "DEVBOX/prod"},
	}}
	policy := NewPolicy([]string{"devbox"})
	id := NewIdentity("devbox", "prod")
	got, err := policy.Resolve(context.Background(), id, store)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != "target" {
		t.Fatalf("Resolve() = %q, want target", got)
	}
	if len(store.adopted) != 0 {
		t.Fatalf("adoptions = %#v, want none", store.adopted)
	}
}

func TestPolicyAdoptsOnlyConfiguredExactUntaggedLabel(t *testing.T) {
	store := &fakeStore{candidates: []Candidate{
		{ID: "conflicting", Label: "devbox/prod", TaggedIdentity: "devbox/other"},
		{ID: "target", Label: "DEVBOX/prod"},
	}}
	policy := NewPolicy([]string{" devbox "})
	got, err := policy.Resolve(context.Background(), NewIdentity("DEVBOX", "prod"), store)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != "target" {
		t.Fatalf("Resolve() = %q, want target", got)
	}
	want := []Identity{NewIdentity("devbox", "prod")}
	if !reflect.DeepEqual(store.adopted, want) {
		t.Fatalf("adoptions = %#v, want %#v", store.adopted, want)
	}
}

func TestPolicyDoesNotAdoptAnUnconfiguredMachine(t *testing.T) {
	store := &fakeStore{candidates: []Candidate{{ID: "target", Label: "devbox/prod"}}}
	got, err := NewPolicy(nil).Resolve(context.Background(), NewIdentity("devbox", "prod"), store)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != "" || len(store.adopted) != 0 {
		t.Fatalf("Resolve() = %q with adoptions %#v, want no match", got, store.adopted)
	}
}

func TestPolicyResolvesTaggedIdentityForAStaleConfiguredMachine(t *testing.T) {
	store := &fakeStore{candidates: []Candidate{{ID: "stale", TaggedIdentity: "devbox/prod"}}}
	got, err := NewPolicy(nil).Resolve(context.Background(), NewIdentity("devbox", "prod"), store)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != "stale" {
		t.Fatalf("Resolve() = %q, want stale local proxy", got)
	}
}

func TestPolicyReturnsStoreErrors(t *testing.T) {
	wantErr := errors.New("list failed")
	store := errorStore{err: wantErr}
	_, err := NewPolicy(nil).Resolve(context.Background(), NewIdentity("devbox", "prod"), store)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Resolve() error = %v, want %v", err, wantErr)
	}
}

type errorStore struct{ err error }

func (s errorStore) ListProxyCandidates(context.Context) ([]Candidate, error) { return nil, s.err }
func (errorStore) AdoptProxy(context.Context, string, Identity) error         { return nil }

func TestParseTargetRejectsMalformedIdentity(t *testing.T) {
	for _, target := range []string{"", "/prod", "devbox/", "-devbox/prod", "devbox"} {
		if _, err := ParseTarget(target); err == nil {
			t.Fatalf("ParseTarget(%q) = nil error, want rejection", target)
		}
	}
	got, err := ParseTarget(" DEVBOX/prod ")
	if err != nil {
		t.Fatalf("ParseTarget() error = %v", err)
	}
	if got.Machine != "DEVBOX" || got.Container != "prod" {
		t.Fatalf("ParseTarget() = %#v, want trimmed source identity", got)
	}
}
