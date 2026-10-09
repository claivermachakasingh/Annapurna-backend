package qrchain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

// independentHash recomputes the canonical hash straight from the written
// specification — sha256(prev || batch || event || actor || ts || evidence) of
// ASCII bytes — so the implementation is checked against the spec rather than
// against itself (D30).
func independentHash(prev, batch, event, actor, ts, evidence string) string {
	sum := sha256.Sum256([]byte(prev + batch + event + actor + ts + evidence))
	return hex.EncodeToString(sum[:])
}

func TestHashMatchesSpecification(t *testing.T) {
	got := Hash("GENESIS", "b-1", "CREATED", "actor-1", "2026-10-02T12:00:00Z", "")
	want := independentHash("GENESIS", "b-1", "CREATED", "actor-1", "2026-10-02T12:00:00Z", "")
	if got != want {
		t.Fatalf("Hash() = %s, want %s", got, want)
	}
	if len(got) != 64 {
		t.Fatalf("hash length = %d, want 64 hex chars", len(got))
	}
}

func TestHashIsDeterministicAndPositionSensitive(t *testing.T) {
	a := Hash("GENESIS", "b-1", "CREATED", "actor-1", "2026-10-02T12:00:00Z", "")
	b := Hash("GENESIS", "b-1", "CREATED", "actor-1", "2026-10-02T12:00:00Z", "")
	if a != b {
		t.Fatal("same inputs must produce the same hash")
	}
	// Swapping fields must change the digest (no ambiguity in the concatenation).
	if a == Hash("b-1", "GENESIS", "CREATED", "actor-1", "2026-10-02T12:00:00Z", "") {
		t.Fatal("field order must affect the hash")
	}
	if a == Hash("GENESIS", "b-1", "APPROVED", "actor-1", "2026-10-02T12:00:00Z", "") {
		t.Fatal("event type must affect the hash")
	}
}

func TestComputeHashAliasMatchesHash(t *testing.T) {
	if ComputeHash("p", "b", "e", "a", "t", "ev") != Hash("p", "b", "e", "a", "t", "ev") {
		t.Fatal("ComputeHash and Hash must agree")
	}
}

// TestVerifyChainAcceptssValidChain builds a three-event chain and checks it
// verifies, then tampers with each part in turn.
func TestVerifyChain(t *testing.T) {
	ts := "2026-10-02T12:00:00Z"
	batch := "batch-1"

	build := func() []QREvent {
		prev := GenesisHash
		events := make([]QREvent, 0, 3)
		for _, ev := range []string{"CREATED", "APPROVED", "MATCHED"} {
			h := Hash(prev, batch, ev, "actor-1", ts, "")
			events = append(events, QREvent{
				BatchID:     batch,
				EventType:   ev,
				ActorID:     "actor-1",
				PrevHash:    prev,
				Hash:        h,
				ServerTSIso: ts,
			})
			prev = h
		}
		return events
	}

	ok, brokenAt := VerifyChain(build())
	if !ok || brokenAt != -1 {
		t.Fatalf("valid chain reported invalid at %d", brokenAt)
	}

	// Tamper with the payload of the second event: its hash no longer matches.
	tampered := build()
	tampered[1].EventType = "PICKED_UP"
	if ok, brokenAt := VerifyChain(tampered); ok || brokenAt != 1 {
		t.Fatalf("tampered payload: got valid=%v brokenAt=%d, want false/1", ok, brokenAt)
	}

	// Tamper with the linkage: the third event no longer points at the second.
	relinked := build()
	relinked[2].PrevHash = GenesisHash
	if ok, brokenAt := VerifyChain(relinked); ok || brokenAt != 2 {
		t.Fatalf("broken link: got valid=%v brokenAt=%d, want false/2", ok, brokenAt)
	}

	// A chain whose first event is not rooted in GENESIS must still be caught by
	// the hash check on the first event.
	orphan := build()
	orphan[0].PrevHash = "not-genesis"
	if ok, _ := VerifyChain(orphan); ok {
		t.Fatal("chain with a modified genesis link must not verify")
	}
}

// TestEvidenceHashIsStable documents that the evidence digest that feeds the
// chain is a plain SHA-256 over the canonical JSON snapshot (DB10).
func TestEvidenceHashIsStable(t *testing.T) {
	payload := []byte(`{"batch_id":"b1","safety_decision":"ELIGIBLE","danger_zone_minutes":18}`)
	first := EvidenceHash(payload)
	if first != EvidenceHash(payload) {
		t.Fatal("evidence hash must be deterministic")
	}
	if first == EvidenceHash([]byte(`{"batch_id":"b1","safety_decision":"REJECTED","danger_zone_minutes":18}`)) {
		t.Fatal("different safety snapshots must produce different evidence hashes")
	}
}

func TestVerifyChainUsesEventHashFallback(t *testing.T) {
	now := time.Now().UTC()
	h := Hash(GenesisHash, "b1", "CREATED", "a1", now.Format(time.RFC3339), "")
	events := []QREvent{{
		BatchID:   "b1",
		EventType: "CREATED",
		ActorID:   "a1",
		PrevHash:  GenesisHash,
		EventHash: h, // Hash field empty → EventHash is used
		CreatedAt: now,
	}}
	if ok, at := VerifyChain(events); !ok {
		t.Fatalf("chain using EventHash/created_at fallback failed at %d", at)
	}
}

func TestGenesisHashConstant(t *testing.T) {
	if GenesisHash != "GENESIS" {
		t.Fatalf("genesis marker changed: %q (seeded chains depend on it)", GenesisHash)
	}
	if fmt.Sprintf("%d", len(Hash(GenesisHash, "b", "CREATED", "a", "t", ""))) != "64" {
		t.Fatal("hash must be 64 hex characters")
	}
}
