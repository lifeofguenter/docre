package main

import (
	"github.com/robfig/cron/v3"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const WaitTimeout = 110

var wg sync.WaitGroup

func runCmd() {
	wg.Add(1)
	defer wg.Done()

	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("ERROR: %s", err)
	} else {
		log.Printf("%s", output)
	}
}

func main() {
	c := cron.New()
	_, err := c.AddFunc(os.Getenv("CRONTAB"), runCmd)
	if err != nil {
		log.Printf("Error: %s", err)
		os.Exit(1)
	}

	c.Start()

	// Create a channel to receive OS signals.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	// Wait for a signal.
	sig := <-sigChan
	log.Printf("Received signal: %s. Waiting for %d seconds before exiting...", sig, WaitTimeout)

	// Stop the cron scheduler from running new jobs.
	for _, entry := range c.Entries() {
		c.Remove(entry.ID)
	}

	// Wait for the wait group to complete if there are active jobs
	doneChan := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneChan)
	}()

	select {
	case <-doneChan:
		log.Println("All jobs completed. Exiting now.")
	case <-time.After(WaitTimeout * time.Second):
		log.Println("Wait timeout reached. Exiting now.")
	}

	c.Stop()
	os.Exit(0)
}
