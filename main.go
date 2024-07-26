package main

import (
	"github.com/robfig/cron/v3"
	"log"
	"os"
	"os/exec"
)

func runCmd() {
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
	select {}
}
