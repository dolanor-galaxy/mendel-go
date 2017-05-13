package config

import (
	"log"
	"flag"
	"os"
	"fmt"
)

const DEFAULT_INPUT_FILE = "./mendel-defaults.ini"

func Usage(exitCode int) {
	usageStr1 := `Usage:
  mendel {-c | -f} [filename]
  mendel -d

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
	InputFile, InputFileToCreate string
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
	flag.StringVar(&CmdArgs.InputFile, "f", "", "Run mendel with this input file")
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
		CmdArgs.InputFile = DEFAULT_INPUT_FILE

	} else if CmdArgs.InputFile != ""{
		// We already verified inputFileToCreate or useDefaults was not specified with this

	} else { Usage(0) }
}
