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
	"syscall"
	"time"
)

type schedule struct {
	ButtonState [][]int `json:"button_state"`
}

type config struct {
	BackupCmd       string
	BackupDest      string
	SpeedArray      [3]int
	SchedulerConfig string
	SleepDuration   int
}

const (
	configPath = "/etc/mr-backup-agent.conf"
	pidPath    = "/var/run/mr-backup-agent/mr-backup-agent.pid"
)

// Positive modulo, returns non negative solution to x % d
func pmod(x, d int) int {
	x = x % d
	if x >= 0 {
		return x
	}
	if d < 0 {
		return x - d
	}
	return x + d
}

func managePidFile(pidPath string) error {
	_, err := os.Stat(pidPath)
	if err == nil {
		fmt.Printf("Pid file %s exists. Exiting\n", pidPath)
		os.Exit(0)
	}

	file, err := os.Create(pidPath)
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

func parseConf(path string) config {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}

	var config config
	if err = json.Unmarshal(content, &config); err != nil {
		log.Fatal(err)
	}

	return config
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

func setupTestSpeedGetter(path string, config config, speed chan int) {
	sleepDuration := 5 * 1000000000
	go func() {
		hour := 0
		weekday := 0
		for {
			fmt.Println()
			log.Printf("Weekday: %d, Hour: %d", weekday, hour)

			schedule := parseSchedulerConf(path)
			speed <- config.SpeedArray[schedule.ButtonState[hour][weekday]]
			time.Sleep(time.Duration(sleepDuration))

			hour++
			if hour >= 24 {
				weekday = (weekday + 1) % 7
				hour = 0
			}
		}
	}()
}

func setupSpeedGetter(path string, config config, speed chan int) {
	go func() {
		for {
			schedule := parseSchedulerConf(path)
			now := time.Now()
			// Damn sunday == 0 nonsense
			weekday := pmod(int(now.Weekday())-1, 7)
			speed <- config.SpeedArray[schedule.ButtonState[now.Hour()][weekday]]
			time.Sleep(time.Duration(config.SleepDuration * 1000000000))
		}
	}()
}

func spawnCommand(config config, speed int) (*exec.Cmd, context.CancelFunc, error) {
	ctx, kill := context.WithCancel(context.Background())
	cmdStr := fmt.Sprintf(config.BackupCmd, speed)
	cmdStr += fmt.Sprintf(" %s", config.BackupDest)

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", cmdStr)
	log.Printf("Running command: %s %s %s", "/bin/sh", "-c", cmdStr)
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
	config := parseConf(configPath)

	log.Print("Mr. Backup Agent starting")
	err := managePidFile(pidPath)
	if err != nil {
		log.Fatal(err)
		return
	}
	defer os.Remove(pidPath)

	finish := make(chan bool)
	setupSignalHandler(finish)

	speed := make(chan int)
	setupSpeedGetter(config.SchedulerConfig, config, speed)

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
				cmdspeed := newspeed
				if cmdspeed == -1 {
					cmdspeed = 0
				}
				cmd, kill, err = spawnCommand(config, cmdspeed)
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
