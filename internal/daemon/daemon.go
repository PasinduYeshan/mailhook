// Package daemon manages the background lifecycle of the mailhook process:
// self-spawns `mailhook serve`, writes a pidfile, and offers stop/status/logs.

package daemon

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/PasinduYeshan/mailhook/internal/server"
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

// Runtime is the bound config of the running instance, written by `serve`.
type Runtime struct {
	PID  int    `json:"pid"`
	SMTP string `json:"smtp"`
	Hook string `json:"hook"`
	API  string `json:"api"`
	Cap  int    `json:"cap"`
}

func runtimeFile() string { return filepath.Join(stateDir(), "run", "mailhook.json") }

// WriteRuntime records the addresses this process is serving on.
func WriteRuntime(cfg server.Config) error {
	if err := os.MkdirAll(filepath.Dir(runtimeFile()), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(Runtime{os.Getpid(), cfg.SMTPAddr, cfg.HookAddr, cfg.APIAddr, cfg.Cap})
	return os.WriteFile(runtimeFile(), b, 0o644)
}

// RemoveRuntime deletes the runtime file if this process wrote it.
func RemoveRuntime() {
	if rt, err := readRuntimeFile(); err == nil && rt.PID == os.Getpid() {
		os.Remove(runtimeFile())
	}
}

func readRuntimeFile() (Runtime, error) {
	var rt Runtime
	b, err := os.ReadFile(runtimeFile())
	if err != nil {
		return rt, err
	}
	return rt, json.Unmarshal(b, &rt)
}

// ReadRuntime returns the live instance's config, if one is running.
func ReadRuntime() (Runtime, bool) {
	rt, err := readRuntimeFile()
	return rt, err == nil && pidAlive(rt.PID)
}

// Args turns the runtime back into serve flags.
func (rt Runtime) Args() []string {
	return []string{"--smtp", rt.SMTP, "--hook", rt.Hook, "--api", rt.API, "--cap", strconv.Itoa(rt.Cap)}
}

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

// isServe reports whether pid is a live `mailhook serve` process, guarding
// against a stale pidfile whose pid was reused by something else.
func isServe(pid int) bool {
	if !pidAlive(pid) {
		return false
	}
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	f := strings.Fields(string(out))
	return len(f) > 1 && f[1] == "serve" && isMailhookBin(f[0])
}

// isMailhookBin accepts renamed builds (mailhook-dev) and this binary's own name.
func isMailhookBin(path string) bool {
	base := filepath.Base(path)
	if strings.Contains(base, "mailhook") {
		return true
	}
	self, err := os.Executable()
	return err == nil && base == filepath.Base(self)
}

// ownPid returns the instance this state dir started, or 0.
func ownPid() int {
	if pid, err := readPid(); err == nil && isServe(pid) {
		return pid
	}
	if rt, err := readRuntimeFile(); err == nil && isServe(rt.PID) {
		return rt.PID
	}
	return 0
}

// otherServes lists `mailhook serve` processes not owned by this state dir.
func otherServes() []int {
	out, _ := exec.Command("pgrep", "-f", " serve").Output()
	own, self := ownPid(), os.Getpid()
	var pids []int
	for _, line := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(line)
		if err == nil && pid != own && pid != self && isServe(pid) {
			pids = append(pids, pid)
		}
	}
	return pids
}

func joinPids(pids []int) string {
	s := make([]string, len(pids))
	for i, p := range pids {
		s[i] = strconv.Itoa(p)
	}
	return strings.Join(s, ", ")
}

// terminate sends SIGTERM, waits for exit, and falls back to SIGKILL.
func terminate(pid int) {
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if !pidAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	// SIGKILL skips serve's own cleanup.
	if rt, err := readRuntimeFile(); err == nil && rt.PID == pid {
		os.Remove(runtimeFile())
	}
}

func Start(args []string) error {
	if pid := ownPid(); pid != 0 {
		fmt.Printf("mailhook already running (pid %d)\n", pid)
		if len(args) > 0 {
			fmt.Println("  use `mailhook restart [flags]` to change ports")
		}
		return nil
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
	logStart, _ := lf.Seek(0, io.SeekEnd)

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
	// Reap via Wait: a dead child stays a zombie and still passes pidAlive.
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()

	// Wait until serve reports its bound ports, or dies trying.
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	timeout := time.After(3 * time.Second)
wait:
	for {
		select {
		case <-exited:
			os.Remove(pidFile())
			msg := logSince(logStart)
			if others := otherServes(); len(others) > 0 {
				msg += fmt.Sprintf("\nother mailhook serve process(es) running: %s (`mailhook stop --all` stops them)", joinPids(others))
			}
			return fmt.Errorf("daemon died on startup:\n%s", msg)
		case <-timeout:
			break wait
		case <-tick.C:
			if rt, ok := ReadRuntime(); ok && rt.PID == pid {
				break wait
			}
		}
	}

	fmt.Printf("mailhook started (pid %d)\n", pid)
	printPorts()
	fmt.Println("  log  ", LogFile())
	return nil
}

// logSince returns the log written after offset, trimmed.
func logSince(offset int64) string {
	f, err := os.Open(LogFile())
	if err != nil {
		return "  (no log)"
	}
	defer f.Close()
	f.Seek(offset, io.SeekStart)
	b, _ := io.ReadAll(f)
	return strings.TrimSpace(string(b))
}

func printPorts() {
	rt, ok := ReadRuntime()
	if !ok {
		rt = Runtime{SMTP: server.DefaultSMTPAddr, Hook: server.DefaultHookAddr, API: server.DefaultAPIAddr}
	}
	fmt.Println("  SMTP ", rt.SMTP)
	fmt.Println("  HOOK ", rt.Hook, " (any HTTP method)")
	fmt.Println("  API  ", rt.API)
	fmt.Println("  UI    http://" + rt.API + "/")
}

// Stop stops only the instance this state dir started; --all also stops
// every other `mailhook serve` process.
func Stop(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	all := fs.Bool("all", false, "also stop other mailhook serve processes")
	fs.Parse(args)

	others := otherServes()
	stopped := false
	if pid := ownPid(); pid != 0 {
		terminate(pid)
		fmt.Printf("mailhook stopped (pid %d)\n", pid)
		stopped = true
	}
	os.Remove(pidFile())

	switch {
	case *all:
		for _, pid := range others {
			terminate(pid)
			fmt.Printf("stopped other instance (pid %d)\n", pid)
		}
	case len(others) > 0:
		fmt.Printf("left %d other mailhook serve process(es) running (pids: %s); use `mailhook stop --all` to stop them\n",
			len(others), joinPids(others))
	}
	if !stopped && (len(others) == 0 || !*all) {
		fmt.Println("mailhook not running")
	}
	return nil
}

// Restart keeps the running instance's ports unless flags override them.
func Restart(args []string) error {
	if rt, ok := ReadRuntime(); ok {
		args = append(rt.Args(), args...)
	}
	_ = Stop(nil)
	return Start(args)
}

func Status(args []string) error {
	if others := otherServes(); len(others) > 0 {
		defer fmt.Printf("other mailhook serve process(es): %s (`mailhook stop --all` stops them)\n", joinPids(others))
	}
	pid := ownPid()
	if pid == 0 {
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
	printPorts()
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
