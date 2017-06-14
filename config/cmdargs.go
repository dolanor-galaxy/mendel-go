package config

import (
	"log"
	"flag"
	"os"
	"fmt"
	"path/filepath"
)

const DEFAULT_INPUT_FILE = "mendel-defaults.ini"

func Usage(exitCode int) {
	usageStr1 := `Usage:
  mendel {-c | -f} <filename> [-D <defaults-path>]
  mendel -d [-D <defaults-path>]

Performs a mendel run...

Options:
`

	usageStr2 := `
Examples:
  mendel -f /home/bob/mendel.in    # run with this input file
  mendel -d     # run with all default parameters from `+DEFAULT_INPUT_FILE+`
  mendel -c /home/bob/mendel.in    # create an input file primed with defaults, then you can edit it
`

	//if exitCode > 0 {
	fmt.Fprintln(os.Stderr, usageStr1)		// send it to stderr
	flag.PrintDefaults()
	fmt.Fprintln(os.Stderr, usageStr2)		// send it to stderr
	//} else {
	//	fmt.Println(usageStr1)		// send it to stdout
	//	flag.PrintDefaults()		//todo: do not yet know how to get this to print to stdout
	//	fmt.Println(usageStr2)		// send it to stdout
	//}
	os.Exit(exitCode)
}

// Config is the struct that gets filled in by TOML automatically from the input file.
type CommandArgs struct {
	InputFile, InputFileToCreate, DefaultFile string
}

// CmdArgs is the singleton instance of CommandArgs that can be accessed throughout the mendel code.
// It gets set in ReadCmdArgs().
var CmdArgs *CommandArgs

// ReadCmdArgs reads the command line args/flag, checks them, and puts them into the Config struct. Will exit if user input error.
// This is also the factory method for the CommandArgs class and will store the created instance in this packages CmdArgs var.
func ReadCmdArgs() {
	//log.Println("Reading command line arguments and flags...") 	// can not use verbosity here because we have not read the config file yet
	CmdArgs = &CommandArgs{} 		// create and set the singleton config
	var useDefaults bool
	flag.StringVar(&CmdArgs.InputFile, "f", "", "Run mendel with this input file (backed by the defaults file)")
	flag.StringVar(&CmdArgs.DefaultFile, "D", "", "Path to the defaults file. If not set, looks for "+DEFAULT_INPUT_FILE+" in the current directory or the directory of the executable")
	flag.StringVar(&CmdArgs.InputFileToCreate, "c", "", "Create a mendel input file (using default values) and then exit")
	flag.BoolVar(&useDefaults, "d", false, "Run mendel with all default parameters")
	flag.Usage = func() { Usage(0) }
	flag.Parse()
	// can use this to get values anywhere in the program: flag.Lookup("name").Value.String()
	// spew.Dump(flag.Lookup("f").Value.String())

	if CmdArgs.InputFileToCreate != "" {
		if CmdArgs.InputFile != "" || useDefaults { log.Println("Error: if you specify -c you can not specify either -f or -d"); Usage(1) }

	} else if useDefaults {
		if CmdArgs.InputFile != "" || CmdArgs.InputFileToCreate != "" { log.Println("Error: if you specify -d you can not specify either -f or -c"); Usage(1) }
		CmdArgs.InputFile = FindDefaultFile()

	} else if CmdArgs.InputFile != ""{
		// We already verified inputFileToCreate or useDefaults was not specified with this

	} else { Usage(0) }
}


// FindDefaultFile looks for the defaults input file and returns the 1st one it finds
func FindDefaultFile() string {
	// If they explicitly told us on the cmd line where it is use that
	if CmdArgs.DefaultFile != "" { return CmdArgs.DefaultFile }

	// Check for it in the current directory
	if _, err := os.Stat(DEFAULT_INPUT_FILE); err == nil { return DEFAULT_INPUT_FILE }

	// Check in the directory this executable came from
	executableDir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil { log.Fatal(err) }
	defaultFile := executableDir + "/" + DEFAULT_INPUT_FILE
	if _, err := os.Stat(defaultFile); err == nil {
		return defaultFile
	}

	return ""		// could not find it
}