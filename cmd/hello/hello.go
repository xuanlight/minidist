package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "hello:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("hello", flag.ContinueOnError)
	name := fs.String("name", "world", "who to greet")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil // -h 是用户想要的，不算失败
		}
		return err
	}

	msg, err := greet(*name)
	if err != nil {
		return err
	}
	fmt.Println(msg)
	return nil
}
