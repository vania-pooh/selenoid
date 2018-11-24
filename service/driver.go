package service

import (
	"fmt"
	"github.com/kballard/go-shellquote"
	"log"
	"net"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"errors"
	"github.com/aerokube/selenoid/session"
	"github.com/aerokube/util"
	"os"
	"path/filepath"
)

// Driver - driver processes manager
type Driver struct {
	ServiceBase
	Environment
	session.Caps
}

// StartWithCancel - Starter interface implementation
func (d *Driver) StartWithCancel() (*StartedService, error) {
	requestId := d.RequestId
	slice, ok := d.Service.Image.([]interface{})
	if !ok {
		return nil, fmt.Errorf("configuration error: image is not an array: %v", d.Service.Image)
	}
	var cmdLine []string
	for _, c := range slice {
		if _, ok := c.(string); !ok {
			return nil, fmt.Errorf("configuration error: value is not a string: %v", c)
		}
		cmdLine = append(cmdLine, c.(string))
	}
	if len(cmdLine) == 0 {
		return nil, errors.New("configuration error: image is empty")
	}
	log.Printf("[%d] [ALLOCATING_PORT]", requestId)
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, fmt.Errorf("cannot bind to port: %v", err)
	}
	u := &url.URL{Scheme: "http", Host: l.Addr().String(), Path: d.Service.Path}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	log.Printf("[%d] [ALLOCATED_PORT] [%s]", requestId, port)
	cmdLine = append(cmdLine, fmt.Sprintf("--port=%s", port))
	cmd := exec.Command(cmdLine[0], cmdLine[1:]...)
	cmd.Env = append(cmd.Env, d.Service.Env...)
	cmd.Env = append(cmd.Env, d.Caps.Env...)
	if d.CaptureDriverLogs {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else if d.LogOutputDir != "" && (d.SaveAllLogs || d.Log) {
		filename := filepath.Join(d.LogOutputDir, d.LogName)
		f, err := os.Create(filename)
		if err != nil {
			return nil, fmt.Errorf("failed to create log file %s: %v", d.LogName, err)
		}
		cmd.Stdout = f
		cmd.Stderr = f
	}
	l.Close()
	log.Printf("[%d] [STARTING_PROCESS] [%s]", requestId, cmdLine)
	s := time.Now()
	err = cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("cannot start process %v: %v", cmdLine, err)
	}
	err = wait(u.String(), d.StartupTimeout)
	if err != nil {
		d.stopProcess(cmd)
		return nil, err
	}
	log.Printf("[%d] [PROCESS_STARTED] [%d] [%.2fs]", requestId, cmd.Process.Pid, util.SecondsSince(s))

	var videoCmd *exec.Cmd
	if d.Video {
		videoOutputPath := filepath.Join(d.VideoOutputDir, d.VideoName)
		videoCmdLine, err := shellquote.Split(strings.Replace(d.VideoContainerImage, "$fileName", videoOutputPath, -1))
		if err != nil {
			return nil, fmt.Errorf("malformed video recorder command: %v", err)
		}
		log.Printf("[%d] [STARTING_PROCESS] [%s]", requestId, videoCmdLine)
		videoCmd = exec.Command(videoCmdLine[0], videoCmdLine[1:]...)
		videoCmd.Stdout = os.Stdout
		videoCmd.Stderr = os.Stderr
		err = videoCmd.Start()
		if err != nil {
			return nil, fmt.Errorf("cannot start video recorder process: %v", err)
		}
	}

	log.Printf("[%d] [PROXY_TO] [%s] [%.2fs]", requestId, u.String(), util.SecondsSince(s))
	cancel := func() {
		if videoCmd != nil {
			d.stopProcess(videoCmd)
		}
		d.stopProcess(cmd)
	}
	return &StartedService{Url: u, Cancel: cancel}, nil
}

func (d *Driver) stopProcess(cmd *exec.Cmd) {
	s := time.Now()
	log.Printf("[%d] [TERMINATING_PROCESS] [%d]", d.RequestId, cmd.Process.Pid)
	err := stopProc(cmd)
	if err != nil {
		log.Printf("[%d] [FAILED_TO_TERMINATE_PROCESS] [%d] [%v]", d.RequestId, cmd.Process.Pid, err)
		return
	}
	if file, ok := cmd.Stdout.(*os.File); ok && !d.CaptureDriverLogs && d.LogOutputDir != "" {
		file.Close()
	}
	log.Printf("[%d] [TERMINATED_PROCESS] [%d] [%.2fs]", d.RequestId, cmd.Process.Pid, util.SecondsSince(s))
}
