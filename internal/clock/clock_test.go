package clock

import (
	"context"
	"testing"
	"time"
)

func TestReal(t *testing.T) {
	c := Real()
	w, m := c.Wall(), c.Mono()
	if w < 1.7e9 || m < 0 {
		t.Fatalf("wall %v mono %v", w, m)
	}
	if err := c.Sleep(context.Background(), 0.01); err != nil {
		t.Fatal(err)
	}
	if c.Mono()-m < 0.009 {
		t.Fatal("slept too short")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c.Sleep(ctx, 10) == nil {
		t.Fatal("a cancelled sleep slept")
	}
	select {
	case <-c.After(0.01):
	case <-time.After(5 * time.Second):
		t.Fatal("After never fired")
	}
	<-c.After(0)
}

func TestFake(t *testing.T) {
	f := NewFake(1000, 5)
	done := make(chan error, 1)
	go func() { done <- f.Sleep(context.Background(), 2) }()
	for f.Waiting() == 0 {
		time.Sleep(time.Millisecond)
	}
	f.SetWall(0) // someone set the clock: sleepers keep their time
	f.Advance(1.5)
	select {
	case <-done:
		t.Fatal("woke early")
	case <-time.After(20 * time.Millisecond):
	}
	f.Advance(0.5)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if f.Wall() != 2 || f.Mono() != 7 {
		t.Fatalf("wall %v mono %v", f.Wall(), f.Mono())
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- f.Sleep(ctx, 100) }()
	for f.Waiting() == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != context.Canceled || f.Waiting() != 0 {
		t.Fatalf("cancel: %v, %d waiting", err, f.Waiting())
	}
	a, b := f.After(3), f.After(1)
	f.Advance(5)
	<-a
	<-b
	if f.Sleep(context.Background(), 0) != nil {
		t.Fatal("zero sleep")
	}
}
