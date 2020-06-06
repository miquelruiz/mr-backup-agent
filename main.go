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
)

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

func setupSpeedGetter(c chan int) {
	go func() {
		for {
			speeds := []int{10, 20, 30, 40, 50, 60, 70}
			c <- speeds[time.Now().Second()/10]
			time.Sleep(time.Duration(2000000000))
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
	conf, err := ioutil.ReadFile(schedulerConf)
	if err != nil {
		log.Fatal(err)
	}

	var c interface{}
	err = json.Unmarshal(conf[34:], &c)
	if err != nil {
		log.Fatal(err)
	}

	m := c.(map[string]interface{})
	fmt.Print(m["button_state"])

	return

	log.Print("Oh, hai!")
	pidFile := fmt.Sprintf(pidPath, os.Getuid())
	err = managePidFile(pidFile)
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
			if newspeed == oldspeed {
				continue
			}
			oldspeed = newspeed

			log.Printf("Speed received: %d", newspeed)
			if cmd != nil {
				log.Printf("Killing old process")
				kill()
			}

			cmd, kill, err = spawnCommand(newspeed)
			if err != nil {
				log.Print(err)
				kill()
				continue
			}

			go subprocessWait(cmd, kill)
		case <-finish:
			if kill != nil {
				kill()
			}
			return
		}
	}
}
