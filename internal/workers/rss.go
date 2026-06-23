package workers

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/petersimmons1972/factvault/internal/collectors"
)

type rssCollectorRunner interface {
	CollectOnce(ctx context.Context, tenantID string, c collectors.Collector) error
}

// RSSWorker polls configured feeds and ingests collected items into the source pipeline.
type RSSWorker struct {
	Pipeline        rssCollectorRunner
	TenantID        string
	DefaultInterval time.Duration
}

// RunOnce ingests every scheduled feed exactly once.
func (w RSSWorker) RunOnce(ctx context.Context, feeds []collectors.FeedSpec) error {
	schedules, err := w.buildSchedules(feeds)
	if err != nil {
		return err
	}
	return w.runForFeeds(ctx, feeds, allRSSFeedIndexes(schedules))
}

// Run polls configured feeds continuously until ctx is canceled.
func (w RSSWorker) Run(ctx context.Context, feeds []collectors.FeedSpec) error {
	schedules, err := w.buildSchedules(feeds)
	if err != nil {
		return err
	}
	lastPolled := map[int]time.Time{}
	for {
		now := time.Now().UTC()
		due := dueRSSFeedIndexes(schedules, lastPolled, now)
		if len(due) > 0 {
			if err := w.runForFeeds(ctx, feeds, due); err != nil {
				return err
			}
			for _, i := range due {
				lastPolled[i] = now
			}
		}
		wait := nextRSSPollWait(schedules, lastPolled, now)
		if wait <= 0 {
			wait = time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (w RSSWorker) runForFeeds(ctx context.Context, feeds []collectors.FeedSpec, feedIdx []int) error {
	for _, i := range feedIdx {
		feed := feeds[i]
		collector := collectors.RSSCollector{Spec: feed}
		if err := w.Pipeline.CollectOnce(ctx, w.tenantID(feed), collector); err != nil {
			return err
		}
	}
	return nil
}

func (w RSSWorker) tenantID(feed collectors.FeedSpec) string {
	if trimmed := strings.TrimSpace(w.TenantID); trimmed != "" {
		return trimmed
	}
	return feed.TenantID
}

func (w RSSWorker) buildSchedules(feeds []collectors.FeedSpec) ([]rssSchedule, error) {
	out := make([]rssSchedule, 0, len(feeds))
	missingTenant := make([]string, 0)
	for i, feed := range feeds {
		if strings.TrimSpace(w.tenantID(feed)) == "" {
			missingTenant = append(missingTenant, rssFeedLabel(feed, i))
			continue
		}
		out = append(out, rssSchedule{feedIndex: i, interval: feed.PollInterval(w.defaultInterval())})
	}
	if len(missingTenant) > 0 {
		slices.Sort(missingTenant)
		return nil, fmt.Errorf("rss worker: feeds missing tenant: %s; set --tenant or configure each feed tenant", strings.Join(missingTenant, ", "))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("rss worker: no eligible feeds; set --tenant or configure feed tenants")
	}
	return out, nil
}

func (w RSSWorker) defaultInterval() time.Duration {
	if w.DefaultInterval > 0 {
		return w.DefaultInterval
	}
	return 15 * time.Minute
}

type rssSchedule struct {
	feedIndex int
	interval  time.Duration
}

func allRSSFeedIndexes(schedules []rssSchedule) []int {
	idx := make([]int, 0, len(schedules))
	for _, s := range schedules {
		idx = append(idx, s.feedIndex)
	}
	return idx
}

func dueRSSFeedIndexes(schedules []rssSchedule, lastPolled map[int]time.Time, now time.Time) []int {
	due := make([]int, 0, len(schedules))
	for _, s := range schedules {
		last, ok := lastPolled[s.feedIndex]
		if !ok || now.Sub(last) >= s.interval {
			due = append(due, s.feedIndex)
		}
	}
	return due
}

func nextRSSPollWait(schedules []rssSchedule, lastPolled map[int]time.Time, now time.Time) time.Duration {
	if len(schedules) == 0 {
		return time.Second
	}
	var minWait time.Duration = -1
	for _, s := range schedules {
		last, ok := lastPolled[s.feedIndex]
		if !ok {
			return 0
		}
		wait := max(s.interval-now.Sub(last), 0)
		if minWait < 0 || wait < minWait {
			minWait = wait
		}
	}
	return minWait
}

func rssFeedLabel(feed collectors.FeedSpec, index int) string {
	if trimmed := strings.TrimSpace(feed.Name); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(feed.URL); trimmed != "" {
		return trimmed
	}
	return fmt.Sprintf("feed[%d]", index)
}
