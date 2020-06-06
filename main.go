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
	schedulerConf = "scheduler.conf"
	sleepDuration = 5 * 1000000000 // 5 seconds
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

func setupSpeedGetter(speed chan int) {
	go func() {
		for {
			conf, err := ioutil.ReadFile(schedulerConf)
			if err != nil {
				log.Fatal(err)
			}

			var schedule schedule
			err = json.Unmarshal(conf[34:], &schedule)
			if err != nil {
				log.Fatal(err)
			}

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
		log.Printf("Subprocess finished with error: %v", err)
	} else {
		log.Printf("Subprocess finished successfully")
	}
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)

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
	setupSpeedGetter(speed)

	oldspeed := 0
	var cmd *exec.Cmd
	var kill context.CancelFunc

	for {
		select {
		case newspeed := <-speed:
			speedChanged := newspeed != oldspeed
			cmdRunning := cmd != nil && cmd.ProcessState == nil

			if !cmdRunning || speedChanged {
				log.Printf("Speed received: %d", newspeed)
				oldspeed = newspeed
			}

			if cmdRunning && speedChanged {
				log.Printf("Killing subprocess")
				kill()
			}

			if newspeed == 0 {
				continue
			}

			if !cmdRunning {
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
