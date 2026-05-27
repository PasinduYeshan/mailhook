// Package daemon manages the background lifecycle of the mailhook process:
// self-spawns `mailhook serve`, writes a pidfile, and offers stop/status/logs.

package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func stateDir() string {
	if v := os.Getenv("MAILHOOK_DIR"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "var", "mailhook")
}

func pidFile() string { return filepath.Join(stateDir(), "run", "mailhook.pid") }
func LogFile() string { return filepath.Join(stateDir(), "log", "mailhook.log") }

func readPid() (int, error) {
	b, err := os.ReadFile(pidFile())
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// reapOrphans kills any `mailhook serve` process that is not the current pid.
// Returns how many it signalled.
func reapOrphans() int {
	out, _ := exec.Command("pgrep", "-f", "mailhook serve").Output()
	n := 0
	self := os.Getpid()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil || pid == self {
			continue
		}
		if syscall.Kill(pid, syscall.SIGTERM) == nil {
			n++
		}
	}
	return n
}

func Start(args []string) error {
	if pid, err := readPid(); err == nil && pidAlive(pid) {
		fmt.Printf("mailhook already running (pid %d)\n", pid)
		return nil
	}
	if n := reapOrphans(); n > 0 {
		fmt.Printf("reaped %d orphan process(es)\n", n)
		time.Sleep(300 * time.Millisecond)
	}
	if err := os.MkdirAll(filepath.Dir(pidFile()), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(LogFile()), 0o755); err != nil {
		return err
	}

	lf, err := os.OpenFile(LogFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer lf.Close()

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, append([]string{"serve"}, args...)...)
	cmd.Stdout = lf
	cmd.Stderr = lf
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn failed: %w", err)
	}
	pid := cmd.Process.Pid
	if err := os.WriteFile(pidFile(), []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		return err
	}
	cmd.Process.Release()

	time.Sleep(400 * time.Millisecond)
	if !pidAlive(pid) {
		os.Remove(pidFile())
		return fmt.Errorf("daemon died on startup — check %s", LogFile())
	}

	fmt.Printf("mailhook started (pid %d)\n", pid)
	fmt.Println("  SMTP  127.0.0.1:1025")
	fmt.Println("  HOOK  127.0.0.1:8090  (any HTTP method)")
	fmt.Println("  API   127.0.0.1:8091")
	fmt.Println("  UI    http://127.0.0.1:8091/")
	fmt.Println("  log  ", LogFile())
	return nil
}

func Stop(args []string) error {
	stopped := 0
	if pid, err := readPid(); err == nil && pidAlive(pid) {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		fmt.Printf("mailhook stopped (pid %d)\n", pid)
		stopped++
	}
	os.Remove(pidFile())
	if n := reapOrphans(); n > 0 {
		fmt.Printf("killed %d orphan process(es)\n", n)
		stopped += n
	}
	if stopped == 0 {
		fmt.Println("mailhook not running")
	}
	return nil
}

func Restart(args []string) error {
	_ = Stop(nil)
	time.Sleep(400 * time.Millisecond)
	return Start(args)
}

func Status(args []string) error {
	pid, err := readPid()
	if err != nil || !pidAlive(pid) {
		out, _ := exec.Command("pgrep", "-f", "mailhook serve").Output()
		if strings.TrimSpace(string(out)) != "" {
			fmt.Printf("mailhook: orphan(s) present without pidfile (pids: %s)\n",
				strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", ", "))
			fmt.Println("  run `mailhook stop` to clean up, then `mailhook start`")
			return nil
		}
		fmt.Println("mailhook: stopped")
		return nil
	}
	fmt.Printf("mailhook: running (pid %d)\n", pid)
	if out, err := exec.Command("ps", "-o", "rss=,%cpu=", "-p", strconv.Itoa(pid)).Output(); err == nil {
		fields := strings.Fields(strings.TrimSpace(string(out)))
		if len(fields) >= 2 {
			rss, _ := strconv.ParseFloat(fields[0], 64)
			fmt.Printf("  RSS %.1f MB · CPU %s%%\n", rss/1024, fields[1])
		}
	}
	fmt.Println("  log ", LogFile())
	return nil
}

func Logs(args []string) error {
	f := LogFile()
	if _, err := os.Stat(f); err != nil {
		return fmt.Errorf("no log file at %s", f)
	}
	path, err := exec.LookPath("tail")
	if err != nil {
		fmt.Println(f)
		return nil
	}
	if err := syscall.Exec(path, []string{"tail", "-f", f}, os.Environ()); err != nil {
		return errors.New("exec tail: " + err.Error())
	}
	return nil
}
