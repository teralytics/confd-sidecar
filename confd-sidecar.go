package main

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func execCommand(stderrpipe bool, program string, args ...string) (*exec.Cmd, io.ReadCloser) {
	cmd := exec.Command(program, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	var thestderrpipe io.ReadCloser
	if stderrpipe {
		thestderrpipe, _ = cmd.StderrPipe()
	} else {
		cmd.Stderr = os.Stderr
	}
	err := cmd.Start()
	if err != nil {
		panic(err)
	}
	return cmd, thestderrpipe
}

func logf(fmtstr string, args ...interface{}) {
	log.Printf(fmt.Sprintf("supervisor[%d]: ", os.Getpid())+fmtstr, args...)
}

func logExit(progname string, err error) int {
	// logExit returns the status code of the exiting process.
	// If the process was killed, it returns 254.
	// If the process did not exit and was not killed, it returns 253.
	if err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			if status, ok := exiterr.Sys().(syscall.WaitStatus); ok && status.ExitStatus() > 0 {
				logf("%s ended with status %d", progname, status.ExitStatus())
				return status.ExitStatus()
			} else {
				logf("%s confd received %s", progname, exiterr)
				return 254
			}
		} else {
			logf("%s ended with status %s", progname, err)
			return 253
		}
	} else {
		logf("%s ended normally", "main program")
	}
	return 0
}

type confdConfiguration struct {
	confdir    string
	backend    string
	node       string
	binary     string
	confdfiles map[string]string
	templates  map[string]string
}

func getConfdConfiguration() confdConfiguration {
	confdir := os.Getenv("CONFD_CONFDIR")
	if confdir == "" {
		confdir = "/etc/confd"
	}
	backend := os.Getenv("CONFD_BACKEND")
	if backend == "" {
		backend = "consul"
	}
	node := os.Getenv("CONFD_NODE")
	binary := os.Getenv("CONFD_PATH")
	if binary == "" {
		binary = "confd"
	}
	c := confdConfiguration{confdir, backend, node, binary, make(map[string]string), make(map[string]string)}
	for _, e := range os.Environ() {
		pair := strings.SplitN(e, "=", 2)
		if strings.HasPrefix(pair[0], "CONFD_CONFDFILE_") || pair[0] == "CONFD_CONFDFILE" {
			data := strings.SplitN(pair[1], "\n", 2)
			if len(data) == 1 {
				data = append(data, "")
			}
			c.confdfiles[data[0]] = data[1]
		}
		if strings.HasPrefix(pair[0], "CONFD_TEMPLATE_") || pair[0] == "CONFD_TEMPLATE" {
			data := strings.SplitN(pair[1], "\n", 2)
			if len(data) == 1 {
				data = append(data, "")
			}
			c.templates[data[0]] = data[1]
		}
	}
	return c
}

func (c confdConfiguration) args() []string {
	parm := []string{fmt.Sprintf("-confdir=%s", c.confdir), fmt.Sprintf("-backend=%s", c.backend), "-watch"}
	if c.node != "" {
		parm = append(parm, fmt.Sprintf("-node=%s", c.node))
	}
	return parm
}

func createFile(fn string, content string, folder string) error {
	if err := os.MkdirAll(folder, os.ModePerm); err != nil {
		logf("fatal error creating requested %s directory prior to writing file %s: %s", folder, fn, err)
		return err
	}
	file := filepath.Join(folder, fn)
	if err := ioutil.WriteFile(file, []byte(content), os.FileMode(0666)); err != nil {
		logf("fatal error creating requested file %s: %s", file, err)
		return err
	}
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("This program runs a foreground service and a confd instance in parallel, sending a")
		fmt.Println("SIGHUP to the service program when confd reconfigures any of the configuration")
		fmt.Println("files it manages.  Additionally, it can automatically provision confd configuration")
		fmt.Println("files and templates upon startup, based on environment variables.")
		fmt.Println("")
		fmt.Printf("Usage of %s:\n", os.Args[0])
		fmt.Println("")
		fmt.Printf("    %s <main program> [main service program arguments...]\n", os.Args[0])
		fmt.Println("")
		fmt.Println("confd pre-startup configuration via environment variables:")
		fmt.Println("")
		fmt.Println("    CONFD_PATH=<path to the confd binary, defaults to search in the PATH>")
		fmt.Println("    CONFD_CONFDIR=<path to confd configuration directory, default /etc/confd>")
		fmt.Println("    CONFD_BACKEND=<name of data backend, default consul>")
		fmt.Println("    CONFD_NODE=<option passed as the confd -node parameter, defaults to empty>")
		fmt.Println("    CONFD_CONFDFILE_<x>=<name and content of config file to create in $CONFD_CONFDIR/conf.d/>")
		fmt.Println("    CONFD_TEMPLATE_<x>=<name and content of template file to create in $CONFD_CONFDIR/templates/>")
		fmt.Println("")
		fmt.Println("Each CONFD_CONFDFILE_<x> and CONFD_TEMPLATE_<x> variable must contain a string")
		fmt.Println("where the first line is the name of the file in the respective directory,")
		fmt.Println("and the remainder is what's meant to be the content of the file.")
		fmt.Println("")
		fmt.Println("If any CONFD_CONFDFILE_<x> or CONFD_TEMPLATE_<x> variable is specified, then")
		fmt.Printf("%s will auto-create the CONFD_CONFDIR directory upon start, and populate it\n", os.Args[0])
		fmt.Println("upon startup, prior to starting confd.")
		fmt.Println("")
		fmt.Println("This progam will wait one second between the time confd rewrites configuration files")
		fmt.Println("and sending a SIGHUP to the service program.  If several configuration files are")
		fmt.Println("modified by confd during that second, the SIGHUP will only be sent after a second")
		fmt.Println("of the last configuration change.")
		os.Exit(64)
	}

	logf("main program and arguments to be run: %v", os.Args[1:])

	signalReceived := make(chan os.Signal, 1)
	signal.Notify(signalReceived, syscall.SIGTERM)

	confdConfig := getConfdConfiguration()
	for fn, content := range confdConfig.confdfiles {
		logf("creating / updating confd conf.d file %s", fn)
		if err := createFile(fn, content, filepath.Join(confdConfig.confdir, "conf.d")); err != nil {
			os.Exit(32)
		}
	}
	for fn, content := range confdConfig.templates {
		logf("creating / updating confd template file %s", fn)
		if err := createFile(fn, content, filepath.Join(confdConfig.confdir, "templates")); err != nil {
			os.Exit(32)
		}
	}

	confdCmd, confdStderr := execCommand(true, confdConfig.binary, confdConfig.args()...)
	logf("confd started — waiting one second to start main program")
	confdEnded := make(chan error)
	configChanged := make(chan time.Time, 1000)
	go func() {
		confdScanner := bufio.NewScanner(confdStderr)
		for confdScanner.Scan() {
			text := confdScanner.Text()
			fmt.Fprintln(os.Stderr, text)
			if strings.Contains(text, " has been updated") {
				configChanged <- time.Now()
			}
		}
		err := confdCmd.Wait()
		confdEnded <- err
	}()

	confdSettleTimeout := time.After(1 * time.Second)
settled:
	for {
		select {
		case <-confdSettleTimeout:
			logf("starting main program now")
			break settled
		case <-configChanged:
			logf("confd has updated a config file — waiting one second to start main program")
			confdSettleTimeout = time.After(1 * time.Second)
		}
	}

	mainProgramCmd, _ := execCommand(false, os.Args[1], os.Args[2:]...)
	mainProgramStartTime := time.Now()
	mainProgramEnded := make(chan error)
	go func() {
		err := mainProgramCmd.Wait()
		mainProgramEnded <- err
	}()

	var confdSignaled bool
	var mainProgramSignaled bool
	var confdExitStatus int
	var mainProgramExitStatus int
	confdSettleTimeout = nil
	for {
		select {
		case configChangedTime := <-configChanged:
			if configChangedTime.After(mainProgramStartTime) {
				logf("confd has updated a config file — waiting one second to reload main program")
				confdSettleTimeout = time.After(1 * time.Second)
			}
		case <-confdSettleTimeout:
			logf("reloading main program now")
			confdSettleTimeout = nil
			mainProgramCmd.Process.Signal(syscall.SIGHUP)
		case <-signalReceived:
			logf("received SIGTERM, signaling confd and main program unconditionally")
			confdCmd.Process.Signal(syscall.SIGTERM)
			confdSignaled = true
			mainProgramCmd.Process.Signal(syscall.SIGTERM)
			mainProgramSignaled = true
		case err := <-confdEnded:
			confdExitStatus = logExit("confd", err)
			confdSignaled = true
			confdEnded = nil
			if !mainProgramSignaled {
				logf("signaling main program")
				mainProgramCmd.Process.Signal(syscall.SIGTERM)
			}
		case err := <-mainProgramEnded:
			mainProgramExitStatus = logExit("main program", err)
			mainProgramSignaled = true
			mainProgramEnded = nil
			if !confdSignaled {
				logf("signaling confd")
				confdCmd.Process.Signal(syscall.SIGTERM)
			}
		}
		if confdEnded == nil && mainProgramEnded == nil {
			logf("confd and main program ended — exiting now")
			break
		}
	}
	if mainProgramExitStatus != 0 {
		os.Exit(mainProgramExitStatus)
	}
	if confdExitStatus != 0 {
		os.Exit(confdExitStatus)
	}
}
