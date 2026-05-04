package state

import "testing"

func TestMatchesLabels(t *testing.T) {
	cases := []struct {
		name         string
		runnerLabels []string
		jobLabels    []string
		want         bool
	}{
		{
			name:         "exact match",
			runnerLabels: []string{"self-hosted", "large"},
			jobLabels:    []string{"self-hosted", "large"},
			want:         true,
		},
		{
			name:         "runner superset of job",
			runnerLabels: []string{"self-hosted", "large", "x64"},
			jobLabels:    []string{"self-hosted", "large"},
			want:         true,
		},
		{
			name:         "job has label runner does not",
			runnerLabels: []string{"self-hosted", "large"},
			jobLabels:    []string{"self-hosted", "large", "x64"},
			want:         false,
		},
		{
			name:         "case insensitive",
			runnerLabels: []string{"Self-Hosted", "Large"},
			jobLabels:    []string{"self-hosted", "large"},
			want:         true,
		},
		{
			name:         "ordering insensitive",
			runnerLabels: []string{"large", "self-hosted"},
			jobLabels:    []string{"self-hosted", "large"},
			want:         true,
		},
		{
			name:         "empty job labels: any runner matches",
			runnerLabels: []string{"self-hosted"},
			jobLabels:    []string{},
			want:         true,
		},
		{
			name:         "empty runner labels with non-empty job: false",
			runnerLabels: []string{},
			jobLabels:    []string{"self-hosted"},
			want:         false,
		},
		{
			name:         "completely disjoint",
			runnerLabels: []string{"a", "b"},
			jobLabels:    []string{"c", "d"},
			want:         false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := MatchesLabels(tc.runnerLabels, tc.jobLabels)
			if got != tc.want {
				t.Errorf("MatchesLabels(%v, %v) = %v, want %v", tc.runnerLabels, tc.jobLabels, got, tc.want)
			}
		})
	}
}
