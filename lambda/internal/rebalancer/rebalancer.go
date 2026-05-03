// Package rebalancer holds the periodic-scheduler logic that closes the
// stranded-queued-jobs drift cycle. See spec at
// repositories/zettelkasten/Projects/jit-runners/specs/2026-05-02-effective-scaleup-design.md.
package rebalancer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/github"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/queue"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/state"
)

// QueueLister is the narrow GitHub surface the rebalancer needs.
// Satisfied by *github.Client.
type QueueLister interface {
	ListQueuedWorkflowJobs(ctx context.Context, ownerRepo string) ([]github.QueuedJob, error)
}

// ScaleUpPublisher is the narrow SQS publisher surface. Satisfied by
// *aws/sqs.Publisher via its PublishScaleUp helper.
type ScaleUpPublisher interface {
	PublishScaleUp(ctx context.Context, msg *queue.ScaleUpMessage) error
}

// Rebalance computes per-label-set demand from GitHub and supply from DDB
// pending runners, and publishes (demand - supply) ScaleUpMessages with
// Source=SourceRebalancer for each gap. The published messages flow through
// the same scaleup launch pipeline as webhook-fired ones, but scaleup
// launches them unconditionally because Source=SourceRebalancer.
//
// Errors during GH listing or DDB scan are returned without publishing.
// Errors during a publish are aggregated; partial-success publishes remain
// in flight (the next cycle re-fires whatever wasn't delivered, since the
// queued jobs still appear in GH).
func Rebalance(ctx context.Context, gh QueueLister, store state.RunnerStore, pub ScaleUpPublisher, repoFull string, installationID int64) error {
	queued, err := gh.ListQueuedWorkflowJobs(ctx, repoFull)
	if err != nil {
		return fmt.Errorf("rebalancer: list queued jobs: %w", err)
	}

	pending, err := store.List(ctx, state.Filter{StatusEq: state.StatusPending})
	if err != nil {
		return fmt.Errorf("rebalancer: list pending runners: %w", err)
	}

	groups := groupByLabels(queued)
	var publishErrs []error
	totalPublished := 0
	for _, g := range groups {
		supply := 0
		for _, r := range pending {
			if state.MatchesLabels(r.Labels, g.labels) {
				supply++
			}
		}
		gap := len(g.jobs) - supply
		if gap <= 0 {
			continue
		}
		anchor := g.jobs[0]
		for i := 0; i < gap; i++ {
			msg := &queue.ScaleUpMessage{
				EventAction:    "queued",
				JobID:          anchor.JobID,
				RunID:          anchor.RunID,
				RepositoryFull: repoFull,
				Labels:         g.labels,
				InstallationID: installationID,
				Source:         queue.SourceRebalancer,
			}
			if err := pub.PublishScaleUp(ctx, msg); err != nil {
				publishErrs = append(publishErrs, fmt.Errorf("publish for labels %v: %w", g.labels, err))
				continue
			}
			totalPublished++
		}
	}
	log.Printf("rebalancer: cycle complete repo=%s demand=%d supply=%d published=%d label_sets=%d",
		repoFull, len(queued), len(pending), totalPublished, len(groups))
	if len(publishErrs) > 0 {
		return errors.Join(publishErrs...)
	}
	return nil
}

type labelGroup struct {
	labels []string
	jobs   []github.QueuedJob
}

// groupByLabels groups queued jobs by their normalized label set. Jobs whose
// labels are equal-as-sets (case- and order-insensitive) go in the same
// group. Order of jobs within a group is preserved (so jobs[0] is the
// earliest the GH API returned, useful as the anchor job_id for the
// published message).
func groupByLabels(jobs []github.QueuedJob) []*labelGroup {
	idx := map[string]*labelGroup{}
	var order []*labelGroup
	for _, j := range jobs {
		k := labelKey(j.Labels)
		g, ok := idx[k]
		if !ok {
			g = &labelGroup{labels: j.Labels}
			idx[k] = g
			order = append(order, g)
		}
		g.jobs = append(g.jobs, j)
	}
	return order
}

// labelKey returns a stable string key for a label set: lowercase, sorted,
// nul-separated. Used purely for grouping equal label sets together.
func labelKey(labels []string) string {
	s := make([]string, len(labels))
	for i, l := range labels {
		s[i] = lower(l)
	}
	sort.Strings(s)
	key := ""
	for _, x := range s {
		key += "\x00" + x
	}
	return key
}

func lower(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
