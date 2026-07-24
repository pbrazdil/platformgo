package platform

import "testing"

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/jobs/e2e_jobs.rs:47
// test: typed_job_consume_roundtrip
func TestTypedJobConsumeRoundtrip(t *testing.T) {
	bus := newMessageBus()
	jobs := newJobFixture(bus)
	jobs.enqueue("diagnostic.echo", []byte("ping"))
	jobs.run(3)
	message, ok := bus.next()
	if !ok || message.Topic != "diagnostic.echo.done" || string(message.Payload) != "ping" {
		t.Fatalf("message = %#v, present=%v", message, ok)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/jobs/e2e_jobs.rs:120
// test: failing_job_is_retried_then_dead_lettered
func TestFailingJobIsRetriedThenDeadLettered(t *testing.T) {
	jobs := newJobFixture(newMessageBus())
	id := jobs.enqueue("diagnostic.always-fail", []byte("poison"))
	jobs.run(3)
	if len(jobs.dead) != 1 || jobs.dead[0].ID != id || jobs.dead[0].Attempts != 3 {
		t.Fatalf("dead jobs = %#v, want %q after 3 attempts", jobs.dead, id)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/jobs/e2e_scheduler.rs:48
// test: cron_loop_runs_a_due_code_registered_task
func TestCronLoopRunsADueCodeRegisteredTask(t *testing.T) {
	jobs := newJobFixture(newMessageBus())
	scheduler := newSchedulerFixture(100, jobs)
	scheduler.ensure(scheduleRecord{
		Name: "due-task", Cron: "* * * * *", Enabled: true, NextRun: 99,
		Kind: "test.scheduled", Payload: []byte("tick"),
	})
	if got := scheduler.runDue(); got != 1 {
		t.Fatalf("enqueued = %d, want 1", got)
	}
	if len(jobs.pending) != 1 || jobs.pending[0].Kind != "test.scheduled" ||
		string(jobs.pending[0].Payload) != "tick" {
		t.Fatalf("pending jobs = %#v", jobs.pending)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/jobs/e2e_scheduler.rs:84
// test: admin_surface_enable_disable_run_now_over_code_registered_schedules
func TestAdminSurfaceEnableDisableRunNowOverCodeRegisteredSchedules(t *testing.T) {
	jobs := newJobFixture(newMessageBus())
	scheduler := newSchedulerFixture(100, jobs)
	scheduler.ensure(scheduleRecord{
		Name: "tick-a", Cron: "0 */5 * * * *", Enabled: true, NextRun: 500,
		Kind: "test.scheduled", Payload: []byte("tick"),
	})
	record, err := scheduler.find("tick-a")
	if err != nil || !record.Enabled || record.Cron != "0 */5 * * * *" {
		t.Fatalf("schedule = %#v, err=%v", record, err)
	}
	if err := scheduler.setEnabled("tick-a", false); err != nil {
		t.Fatal(err)
	}
	record, _ = scheduler.find("tick-a")
	if record.Enabled {
		t.Fatal("schedule remained enabled")
	}
	if err := scheduler.setEnabled("tick-a", true); err != nil {
		t.Fatal(err)
	}
	previous := record.NextRun
	if err := scheduler.runNow("tick-a"); err != nil {
		t.Fatal(err)
	}
	record, _ = scheduler.find("tick-a")
	if !record.Enabled || record.NextRun >= previous {
		t.Fatalf("schedule after run-now = %#v, previous next run=%d", record, previous)
	}
	if err := scheduler.setEnabled("ghost", false); err == nil {
		t.Fatal("disabling missing schedule succeeded")
	}
	if err := scheduler.runNow("ghost"); err == nil {
		t.Fatal("running missing schedule succeeded")
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/jobs/e2e_worker.rs:10
// test: worker_runs_outbox_publisher
func TestWorkerRunsOutboxPublisher(t *testing.T) {
	bus := newMessageBus()
	outbox := newOutboxFixture(bus)
	id := outbox.write("test.worker.created", []byte("hi"))
	server := newServerFixture()
	if server.healthz() != "ok" || !server.ready.Ready {
		t.Fatal("worker health endpoints are not ready")
	}
	outbox.drain()
	message, ok := bus.next()
	if !ok || message.ID != id || message.Topic != "test.worker.created" {
		t.Fatalf("message = %#v, present=%v, want id=%q", message, ok, id)
	}
}
