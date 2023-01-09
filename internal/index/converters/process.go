package converters

import (
	"bufio"
	"log"
	"os/exec"
)

type (
	Process struct {
		converterName  string
		executablePath string
		cmd            *exec.Cmd
		input          chan []byte
		output         chan []byte
	}
)

// To stop the process, close the input channel.
// The output channel will be closed when the process exits.
func NewProcess(converterName string, executablePath string) *Process {
	process := Process{
		converterName:  converterName,
		executablePath: executablePath,
		cmd:            nil,
		input:          make(chan []byte),
		output:         make(chan []byte),
	}

	process.Start()
	return &process
}

func (process *Process) Start() {
	if process.IsRunning() {
		go process.runProcess()
	}
}

// func (process *Process) Abort() error {
// 	if process.cmd == nil {
// 		return nil
// 	}
// 	process.cmd.Process.Kill()
// 	close(process.input)
// 	err := <-process.status
// 	process.cmd = nil
// 	return err
// }

func (process *Process) IsRunning() bool {
	return process.cmd != nil
}

func ReadLine(reader *bufio.Reader) ([]byte, error) {
	result := []byte{}
	for {
		line, isPrefix, err := reader.ReadLine()
		if err != nil {
			return nil, err
		}
		result = append(result, line...)
		if !isPrefix {
			return result, nil
		}
	}
}

// Run until input channel is closed
func (process *Process) runProcess() {
	if process.IsRunning() {
		return
	}
	process.cmd = exec.Command(process.executablePath)
	stdout, err := process.cmd.StdoutPipe()
	if err != nil {
		log.Printf("Filter (%s): Failed to create stdout pipe: %q", process.converterName, err)
		close(process.output)
		return
	}

	// Pipe stdout to output channel
	go func() {
		reader := bufio.NewReaderSize(stdout, 65536)
		for {
			line, err := ReadLine(reader)
			if err != nil {
				break
			}
			process.output <- line
		}
		close(process.output)
	}()

	stderr, err := process.cmd.StderrPipe()
	if err != nil {
		log.Printf("Filter (%s): Failed to create stderr pipe: %q", process.converterName, err)
		stdout.Close()
		return
	}

	// Dump stderr directly
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Printf("Filter (%s) stderr: %s", process.converterName, scanner.Text())
		}
	}()

	stdin, err := process.cmd.StdinPipe()
	if err != nil {
		log.Printf("Filter (%s): Failed to create stdin pipe: %q", process.converterName, err)
		stdout.Close()
		stderr.Close()
		return
	}

	err = process.cmd.Start()
	if err != nil {
		log.Printf("Filter (%s): Failed to start process: %q", process.converterName, err)
		stdout.Close()
		stderr.Close()
		stdin.Close()
		return
	}

	for line := range process.input {
		if _, err := stdin.Write(line); err != nil {
			log.Printf("Filter (%s): Failed to write to stdin: %q", process.converterName, err)
			// wait for process to exit and close std pipes.
			process.cmd.Wait()
			// drain input channel to unblock caller
			for range process.input {
			}
			return
		}
	}

	process.cmd.Process.Kill()
	process.cmd.Wait()
}
