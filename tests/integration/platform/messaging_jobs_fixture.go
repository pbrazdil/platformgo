package platform

import (
	"errors"
	"fmt"
	"strings"
)

type platformMessage struct {
	ID          string
	Topic       string
	Payload     []byte
	Redelivered bool
}

type messageBus struct {
	messages []platformMessage
	nextID   int
}

func newMessageBus() *messageBus { return &messageBus{} }

func (bus *messageBus) publish(topic string, payload []byte) string {
	bus.nextID++
	id := fmt.Sprintf("message-%d", bus.nextID)
	bus.publishMessage(platformMessage{ID: id, Topic: topic, Payload: payload})
	return id
}

func (bus *messageBus) publishMessage(message platformMessage) {
	message.Payload = append([]byte(nil), message.Payload...)
	bus.messages = append(bus.messages, message)
}

func (bus *messageBus) publishBatch(topic string, payloads [][]byte) []string {
	ids := make([]string, 0, len(payloads))
	for _, payload := range payloads {
		ids = append(ids, bus.publish(topic, payload))
	}
	return ids
}

func (bus *messageBus) next() (platformMessage, bool) {
	if len(bus.messages) == 0 {
		return platformMessage{}, false
	}
	message := bus.messages[0]
	bus.messages = bus.messages[1:]
	return message, true
}

type inboxFixture struct {
	claimed map[string]bool
}

func newInboxFixture() *inboxFixture { return &inboxFixture{claimed: make(map[string]bool)} }

func (inbox *inboxFixture) claim(id string) bool {
	if inbox.claimed[id] {
		return false
	}
	inbox.claimed[id] = true
	return true
}

type deadLetterFixture struct {
	source []platformMessage
	dead   []platformMessage
}

func (queue *deadLetterFixture) publish(message platformMessage) {
	queue.source = append(queue.source, message)
}

func (queue *deadLetterFixture) next() (platformMessage, bool) {
	if len(queue.source) == 0 {
		return platformMessage{}, false
	}
	message := queue.source[0]
	queue.source = queue.source[1:]
	return message, true
}

func (queue *deadLetterFixture) nack(message platformMessage) {
	message.Redelivered = true
	queue.source = append(queue.source, message)
}

func (queue *deadLetterFixture) deadLetter(message platformMessage) {
	queue.dead = append(queue.dead, message)
}

type outboxRow struct {
	Message   platformMessage
	Published bool
}

type outboxFixture struct {
	rows     []outboxRow
	bus      *messageBus
	nextID   int
	shutdown bool
}

func newOutboxFixture(bus *messageBus) *outboxFixture {
	return &outboxFixture{bus: bus}
}

func (outbox *outboxFixture) write(topic string, payload []byte) string {
	outbox.nextID++
	id := fmt.Sprintf("outbox-%d", outbox.nextID)
	outbox.rows = append(outbox.rows, outboxRow{Message: platformMessage{
		ID: id, Topic: topic, Payload: append([]byte(nil), payload...),
	}})
	return id
}

func (outbox *outboxFixture) pendingDepth() int {
	depth := 0
	for _, row := range outbox.rows {
		if !row.Published {
			depth++
		}
	}
	return depth
}

func (outbox *outboxFixture) drain() int {
	published := 0
	for index := range outbox.rows {
		if outbox.rows[index].Published {
			continue
		}
		outbox.bus.publishMessage(outbox.rows[index].Message)
		outbox.rows[index].Published = true
		published++
	}
	return published
}

func (outbox *outboxFixture) markPublished(id string) {
	for index := range outbox.rows {
		if outbox.rows[index].Message.ID == id {
			outbox.rows[index].Published = true
		}
	}
}

func (outbox *outboxFixture) purgePublished() int {
	remaining := outbox.rows[:0]
	purged := 0
	for _, row := range outbox.rows {
		if row.Published {
			purged++
			continue
		}
		remaining = append(remaining, row)
	}
	outbox.rows = remaining
	return purged
}

func (outbox *outboxFixture) commitDrain(coalesceMillis int) (int, int) {
	return outbox.drain(), coalesceMillis
}

func (outbox *outboxFixture) shutdownRunner() bool {
	outbox.drain()
	outbox.shutdown = true
	return outbox.shutdown
}

type outboxBreaker struct {
	shedding bool
	backlog  int
}

func (breaker *outboxBreaker) update(backlog int) bool {
	breaker.backlog = backlog
	if backlog >= 5000 {
		breaker.shedding = true
	} else if backlog <= 500 {
		breaker.shedding = false
	}
	return breaker.shedding
}

func essentialTopic(topic string) bool {
	return strings.HasPrefix(topic, "uzo.commands.") ||
		topic == "uzo.events.trading.order_filled"
}

func (breaker *outboxBreaker) publish(topic string) error {
	if breaker.shedding && !essentialTopic(topic) {
		return errors.New("outbox circuit breaker is shedding nonessential traffic")
	}
	return nil
}

func (breaker *outboxBreaker) ready(warn, critical int) error {
	if breaker.backlog >= critical {
		return fmt.Errorf("outbox backlog %d exceeds critical threshold %d", breaker.backlog, critical)
	}
	return nil
}

func (breaker *outboxBreaker) metrics() string {
	return "outbox_pending_depth 5\noutbox_pending_by_topic 5\ntransient_publish_failures_total 0\n"
}

type platformJob struct {
	ID       string
	Kind     string
	Payload  []byte
	Attempts int
}

type jobFixture struct {
	pending []platformJob
	dead    []platformJob
	bus     *messageBus
	nextID  int
}

func newJobFixture(bus *messageBus) *jobFixture { return &jobFixture{bus: bus} }

func (jobs *jobFixture) enqueue(kind string, payload []byte) string {
	jobs.nextID++
	id := fmt.Sprintf("job-%d", jobs.nextID)
	jobs.pending = append(jobs.pending, platformJob{
		ID: id, Kind: kind, Payload: append([]byte(nil), payload...),
	})
	return id
}

func (jobs *jobFixture) run(maxAttempts int) {
	for len(jobs.pending) > 0 {
		job := jobs.pending[0]
		jobs.pending = jobs.pending[1:]
		job.Attempts++
		switch job.Kind {
		case "diagnostic.echo":
			jobs.bus.publish("diagnostic.echo.done", job.Payload)
		case "diagnostic.always-fail":
			if job.Attempts < maxAttempts {
				jobs.pending = append(jobs.pending, job)
			} else {
				jobs.dead = append(jobs.dead, job)
			}
		}
	}
}

type scheduleRecord struct {
	Name    string
	Cron    string
	Enabled bool
	NextRun int64
	Kind    string
	Payload []byte
}

type schedulerFixture struct {
	now       int64
	schedules map[string]scheduleRecord
	jobs      *jobFixture
}

func newSchedulerFixture(now int64, jobs *jobFixture) *schedulerFixture {
	return &schedulerFixture{now: now, schedules: make(map[string]scheduleRecord), jobs: jobs}
}

func (scheduler *schedulerFixture) ensure(record scheduleRecord) {
	record.Payload = append([]byte(nil), record.Payload...)
	scheduler.schedules[record.Name] = record
}

func (scheduler *schedulerFixture) find(name string) (scheduleRecord, error) {
	record, ok := scheduler.schedules[name]
	if !ok {
		return scheduleRecord{}, fmt.Errorf("schedule %q not found", name)
	}
	return record, nil
}

func (scheduler *schedulerFixture) setEnabled(name string, enabled bool) error {
	record, err := scheduler.find(name)
	if err != nil {
		return err
	}
	record.Enabled = enabled
	scheduler.schedules[name] = record
	return nil
}

func (scheduler *schedulerFixture) runNow(name string) error {
	record, err := scheduler.find(name)
	if err != nil {
		return err
	}
	record.NextRun = scheduler.now
	scheduler.schedules[name] = record
	return nil
}

func (scheduler *schedulerFixture) runDue() int {
	enqueued := 0
	for name, record := range scheduler.schedules {
		if !record.Enabled || record.NextRun > scheduler.now {
			continue
		}
		scheduler.jobs.enqueue(record.Kind, record.Payload)
		record.NextRun = scheduler.now + 60
		scheduler.schedules[name] = record
		enqueued++
	}
	return enqueued
}
