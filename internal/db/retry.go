package db

import (
	"context"
	"time"

	"github.com/cenkalti/backoff/v5"
)

// NewBackOff creates an exponential backoff with the given initial and max intervals.
func NewBackOff(initial, max time.Duration) *backoff.ExponentialBackOff {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = initial
	b.MaxInterval = max
	b.RandomizationFactor = 0.5
	b.Multiplier = 2.0
	return b
}

// Retry executes fn, retrying on transient errors.
// Non-transient errors stop retrying immediately.
func Retry(ctx context.Context, fn func() error, opts ...backoff.RetryOption) error {
	_, err := backoff.Retry(ctx, func() (struct{}, error) {
		err := fn()
		if err != nil && !IsTransient(err) {
			return struct{}{}, backoff.Permanent(err)
		}
		return struct{}{}, err
	}, opts...)
	return err
}

// RetryValue executes fn, retrying on transient errors.
// Returns the value from fn on success.
func RetryValue[T any](ctx context.Context, fn func() (T, error), opts ...backoff.RetryOption) (T, error) {
	return backoff.Retry(ctx, func() (T, error) {
		val, err := fn()
		if err != nil && !IsTransient(err) {
			return val, backoff.Permanent(err)
		}
		return val, err
	}, opts...)
}
