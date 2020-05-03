package main

import (
	"fmt"
	"github.com/robfig/cron/v3"
	"os"
	"os/exec"
)

func runCmd() {
	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	output, _ := cmd.CombinedOutput()
	fmt.Printf("%s", output)
}

func main() {
	c := cron.New()
	c.AddFunc(os.Getenv("CRONTAB"), runCmd)
	c.Start()
	select {}
}
