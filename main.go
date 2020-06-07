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

type config struct {
	BackupCmd     string
	BackupDest    string
	SpeedArray    []int
	Schedule      [][]int
	SleepDuration int
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

func setupTestSpeedGetter(config config, speed chan int) {
	sleepDuration := 5 * 1000000000
	go func() {
		hour := 0
		weekday := 0
		for {
			fmt.Println()
			log.Printf("Weekday: %d, Hour: %d", weekday, hour)

			speed <- config.SpeedArray[config.Schedule[hour][weekday]]
			time.Sleep(time.Duration(sleepDuration))

			hour++
			if hour >= 24 {
				weekday = (weekday + 1) % 7
				hour = 0
			}
		}
	}()
}

func setupSpeedGetter(configPath string, speed chan int) {
	go func() {
		for {
			config := parseConf(configPath)
			now := time.Now()
			// Damn sunday == 0 nonsense
			weekday := pmod(int(now.Weekday())-1, 7)
			s := config.SpeedArray[config.Schedule[now.Hour()][weekday]]
			speed <- s
			time.Sleep(time.Duration(config.SleepDuration * 1000000000))
		}
	}()
}

func spawnCommand(config config, speed int) (*exec.Cmd, context.CancelFunc, error) {
	ctx, kill := context.WithCancel(context.Background())
	cmdStr := fmt.Sprintf(config.BackupCmd, speed)
	cmdStr += fmt.Sprintf(" %s", config.BackupDest)

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", cmdStr)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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

// Need this because the CancelFunc from the context will just kill the child
// process and not any of the processes spawn by it:
// https://stackoverflow.com/questions/22470193/why-wont-go-kill-a-child-process-correctly
func killProcessGroup(cmd *exec.Cmd) {
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil {
		syscall.Kill(-pgid, 15) // note the minus sign
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
	setupSpeedGetter(configPath, speed)

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
				killProcessGroup(cmd)
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
				killProcessGroup(cmd)
			}
			return
		}
	}
}
