package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

const (
	pidPath       = "/var/run/%d/mr-backup-agent.pid"
	sleepDuration = 60 * 1000000000 // 60 seconds
)

var speeds = [...]int{-1, 20, 0}

func managePidFile(pidFile string) error {
	_, err := os.Stat(pidFile)
	if err == nil {
		fmt.Printf("Pid file %s exists. Exiting\n", pidFile)
		os.Exit(0)
	}

	file, err := os.Create(pidFile)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.Write([]byte(fmt.Sprintf("%d", os.Getpid())))
	if err != nil {
		return err
	}

	return nil
}

func setupSignalHandler(finish chan bool) {
	c := make(chan os.Signal)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		log.Printf("Signal received: %s", <-c)
		finish <- true
	}()
}

type schedule struct {
	ButtonState [][]int `json:"button_state"`
}

func parseSchedulerConf(conf string) schedule {
	cont, err := ioutil.ReadFile(conf)
	if err != nil {
		log.Fatal(err)
	}

	var schedule schedule
	// The scheduler is not 100% valid json, so skip the offending bytes.
	// This is dirty AF but I don't want to bother parsing it correctly.
	err = json.Unmarshal(cont[34:], &schedule)
	if err != nil {
		log.Fatal(err)
	}

	return schedule
}

func setupTestSpeedGetter(conf string, speed chan int) {
	sleepDuration := 5 * 1000000000
	go func() {
		hour := 0
		weekday := 0
		for {
			fmt.Println()
			log.Printf("Weekday: %d, Hour: %d", weekday, hour)

			schedule := parseSchedulerConf(conf)
			speed <- speeds[schedule.ButtonState[hour][weekday]]
			time.Sleep(time.Duration(sleepDuration))

			hour++
			if hour >= 24 {
				weekday = (weekday + 1) % 7
				hour = 0
			}
		}
	}()
}

func setupSpeedGetter(conf string, speed chan int) {
	go func() {
		for {
			schedule := parseSchedulerConf(conf)
			now := time.Now()
			speed <- speeds[schedule.ButtonState[now.Hour()][now.Weekday()]]
			time.Sleep(time.Duration(sleepDuration))
		}
	}()
}

func spawnCommand(speed int) (*exec.Cmd, context.CancelFunc, error) {
	ctx, kill := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "/usr/bin/python", "test.py", strconv.Itoa(speed))
	if err := cmd.Start(); err != nil {
		kill()
		return nil, nil, err
	}
	log.Printf("Spawned process %d", cmd.Process.Pid)
	return cmd, kill, nil
}

func subprocessWait(cmd *exec.Cmd, kill context.CancelFunc) {
	defer kill()
	if err := cmd.Wait(); err != nil {
		log.Printf("Subprocess %d finished: %v", cmd.Process.Pid, err)
	} else {
		log.Printf("Subprocess %d finished successfully", cmd.Process.Pid)
	}
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)

	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <path-to-scheduler-conf>\n", os.Args[0])
		os.Exit(1)
	}
	schedulerConf := os.Args[1]

	log.Print("Mr. Backup Agent starting")
	pidFile := fmt.Sprintf(pidPath, os.Getuid())
	err := managePidFile(pidFile)
	if err != nil {
		log.Fatal(err)
		return
	}
	defer os.Remove(pidFile)

	finish := make(chan bool)
	setupSignalHandler(finish)

	speed := make(chan int)
	setupTestSpeedGetter(schedulerConf, speed)

	oldspeed := 0
	var cmd *exec.Cmd
	var kill context.CancelFunc

	for {
		select {
		case newspeed := <-speed:
			speedChanged := newspeed != oldspeed
			cmdRunning := cmd != nil && cmd.ProcessState == nil
			cmdKilled := false

			if !cmdRunning || speedChanged {
				log.Printf("Speed received: %d", newspeed)
				oldspeed = newspeed
			}

			if cmdRunning && speedChanged {
				log.Printf("Killing subprocess")
				kill()
				cmdKilled = true
			}

			if newspeed != 0 && (!cmdRunning || cmdKilled) {
				cmd, kill, err = spawnCommand(newspeed)
				if err != nil {
					log.Print(err)
					continue
				}
				go subprocessWait(cmd, kill)
			}

		case <-finish:
			log.Printf("Finishing")
			if cmd != nil && cmd.ProcessState == nil {
				kill()
			}
			return
		}
	}
}
