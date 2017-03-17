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
    var dev string = "/dev/"

    files, err := ioutil.ReadDir(dev)
    if err != nil {
        log.Fatal(err)
    }

    devices := []string{}
    for _, f := range files {
        if !f.IsDir() && strings.HasPrefix(f.Name(), "video") {
            device := filepath.Join(dev, f.Name())
            devices = append(devices, device)
        }
    }

    return devices
}

func get_mics() ([]string) {
    devices_content, _ := ioutil.ReadFile("/proc/asound/devices")

    re := regexp.MustCompile(`(?m)^.*\[(.*?)\].*capture`)
    matches := re.FindAllStringSubmatch(string(devices_content), -1)

    devices := []string{}
    for _, match := range matches {
        identifiers := strings.Split(match[1], "-")
        card_id := strings.TrimSpace(identifiers[0])
        device_id := strings.TrimSpace(identifiers[1])
        device_path := fmt.Sprintf("/dev/snd/pcmC%sD%sc", card_id, device_id)
        
        if _, err := os.Stat(device_path); err == nil {
            devices = append(devices, device_path)
        }
    }

    return devices
}

func watch_for_events(device string) {
    slog, err := syslog.New(syslog.LOG_ERR, "snoopwatchd")

    watcher, err := fsnotify.NewWatcher()
    if err != nil {
        log.Fatal(err)
    }
    defer watcher.Close()

    err = watcher.Add(device)
    if err != nil {
        log.Println(err)
    }

    for {
        select {
        case event := <-watcher.Events:
            if event.Op&fsnotify.Open == fsnotify.Open {
                msg := fmt.Sprintf("Some process accessed device %s", event.Name)

                current_pids, err := psfiles.FindProcsByFile(event.Name)
                if err == nil {
                    for _, pid := range current_pids {
                        proc, _ := psprocess.NewProcess(int32(pid))
                        proc_exe, _ := proc.Exe()
                        proc_cmd, _ := proc.Cmdline()
                        msg = fmt.Sprintf("%s (pid=%d, exe=%s, cmd=%s)",
                            msg, pid, proc_exe, proc_cmd)
                    }
                }

                slog.Alert(msg)
                log.Println(msg)
            } else if event.Op&fsnotify.Remove == fsnotify.Remove {
                return
            }
        case err := <-watcher.Errors:
            log.Println("error:", err)
        }
    }
}

func main() {
    slog, err := syslog.New(syslog.LOG_ERR, "snoopwatchd")
    if err != nil {
        log.Fatal(err)
    }
    defer slog.Close()

    monitored := []string{}
    for {
        devices := append(get_cams(), get_mics()...)
        for _, device := range devices {
            var found bool = false
            for _, monitor := range monitored {
                if device == monitor {
                    found = true
                    break
                }
            }

            if found == false {
                msg := fmt.Sprintf("New device was connected %s", device)
                slog.Info(msg)
                log.Println(msg)

                time.Sleep(10 * time.Millisecond)
                go watch_for_events(device)
                monitored = append(monitored, device)
            }
        }

        for index, monitor := range monitored {
            var found bool = false
            for _, device := range devices {
                if monitor == device {
                    found = true
                    break
                }
            }

            if found == false {
                msg := fmt.Sprintf("Device was disconnected %s", monitor)
                slog.Alert(msg)
                log.Println(msg)

                monitored = append(monitored[:int(index)], monitored[int(index)+1:]...)
            }
        }

        time.Sleep(100 * time.Millisecond)
    }
}
