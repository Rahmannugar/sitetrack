package outbox

import (
	"testing"
	"time"
)

func TestRetryDelayHasExponentialJitterAndHonorsProviderWait(t *testing.T) {
	for _, test := range []struct {
		attempt                int32
		provider, lower, upper time.Duration
	}{
		{1, 0, time.Second, 2 * time.Second},
		{4, 0, 8 * time.Second, 16 * time.Second},
		{4, 30 * time.Second, 30 * time.Second, 38 * time.Second},
		{8, 0, 64 * time.Second, 128 * time.Second},
	} {
		for range 100 {
			delay := retryDelay(test.attempt, test.provider)
			if delay < test.lower || delay > test.upper {
				t.Fatalf("retry delay outside bounds: %v", delay)
			}
		}
	}
}
