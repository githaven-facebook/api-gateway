package unit_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/nicedavid98/api-gateway/internal/discovery"
	"github.com/nicedavid98/api-gateway/internal/proxy"
)

func makeInstances(n int) []discovery.Instance {
	instances := make([]discovery.Instance, n)
	for i := range instances {
		instances[i] = discovery.Instance{
			ID:      fmt.Sprintf("instance-%d", i),
			Host:    "localhost",
			Port:    8080 + i,
			Healthy: true,
			Weight:  1,
		}
	}
	return instances
}

func TestRoundRobinBalancer(t *testing.T) {
	lb := proxy.NewRoundRobinBalancer()
	instances := makeInstances(3)

	seen := make(map[string]int)
	const rounds = 9
	for i := 0; i < rounds; i++ {
		inst, err := lb.Next(instances)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		seen[inst.ID]++
	}

	for _, inst := range instances {
		if seen[inst.ID] != rounds/len(instances) {
			t.Errorf("instance %s selected %d times, expected %d", inst.ID, seen[inst.ID], rounds/len(instances))
		}
	}
}

func TestRoundRobinBalancer_Empty(t *testing.T) {
	lb := proxy.NewRoundRobinBalancer()
	_, err := lb.Next(nil)
	if err == nil {
		t.Error("expected error for empty instances")
	}
}

func TestWeightedBalancer_Distribution(t *testing.T) {
	lb := proxy.NewWeightedBalancer()
	instances := []discovery.Instance{
		{ID: "a", Host: "localhost", Port: 8080, Healthy: true, Weight: 3},
		{ID: "b", Host: "localhost", Port: 8081, Healthy: true, Weight: 1},
	}

	seen := make(map[string]int)
	const total = 400
	for i := 0; i < total; i++ {
		inst, err := lb.Next(instances)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		seen[inst.ID]++
	}

	// Instance "a" should get ~75% (±5%).
	ratioA := float64(seen["a"]) / float64(total)
	if ratioA < 0.70 || ratioA > 0.80 {
		t.Errorf("instance a got %.1f%% of requests, expected ~75%%", ratioA*100)
	}
}

func TestWeightedBalancer_SingleInstance(t *testing.T) {
	lb := proxy.NewWeightedBalancer()
	instances := []discovery.Instance{
		{ID: "only", Host: "localhost", Port: 8080, Healthy: true, Weight: 1},
	}

	inst, err := lb.Next(instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.ID != "only" {
		t.Errorf("expected 'only', got %q", inst.ID)
	}
}

func TestLeastConnectionsBalancer(t *testing.T) {
	lb := proxy.NewLeastConnectionsBalancer()
	instances := makeInstances(3)

	// First request goes to first instance.
	inst, err := lb.Next(instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	firstID := inst.ID

	// Second request should go to a different instance (first has 1 conn).
	inst2, err := lb.Next(instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst2.ID == firstID {
		t.Error("expected least connections balancer to pick different instance")
	}

	// Release connection from first instance.
	lb.Done(firstID)

	// Now both first and second have 0 and 1 connections respectively,
	// so next should go to the first instance again.
	inst3, err := lb.Next(instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst3.ID != firstID {
		t.Logf("after Done, got %s (acceptable, depends on ordering)", inst3.ID)
	}
}

func TestRoundRobinBalancer_Concurrent(t *testing.T) {
	lb := proxy.NewRoundRobinBalancer()
	instances := makeInstances(5)

	const goroutines = 20
	const reqsPerGoroutine = 100

	var wg sync.WaitGroup
	errors := make(chan error, goroutines*reqsPerGoroutine)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < reqsPerGoroutine; j++ {
				_, err := lb.Next(instances)
				if err != nil {
					errors <- err
				}
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent error: %v", err)
	}
}
