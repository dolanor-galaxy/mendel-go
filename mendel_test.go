package main

import (
	"testing"
	"os/exec"
	"errors"
	"io/ioutil"
	"bytes"
)


func TestMendelCase1(t *testing.T) {
	num := "1"
	testCase := "testcase" + num
	//t.Logf("Running mendel %v", testCase)
	inFileName := "test/input/mendel-" + testCase + ".ini"
	outFileName := "test/output/" + testCase + "/mendel.hst"
	expFileName := "test/expected/" + testCase + "/mendel.hst"
	cmdString := "./mendel-go"
	cmdFailed := false
	stdoutBytes, stderrBytes, err := runCmd(t, cmdString, "-f", inFileName)
	if err != nil {
		t.Errorf("Error running command %v: %v", cmdString, err)
		cmdFailed = true
	}

	if stdoutBytes != nil && cmdFailed { t.Logf("stdout: %s", stdoutBytes) }
	if stderrBytes != nil && len(stderrBytes) > 0 {
		t.Logf("stderr: %s", stderrBytes)
	}
	if cmdFailed { return }

	// Open the actual and expected the policy files
	compareFiles(t, outFileName, expFileName)

	// read output file
	//jInbytes, err := ioutil.ReadFile(filename)
	//if err != nil {
	//	return nil, nil, errors.New("Unable to read " + filename + " file, error: " + err.Error())
	//}
}


// Run a command with args, and return stdout, stderr
func runCmd(t *testing.T, commandString string, args ...string) ([]byte, []byte, error) {
	// For debug, build the full cmd string
	cmdStr := commandString
	for _, a := range args {
		cmdStr += " " + a
	}
	t.Logf("Running: %v\n", cmdStr)

	// Create the command object with its args
	cmd := exec.Command(commandString, args...)
	if cmd == nil {
		return nil, nil, errors.New("Did not return a command object, returned nil")
	}
	// Create the stdout pipe to hold the output from the command
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, errors.New("Error retrieving output from command, error: " + err.Error())
	}
	// Create the stderr pipe to hold the errors from the command
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, errors.New("Error retrieving stderr from command, error: " + err.Error())
	}
	// Start the command, which will block for input to std in
	err = cmd.Start()
	if err != nil {
		return nil, nil, errors.New("Unable to start command, error: " + err.Error())
	}
	err = error(nil)
	// Read the output from stdout and stderr into byte arrays
	// stdoutBytes, err := readPipe(stdout)
	stdoutBytes, err := ioutil.ReadAll(stdout)
	if err != nil {
		return nil, nil, errors.New("Error reading stdout, error: " + err.Error())
	}
	// stderrBytes, err := readPipe(stderr)
	stderrBytes, err := ioutil.ReadAll(stderr)
	if err != nil {
		return nil, nil, errors.New("Error reading stderr, error: " + err.Error())
	}
	// Now block waiting for the command to complete
	err = cmd.Wait()
	if err != nil {
		return stdoutBytes, stderrBytes, errors.New("Error waiting for command: " + err.Error())
	}

	return stdoutBytes, stderrBytes, error(nil)
}


// Compare the actual output file with the expected output
func compareFiles(t *testing.T, outputFilename, expectedFilename string) {
	if outputFile, err := ioutil.ReadFile(outputFilename); err != nil {
		t.Errorf("Unable to open %v file, error: %v", outputFilename, err)
		// Read the file into it's own byte array
	} else if expectedFile, err := ioutil.ReadFile(expectedFilename); err != nil {
		t.Errorf("Unable to open %v file, error: %v", expectedFilename, err)
		// Compare the bytes of both files. If there is a difference, then we have a problem so a bunch
		// of diagnostics will be written out.
	} else if bytes.Compare(outputFile, expectedFile) != 0 {
		t.Errorf("Newly created %v file does not match %v file.", outputFilename, expectedFilename)
		// if err := ioutil.WriteFile("./test/new_governor.sls", out2, 0644); err != nil {
		//     t.Errorf("Unable to write ./test/new_governor.sls file, error: %v", err)
		// }
		for idx, val := range outputFile {
			if val == expectedFile[idx] {
				continue
			} else {
				t.Errorf("Found difference at index %v", idx)
				t.Errorf("bytes around diff in output   file: %v", string(outputFile[idx-10:idx+10]))
				t.Errorf("bytes around diff in expected file: %v", string(expectedFile[idx-10:idx+10]))
				break
			}
		}
	}
}