package billing

import (
	"testing"
	"time"
)

func TestBuildSubscriptionTimelinePromotesHigherTierAndPreservesLowerDuration(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	segments, err := buildSubscriptionTimeline(
		[]subscriptionTimelineSegment{
			{SubscriptionID: 100, PlanID: 2, PriceID: 20, Rank: 20, StartAt: now, EndAt: now.Add(30 * day)},
		},
		subscriptionTimelineGrant{PlanID: 3, PriceID: 30, Rank: 30, StartAt: now, Duration: 30 * day, NewGrant: true},
	)
	if err != nil {
		t.Fatalf("buildSubscriptionTimeline() error = %v", err)
	}
	assertTimeline(t, segments, []subscriptionTimelineSegment{
		{PlanID: 3, PriceID: 30, Rank: 30, StartAt: now, EndAt: now.Add(30 * day), NewGrant: true},
		{SubscriptionID: 100, PlanID: 2, PriceID: 20, Rank: 20, StartAt: now.Add(30 * day), EndAt: now.Add(60 * day)},
	})
}

func TestBuildSubscriptionTimelinePromotesHigherTierInsideLongerLowerTier(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	segments, err := buildSubscriptionTimeline(
		[]subscriptionTimelineSegment{
			{SubscriptionID: 100, PlanID: 2, PriceID: 20, Rank: 20, StartAt: now, EndAt: now.Add(90 * day)},
		},
		subscriptionTimelineGrant{PlanID: 3, PriceID: 30, Rank: 30, StartAt: now.Add(30 * day), Duration: 30 * day, NewGrant: true},
	)
	if err != nil {
		t.Fatalf("buildSubscriptionTimeline() error = %v", err)
	}
	assertTimeline(t, segments, []subscriptionTimelineSegment{
		{SubscriptionID: 100, PlanID: 2, PriceID: 20, Rank: 20, StartAt: now, EndAt: now.Add(30 * day)},
		{PlanID: 3, PriceID: 30, Rank: 30, StartAt: now.Add(30 * day), EndAt: now.Add(60 * day), NewGrant: true},
		{SubscriptionID: 100, PlanID: 2, PriceID: 20, Rank: 20, StartAt: now.Add(60 * day), EndAt: now.Add(120 * day)},
	})
}

func TestBuildSubscriptionTimelineQueuesLowerTierBehindHigherTier(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	segments, err := buildSubscriptionTimeline(
		[]subscriptionTimelineSegment{
			{PlanID: 3, PriceID: 30, Rank: 30, StartAt: now, EndAt: now.Add(30 * day)},
		},
		subscriptionTimelineGrant{PlanID: 2, PriceID: 20, Rank: 20, StartAt: now, Duration: 30 * day, NewGrant: true},
	)
	if err != nil {
		t.Fatalf("buildSubscriptionTimeline() error = %v", err)
	}
	assertTimeline(t, segments, []subscriptionTimelineSegment{
		{PlanID: 3, PriceID: 30, Rank: 30, StartAt: now, EndAt: now.Add(30 * day)},
		{PlanID: 2, PriceID: 20, Rank: 20, StartAt: now.Add(30 * day), EndAt: now.Add(60 * day), NewGrant: true},
	})
}

func TestBuildSubscriptionTimelineFillsGapBeforeFutureHigherTier(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	segments, err := buildSubscriptionTimeline(
		[]subscriptionTimelineSegment{
			{PlanID: 3, PriceID: 30, Rank: 30, StartAt: now.Add(10 * day), EndAt: now.Add(40 * day)},
		},
		subscriptionTimelineGrant{PlanID: 2, PriceID: 20, Rank: 20, StartAt: now, Duration: 30 * day, NewGrant: true},
	)
	if err != nil {
		t.Fatalf("buildSubscriptionTimeline() error = %v", err)
	}
	assertTimeline(t, segments, []subscriptionTimelineSegment{
		{PlanID: 2, PriceID: 20, Rank: 20, StartAt: now, EndAt: now.Add(10 * day), NewGrant: true},
		{PlanID: 3, PriceID: 30, Rank: 30, StartAt: now.Add(10 * day), EndAt: now.Add(40 * day)},
		{PlanID: 2, PriceID: 20, Rank: 20, StartAt: now.Add(40 * day), EndAt: now.Add(60 * day), NewGrant: true},
	})
}

func TestBuildSubscriptionTimelineQueuesSameTierContiguously(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	segments, err := buildSubscriptionTimeline(
		[]subscriptionTimelineSegment{
			{PlanID: 2, PriceID: 20, Rank: 20, StartAt: now, EndAt: now.Add(30 * day)},
		},
		subscriptionTimelineGrant{PlanID: 2, PriceID: 20, Rank: 20, StartAt: now, Duration: 30 * day, NewGrant: true},
	)
	if err != nil {
		t.Fatalf("buildSubscriptionTimeline() error = %v", err)
	}
	assertTimeline(t, segments, []subscriptionTimelineSegment{
		{PlanID: 2, PriceID: 20, Rank: 20, StartAt: now, EndAt: now.Add(30 * day)},
		{PlanID: 2, PriceID: 20, Rank: 20, StartAt: now.Add(30 * day), EndAt: now.Add(60 * day), NewGrant: true},
	})
}

func assertTimeline(t *testing.T, got []subscriptionTimelineSegment, want []subscriptionTimelineSegment) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("timeline len = %d, want %d: %#v", len(got), len(want), got)
	}
	for index := range want {
		if got[index].PlanID != want[index].PlanID ||
			got[index].SubscriptionID != want[index].SubscriptionID ||
			got[index].PriceID != want[index].PriceID ||
			got[index].Rank != want[index].Rank ||
			!got[index].StartAt.Equal(want[index].StartAt) ||
			!got[index].EndAt.Equal(want[index].EndAt) ||
			got[index].NewGrant != want[index].NewGrant {
			t.Fatalf("timeline[%d] = %#v, want %#v", index, got[index], want[index])
		}
	}
}
