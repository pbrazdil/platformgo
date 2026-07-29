package platform

import "testing"

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/messaging/e2e_outbox.rs:38
// test: pending_depth_counts_backlog_and_purge_removes_published
func TestPendingDepthCountsBacklogAndPurgeRemovesPublished(t *testing.T) {
	outbox := newOutboxFixture(newMessageBus())
	id := outbox.write("test.outbox.retention", []byte("keep-then-purge"))
	if got := outbox.pendingDepth(); got != 1 {
		t.Fatalf("pending depth = %d, want 1", got)
	}
	outbox.markPublished(id)
	if got := outbox.pendingDepth(); got != 0 {
		t.Fatalf("pending depth after publish = %d, want 0", got)
	}
	if got := outbox.purgePublished(); got != 1 || len(outbox.rows) != 0 {
		t.Fatalf("purged=%d rows=%d, want 1 and 0", got, len(outbox.rows))
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/messaging/e2e_outbox.rs:68
// test: on_commit_drain_via_notify_beats_fallback_poll
func TestOnCommitDrainViaNotifyBeatsFallbackPoll(t *testing.T) {
	outbox := newOutboxFixture(newMessageBus())
	id := outbox.write("test.outbox.fast", []byte("hello"))
	drained, elapsedMillis := outbox.commitDrain(1)
	if drained != 1 || elapsedMillis >= 10_000 {
		t.Fatalf("drained=%d elapsed=%dms, want one before 10s fallback", drained, elapsedMillis)
	}
	message, ok := outbox.bus.next()
	if !ok || message.ID != id || message.Topic != "test.outbox.fast" {
		t.Fatalf("message = %#v, present=%v", message, ok)
	}
}
