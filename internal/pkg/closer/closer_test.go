package closer_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/pkg/closer"
	"github.com/stretchr/testify/require"
)

func TestClose_LIFOOrder(t *testing.T) {
	var order []string
	c := closer.New()
	c.Add("first", func() error {
		order = append(order, "first")

		return nil
	})
	c.Add("second", func() error {
		order = append(order, "second")

		return nil
	})

	require.NoError(t, c.Close(context.Background()))
	require.Equal(t, []string{"second", "first"}, order)
}

func TestClose_JoinsErrors(t *testing.T) {
	c := closer.New()
	c.Add("ok", func() error { return nil })
	c.Add("boom", func() error { return errors.New("boom") })
	c.Add("bang", func() error { return errors.New("bang") })

	err := c.Close(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "boom")
	require.Contains(t, err.Error(), "bang")
}

func TestClose_Idempotent(t *testing.T) {
	calls := 0
	c := closer.New()
	c.Add("once", func() error {
		calls++

		return nil
	})

	require.NoError(t, c.Close(context.Background()))
	require.NoError(t, c.Close(context.Background()))
	require.Equal(t, 1, calls)
}

func TestClose_HonorsContext(t *testing.T) {
	c := closer.New()
	block := make(chan struct{})
	started := make(chan struct{})
	c.Add("slow", func() error {
		close(started)
		<-block

		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Close(ctx) }()

	<-started
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not honor context deadline")
	}
	close(block)
}

func TestAdd_AfterClose_RunsImmediately(t *testing.T) {
	// Ресурс, добавленный после shutdown, не должен утекать: Close уже прошёл,
	// Add выполняет fn немедленно.
	ran := false
	c := closer.New()
	require.NoError(t, c.Close(context.Background()))

	c.Add("late", func() error {
		ran = true

		return nil
	})

	require.True(t, ran)
}
