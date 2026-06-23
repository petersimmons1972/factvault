package workers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/petersimmons1972/factvault/internal/collectors"
)

type rssCollectCall struct {
	tenantID string
}

type rssPipelineStub struct {
	calls []rssCollectCall
}

func (s *rssPipelineStub) CollectOnce(_ context.Context, tenantID string, _ collectors.Collector) error {
	s.calls = append(s.calls, rssCollectCall{tenantID: tenantID})
	return nil
}

func TestRSSWorkerRespectsTenantFlag(t *testing.T) {
	pipeline := &rssPipelineStub{}
	worker := RSSWorker{
		Pipeline:        pipeline,
		TenantID:        "tenant-from-flag",
		DefaultInterval: time.Minute,
	}

	err := worker.RunOnce(context.Background(), []collectors.FeedSpec{{
		URL:      "https://example.com/feed.xml",
		TenantID: "tenant-from-feed",
	}})
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if len(pipeline.calls) != 1 {
		t.Fatalf("collect calls=%d want 1", len(pipeline.calls))
	}
	if got := pipeline.calls[0].tenantID; got != "tenant-from-flag" {
		t.Fatalf("tenantID=%q want tenant-from-flag", got)
	}
}

func TestRSSWorkerDefaultTenant(t *testing.T) {
	pipeline := &rssPipelineStub{}
	worker := RSSWorker{
		Pipeline:        pipeline,
		DefaultInterval: time.Minute,
	}

	err := worker.RunOnce(context.Background(), []collectors.FeedSpec{{
		URL:      "https://example.com/feed.xml",
		TenantID: "tenant-from-feed",
	}})
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if len(pipeline.calls) != 1 {
		t.Fatalf("collect calls=%d want 1", len(pipeline.calls))
	}
	if got := pipeline.calls[0].tenantID; got != "tenant-from-feed" {
		t.Fatalf("tenantID=%q want tenant-from-feed", got)
	}
}

func TestRSSWorkerRejectsNoEligibleFeeds(t *testing.T) {
	worker := RSSWorker{Pipeline: &rssPipelineStub{}, DefaultInterval: time.Minute}

	err := worker.RunOnce(context.Background(), []collectors.FeedSpec{{URL: "https://example.com/feed.xml"}})
	if err == nil {
		t.Fatal("expected no eligible feeds error")
	}
}

func TestRSSWorkerRejectsPartiallyUnconfiguredFeeds(t *testing.T) {
	pipeline := &rssPipelineStub{}
	worker := RSSWorker{Pipeline: pipeline, DefaultInterval: time.Minute}

	err := worker.RunOnce(context.Background(), []collectors.FeedSpec{
		{Name: "good", URL: "https://example.com/good.xml", TenantID: "tenant-from-feed"},
		{Name: "bad", URL: "https://example.com/bad.xml"},
	})
	if err == nil {
		t.Fatal("expected missing tenant error")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Fatalf("error %q does not identify misconfigured feed", err)
	}
	if len(pipeline.calls) != 0 {
		t.Fatalf("collect calls=%d want 0", len(pipeline.calls))
	}
}

func TestRSSScheduleHelpers(t *testing.T) {
	feeds := []collectors.FeedSpec{
		{TenantID: "t1", Interval: "10m"},
		{TenantID: "t-mid", Interval: "5m"},
		{TenantID: "t2", Interval: "30m"},
	}
	worker := RSSWorker{DefaultInterval: 15 * time.Minute}

	schedules, err := worker.buildSchedules(feeds)
	if err != nil {
		t.Fatalf("build schedules: %v", err)
	}
	if len(schedules) != 3 {
		t.Fatalf("len(schedules)=%d want 3", len(schedules))
	}
	idx := allRSSFeedIndexes(schedules)
	if len(idx) != 3 || idx[0] != 0 || idx[1] != 1 || idx[2] != 2 {
		t.Fatalf("indexes=%v", idx)
	}

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	due := dueRSSFeedIndexes(schedules, map[int]time.Time{}, now)
	if len(due) != 3 {
		t.Fatalf("initial due=%v want all feeds", due)
	}

	last := map[int]time.Time{0: now, 1: now, 2: now}
	if got := nextRSSPollWait(schedules, last, now); got != 5*time.Minute {
		t.Fatalf("next wait=%s want 5m", got)
	}
	due = dueRSSFeedIndexes(schedules, last, now.Add(5*time.Minute))
	if len(due) != 1 || due[0] != 1 {
		t.Fatalf("due at 5m=%v want only feed 1", due)
	}
	due = dueRSSFeedIndexes(schedules, last, now.Add(30*time.Minute))
	if len(due) != 3 {
		t.Fatalf("due at 30m=%v want all", due)
	}
}

func TestRSSOnceUsesAllScheduledFeeds(t *testing.T) {
	schedules := []rssSchedule{{feedIndex: 2, interval: time.Minute}, {feedIndex: 7, interval: 2 * time.Minute}}
	idx := allRSSFeedIndexes(schedules)
	if len(idx) != 2 || idx[0] != 2 || idx[1] != 7 {
		t.Fatalf("once indexes=%v", idx)
	}
}
