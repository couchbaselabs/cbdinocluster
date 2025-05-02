package main

import (
	"log"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/couchbaselabs/cbdinocluster/utils/dinocerts"
)

func main() {
	seedsToTest := make(chan string, 10000)

	var charSet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*())-=_+[]{}|;':\",./<>?"
	for _, char1 := range charSet {
		seedsToTest <- string(char1)
	}

	type testedSeed struct {
		Seed  string
		Delta time.Duration
	}
	var testedSeeds []testedSeed
	var testedSeedsLock sync.Mutex

	testOne := func(seed string) {
		stime := time.Now()

		dinocerts.GenerateKeyUncached(seed)

		etime := time.Now()
		dtime := etime.Sub(stime)

		log.Printf("Seed: %s, Time: %v", seed, dtime)

		testedSeedsLock.Lock()
		testedSeeds = append(testedSeeds, testedSeed{Seed: seed, Delta: dtime})
		testedSeedsLock.Unlock()
	}

	// force gomaxprocs to numcpu
	runtime.GOMAXPROCS(runtime.NumCPU())

	// run on each cpu
	var wg sync.WaitGroup
	wg.Add(runtime.NumCPU())
	for i := 0; i < runtime.NumCPU(); i++ {
		go func() {
			for {
				select {
				case seed := <-seedsToTest:
					testOne(seed)
				default:
					wg.Done()
					return
				}
			}
		}()
	}

	wg.Wait()

	slices.SortFunc(testedSeeds, func(a, b testedSeed) int {
		return int(a.Delta - b.Delta)
	})

	bestSeeds := testedSeeds[:10]
	for _, bestSeed := range bestSeeds {
		log.Printf("Best Seed: %s, Time: %v", bestSeed.Seed, bestSeed.Delta)
	}
}
