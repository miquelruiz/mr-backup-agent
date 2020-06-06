package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"time"
)

const (
	pidPath = "/var/run/%d/mr-backup-agent.pid"
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

func getSpeed() int {
	speeds := []int{10, 20, 30, 40, 50, 60, 70}
	fmt.Println(time.Now().Second())
	return speeds[time.Now().Second()/10]
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

func runBackup(speed chan int, done chan bool, errc chan error) {
	var cmd *exec.Cmd
	var kill context.CancelFunc
	var err error

	cmdDone := make(chan bool)
	cmdError := make(chan error)
	killed := false

	for {
		select {
		case sp := <-speed:
			log.Printf("Speed received: %d", sp)
			if cmd != nil {
				log.Printf("Killing old process")
				killed = true
				kill()
			}

			// This is the signal to stop
			if sp == 0 {
				done <- true
				return
			}

			cmd, kill, err = spawnCommand(sp)
			if err != nil {
				errc <- err
				kill()
				return
			}

			go func() {
				err := cmd.Wait()
				kill()
				if err != nil {
					cmdError <- err
					return
				}
				cmdDone <- true
			}()

		case <-cmdDone:
			if !killed {
				done <- true
				return
			}
			killed = false

		case err := <-cmdError:
			if !killed {
				errc <- err
				return
			}
			killed = false
		}
	}
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)

	log.Print("Oh, hai!")
	pidFile := fmt.Sprintf(pidPath, os.Getuid())
	err := managePidFile(pidFile)
	if err != nil {
		log.Fatal(err)
		return
	}
	defer os.Remove(pidFile)

	oldspeed := 0
	speed := make(chan int)
	done := make(chan bool)
	errc := make(chan error)
	go runBackup(speed, done, errc)

LOOP:
	for {
		select {

		// read error channel and crash
		case err = <-errc:
			os.Remove(pidFile)
			log.Fatal(err)

		// read termination channel and break
		case <-done:
			log.Printf("We're done")
			break LOOP

		// read config file and check with Now
		// send new limit if changed
		default:
			sp := getSpeed()
			if sp == oldspeed {
				time.Sleep(time.Duration(2000000000))
				continue
			}

			oldspeed = sp
			speed <- sp

			time.Sleep(time.Duration(2000000000))
		}
	}
}
