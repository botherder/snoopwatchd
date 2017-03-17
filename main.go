package main

import (
	"os"
	"log"
	"fmt"
	"time"
	"regexp"
	"strings"
	"io/ioutil"
	"log/syslog"
	"path/filepath"
	"github.com/botherder/fsnotify"
	psprocess "github.com/shirou/gopsutil/process"
	psfiles "github.com/botherder/gopsutil/files"
)

func get_cams() ([]string) {
	// Get list of files in /dev/.
	var dev string = "/dev/"
	files, err := ioutil.ReadDir(dev)
	if err != nil {
		log.Fatal(err)
	}

	devices := []string{}
	for _, f := range files {
		// Check if a file starts with "video", which should be at webcam.
		if !f.IsDir() && strings.HasPrefix(f.Name(), "video") {
			device := filepath.Join(dev, f.Name())
			devices = append(devices, device)
		}
	}

	return devices
}

func get_mics() ([]string) {
	// Read file containing details on audio devices.
	devices_content, _ := ioutil.ReadFile("/proc/asound/devices")

	// Look for those marked as recording devices.
	re := regexp.MustCompile(`(?m)^.*\[(.*?)\].*capture`)
	matches := re.FindAllStringSubmatch(string(devices_content), -1)

	devices := []string{}
	for _, match := range matches {
		// For each entry we identify the appropriate device.
		identifiers := strings.Split(match[1], "-")
		card_id := strings.TrimSpace(identifiers[0])
		device_id := strings.TrimSpace(identifiers[1])
		device_path := fmt.Sprintf("/dev/snd/pcmC%sD%sc", card_id, device_id)
		
		// Check if device name exists, if so add it to the list.
		if _, err := os.Stat(device_path); err == nil {
			devices = append(devices, device_path)
		}
	}

	return devices
}

func watch_for_events(device string) {
	// Initialize syslog logger for this watcher.
	slog, err := syslog.New(syslog.LOG_ERR, "snoopwatchd")

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	// Add device to the watcher.
	err = watcher.Add(device)
	if err != nil {
		log.Println(err)
	}

	// Watch for events.
	for {
		select {
		case event := <-watcher.Events:
			// If it's an IN_OPEN event the current device as accessed.
			if event.Op&fsnotify.Open == fsnotify.Open {
				msg := fmt.Sprintf("Some process accessed device %s", event.Name)

				// Try to find processes that have an open handle to the device.
				// WARNING: This isn't actually directly connected to this
				// particular event. We assume there might be only one process
				// with access to the device at any given time.
				current_pids, err := psfiles.FindProcsByFile(event.Name)
				if err == nil {
					for _, pid := range current_pids {
						// Get details on the process.
						proc, _ := psprocess.NewProcess(int32(pid))
						proc_exe, _ := proc.Exe()
						proc_cmd, _ := proc.Cmdline()
						msg = fmt.Sprintf("%s (pid=%d, exe=%s, cmd=%s)",
							msg, pid, proc_exe, proc_cmd)
					}
				}

				// Log to syslog and to console.
				slog.Alert(msg)
				log.Println(msg)
			// If it's an IN_DELETE file, we assume the device was disconnected.
			} else if event.Op&fsnotify.Remove == fsnotify.Remove {
				return
			}
		case err := <-watcher.Errors:
			log.Println("error:", err)
		}
	}
}

// Spaghetti code to remove a device from a list.
func remove_from_slice(things []string, item string) []string {
	var to_remove int
	for index, thing := range things {
		if item == thing {
			to_remove = int(index)
		}
	}

	things = append(things[:int(to_remove)], things[int(to_remove)+1:]...)
	return things
}

func main() {
	// Initialize syslog logger for the core procedure.
	slog, err := syslog.New(syslog.LOG_ERR, "snoopwatchd")
	if err != nil {
		log.Fatal(err)
	}
	defer slog.Close()

	monitored := []string{}
	for {
		// Fetch currently available webcams and microphones.
		devices := append(get_cams(), get_mics()...)
		for _, device := range devices {
			var found bool = false
			// Loop through current devices and check if they have already
			// been marked for monitoring.
			for _, monitor := range monitored {
				if device == monitor {
					found = true
					break
				}
			}

			// If the device does not appear in the monitored list, add it
			// and start the related watcher.
			if found == false {
				msg := fmt.Sprintf("New device was connected %s", device)
				slog.Info(msg)
				log.Println(msg)

				go watch_for_events(device)
				monitored = append(monitored, device)
			}
		}

		// Now we check if any device was removed.
		new_monitored := monitored
		for _, monitor := range monitored {
			var found bool = false
			for _, device := range devices {
				if monitor == device {
					found = true
					break
				}
			}

			// If the device in the monitored list isn't currently available
			// we remove it from the list.
			if found == false {
				msg := fmt.Sprintf("Device was disconnected %s", monitor)
				slog.Alert(msg)
				log.Println(msg)

				new_monitored = remove_from_slice(new_monitored, monitor)
			}
		}
		monitored = new_monitored

		time.Sleep(100 * time.Millisecond)
	}
}
